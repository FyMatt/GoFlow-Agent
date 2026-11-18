package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

type TaskRunState struct {
	RunID            string
	Status           string
	Error            string
	Terminal         bool
	Success          bool
	AwaitingApproval bool
}

type TaskRunner interface {
	StartScheduledTask(Schedule, Job) (string, error)
	FindScheduledTask(jobID string) (TaskRunState, bool)
	ScheduledTask(runID string) (TaskRunState, bool)
	CancelScheduledTask(runID, reason string) bool
}

type Notifier interface {
	Notify(Schedule, Job) error
}

type EngineOptions struct {
	TickInterval time.Duration
	Now          func() time.Time
	Notifier     Notifier
}

type Engine struct {
	store        *Store
	runner       TaskRunner
	tickInterval time.Duration
	now          func() time.Time
	notifier     Notifier
	tickMu       sync.Mutex
}

func NewEngine(store *Store, runner TaskRunner, opts EngineOptions) *Engine {
	interval := opts.TickInterval
	if interval <= 0 {
		interval = time.Second
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Engine{store: store, runner: runner, tickInterval: interval, now: now, notifier: opts.Notifier}
}

func (e *Engine) Run(ctx context.Context) error {
	if e == nil || e.store == nil || e.runner == nil {
		return fmt.Errorf("scheduler engine is not configured")
	}
	if err := e.Tick(); err != nil {
		return err
	}
	ticker := time.NewTicker(e.tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := e.Tick(); err != nil {
				return err
			}
		}
	}
}

func (e *Engine) Tick() error {
	if e == nil || e.store == nil || e.runner == nil {
		return fmt.Errorf("scheduler engine is not configured")
	}
	e.tickMu.Lock()
	defer e.tickMu.Unlock()
	now := e.now().UTC()
	active, claimedWithoutRun, err := e.reconcileJobs(now)
	if err != nil {
		return err
	}
	if claimedWithoutRun != nil {
		return e.startClaimedJob(*claimedWithoutRun, now)
	}
	if err := e.dispatchNotifications(now); err != nil {
		return err
	}
	if _, err := e.store.ensureDueJobs(now); err != nil {
		return err
	}
	job, ok, err := e.store.claimNextJob(now, active)
	if err != nil || !ok {
		return err
	}
	return e.startClaimedJob(job, now)
}

func (e *Engine) dispatchNotifications(now time.Time) error {
	if e.notifier == nil {
		return nil
	}
	jobs, err := e.store.ListJobs("")
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if !jobTerminal(job.Status) || job.NotifyStatus == "sent" || job.NotifyAttempts >= 3 || (job.NextNotifyAt != "" && !timestampDue(job.NextNotifyAt, now)) {
			continue
		}
		schedule, ok, err := e.store.GetSchedule(job.ScheduleID)
		if err != nil {
			return err
		}
		if !ok || !notificationEnabled(schedule.Notify, job.Status) {
			continue
		}
		notifyErr := e.notifier.Notify(schedule, job)
		_, _, err = e.store.updateJob(job.ID, func(item *Job) error {
			item.NotifyAttempts++
			item.UpdatedAt = now.Format(time.RFC3339Nano)
			if notifyErr == nil {
				item.NotifyStatus = "sent"
				item.NotifyError = ""
				item.NextNotifyAt = ""
				item.NotifiedAt = item.UpdatedAt
				return nil
			}
			item.NotifyError = notifyErr.Error()
			if item.NotifyAttempts >= 3 {
				item.NotifyStatus = "failed"
				item.NextNotifyAt = ""
			} else {
				item.NotifyStatus = "retry_waiting"
				item.NextNotifyAt = now.Add(time.Duration(item.NotifyAttempts*30) * time.Second).Format(time.RFC3339Nano)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) reconcileJobs(now time.Time) (bool, *Job, error) {
	jobs, err := e.store.ListJobs("")
	if err != nil {
		return false, nil, err
	}
	active := false
	for _, job := range jobs {
		if !jobActive(job.Status) {
			continue
		}
		if job.RunID == "" {
			if run, ok := e.runner.FindScheduledTask(job.ID); ok {
				schedule, _, scheduleErr := e.store.GetSchedule(job.ScheduleID)
				if scheduleErr != nil {
					return false, nil, scheduleErr
				}
				if err := e.applyRunState(job, run, now, schedule); err != nil {
					return false, nil, err
				}
				if !run.Terminal {
					active = true
				}
				continue
			}
			if job.Status == JobClaimed {
				copy := job
				return active, &copy, nil
			}
			if err := e.failJob(job, "scheduled task run is missing after restart", now); err != nil {
				return false, nil, err
			}
			continue
		}
		run, ok := e.runner.ScheduledTask(job.RunID)
		if !ok {
			if err := e.failJob(job, "scheduled task run is no longer available", now); err != nil {
				return false, nil, err
			}
			continue
		}
		schedule, scheduleOK, scheduleErr := e.store.GetSchedule(job.ScheduleID)
		if scheduleErr != nil {
			return false, nil, scheduleErr
		}
		if scheduleOK && jobTimedOut(job, schedule, now) {
			e.runner.CancelScheduledTask(job.RunID, "scheduled task timed out")
			if err := e.finishOrRetryJob(job, JobTimedOut, AttemptTimedOut, "scheduled task timed out", now, schedule, true); err != nil {
				return false, nil, err
			}
			continue
		}
		if scheduleOK && jobApprovalExpired(job, schedule, now) {
			e.runner.CancelScheduledTask(job.RunID, "scheduled task approval expired")
			if err := e.finishOrRetryJob(job, JobApprovalExpired, AttemptApprovalExpired, "scheduled task approval expired", now, schedule, false); err != nil {
				return false, nil, err
			}
			continue
		}
		if err := e.applyRunState(job, run, now, schedule); err != nil {
			return false, nil, err
		}
		if !run.Terminal {
			active = true
		}
	}
	return active, nil, nil
}

func (e *Engine) startClaimedJob(job Job, now time.Time) error {
	schedule, ok, err := e.store.GetSchedule(job.ScheduleID)
	if err != nil {
		return err
	}
	if !ok {
		return e.failJob(job, "schedule was deleted before execution", now)
	}
	runID, err := e.runner.StartScheduledTask(schedule, job)
	if err != nil {
		return e.failJob(job, err.Error(), now)
	}
	if strings.TrimSpace(runID) == "" {
		return e.failJob(job, "scheduled task did not allocate a run id", now)
	}
	_, found, err := e.store.updateJob(job.ID, func(item *Job) error {
		timestamp := now.Format(time.RFC3339Nano)
		item.RunID = strings.TrimSpace(runID)
		item.Status = JobRunning
		item.StartedAt = timestamp
		item.UpdatedAt = timestamp
		if len(item.Attempts) == 0 {
			item.Attempt = 1
			item.Attempts = append(item.Attempts, Attempt{Number: 1, Status: AttemptClaimed, ClaimedAt: item.ClaimedAt})
		}
		index := len(item.Attempts) - 1
		item.Attempts[index].Status = AttemptRunning
		item.Attempts[index].RunID = item.RunID
		item.Attempts[index].StartedAt = timestamp
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("claimed job %q disappeared", job.ID)
	}
	return nil
}

func (e *Engine) applyRunState(job Job, run TaskRunState, now time.Time, schedule Schedule) error {
	if run.Terminal && !run.Success {
		return e.finishOrRetryJob(job, JobFailed, AttemptFailed, run.Error, now, schedule, true)
	}
	_, _, err := e.store.updateJob(job.ID, func(item *Job) error {
		item.RunID = strings.TrimSpace(run.RunID)
		if item.RunID == "" {
			item.RunID = job.RunID
		}
		item.UpdatedAt = now.Format(time.RFC3339Nano)
		if run.Terminal {
			item.CompletedAt = item.UpdatedAt
			item.Error = strings.TrimSpace(run.Error)
			item.Status = JobSucceeded
			finishCurrentAttempt(item, item.Status, item.CompletedAt, item.Error)
		} else if run.AwaitingApproval {
			item.Status = JobAwaitingApproval
			if item.ApprovalExpiry == "" && schedule.Policy.ApprovalExpirySeconds > 0 {
				item.ApprovalExpiry = now.Add(time.Duration(schedule.Policy.ApprovalExpirySeconds) * time.Second).Format(time.RFC3339Nano)
			}
			setCurrentAttemptStatus(item, AttemptAwaitingApproval)
		} else {
			item.Status = JobRunning
		}
		return nil
	})
	return err
}

func (e *Engine) failJob(job Job, message string, now time.Time) error {
	schedule, ok, err := e.store.GetSchedule(job.ScheduleID)
	if err != nil {
		return err
	}
	if !ok {
		schedule.Policy.MaxAttempts = 1
	}
	return e.finishOrRetryJob(job, JobFailed, AttemptFailed, message, now, schedule, true)
}

func (e *Engine) finishOrRetryJob(job Job, terminalStatus, attemptStatus, message string, now time.Time, schedule Schedule, retry bool) error {
	_, _, err := e.store.updateJob(job.ID, func(item *Job) error {
		item.Error = strings.TrimSpace(message)
		item.UpdatedAt = now.Format(time.RFC3339Nano)
		finishCurrentAttempt(item, attemptStatus, item.UpdatedAt, item.Error)
		if retry && item.Attempt < schedule.Policy.MaxAttempts {
			item.Status = JobRetryWaiting
			item.RunID = ""
			item.StartedAt = ""
			item.ClaimedAt = ""
			item.ApprovalExpiry = ""
			item.NextAttemptAt = now.Add(time.Duration(schedule.Policy.RetryDelaySeconds) * time.Second).Format(time.RFC3339Nano)
			return nil
		}
		item.Status = terminalStatus
		item.CompletedAt = item.UpdatedAt
		return nil
	})
	return err
}

func jobTimedOut(job Job, schedule Schedule, now time.Time) bool {
	if schedule.Policy.TimeoutSeconds <= 0 || strings.TrimSpace(job.StartedAt) == "" {
		return false
	}
	started, err := parseTimestamp(job.StartedAt)
	return err == nil && !started.Add(time.Duration(schedule.Policy.TimeoutSeconds)*time.Second).After(now)
}

func jobApprovalExpired(job Job, schedule Schedule, now time.Time) bool {
	if job.Status != JobAwaitingApproval || schedule.Policy.ApprovalExpirySeconds <= 0 || strings.TrimSpace(job.ApprovalExpiry) == "" {
		return false
	}
	expires, err := parseTimestamp(job.ApprovalExpiry)
	return err == nil && !expires.After(now)
}

func finishCurrentAttempt(job *Job, status, at, message string) {
	if job == nil || len(job.Attempts) == 0 {
		return
	}
	index := len(job.Attempts) - 1
	job.Attempts[index].Status = status
	job.Attempts[index].CompletedAt = at
	job.Attempts[index].Error = message
}

func setCurrentAttemptStatus(job *Job, status string) {
	if job == nil || len(job.Attempts) == 0 {
		return
	}
	job.Attempts[len(job.Attempts)-1].Status = status
}

func executionKey(scheduleID string, runAt time.Time) string {
	return strings.TrimSpace(scheduleID) + "/" + runAt.UTC().Format(time.RFC3339Nano)
}

func jobIDForExecutionKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "job-" + hex.EncodeToString(sum[:12])
}

func jobActive(status string) bool {
	switch status {
	case JobClaimed, JobRunning, JobAwaitingApproval:
		return true
	default:
		return false
	}
}

func jobTerminal(status string) bool {
	switch status {
	case JobSucceeded, JobFailed, JobTimedOut, JobApprovalExpired, JobCancelled, JobSkipped:
		return true
	default:
		return false
	}
}

func notificationEnabled(notification Notification, status string) bool {
	if strings.TrimSpace(notification.Kind) == "" {
		return false
	}
	if status == JobSucceeded {
		return notification.OnSuccess
	}
	return notification.OnFailure
}
