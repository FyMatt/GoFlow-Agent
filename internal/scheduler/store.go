package scheduler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/persistence"
)

const (
	storeDirectory = "schedules"
	stateFileName  = "state.json"
	legacyBackup   = "state.schema-0.json"
)

var storeLocks sync.Map

type Store struct {
	root string
	mu   *sync.Mutex
	now  func() time.Time
}

func NewStore(dataRoot string) *Store {
	root := filepath.Join(filepath.Clean(strings.TrimSpace(dataRoot)), storeDirectory)
	lock, _ := storeLocks.LoadOrStore(strings.ToLower(root), &sync.Mutex{})
	return &Store{root: root, mu: lock.(*sync.Mutex), now: time.Now}
}

func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) Ensure() error {
	if err := s.validateStore(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("create schedule store: %w", err)
	}
	path := filepath.Join(s.root, stateFileName)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return s.writeLocked(State{SchemaVersion: SchemaVersion})
	} else if err != nil {
		return fmt.Errorf("inspect schedule state: %w", err)
	}
	state, legacyRaw, err := s.loadLocked(false)
	if err != nil {
		return err
	}
	if legacyRaw == nil {
		return nil
	}
	state.Revision++
	return s.writeLocked(state)
}

func (s *Store) Snapshot() (State, error) {
	if err := s.validateStore(); err != nil {
		return State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, _, err := s.loadLocked(false)
	return cloneState(state), err
}

func (s *Store) ListSchedules() ([]Schedule, error) {
	state, err := s.Snapshot()
	return state.Schedules, err
}

func (s *Store) GetSchedule(id string) (Schedule, bool, error) {
	id = strings.TrimSpace(id)
	state, err := s.Snapshot()
	if err != nil {
		return Schedule{}, false, err
	}
	for _, item := range state.Schedules {
		if item.ID == id {
			return item, true, nil
		}
	}
	return Schedule{}, false, nil
}

func (s *Store) PutSchedule(item Schedule) (Schedule, error) {
	var saved Schedule
	err := s.update(func(state *State) error {
		now := s.now().UTC().Format(time.RFC3339Nano)
		item = normalizeSchedule(item)
		if err := validateSchedule(item); err != nil {
			return err
		}
		index := -1
		triggerChanged := false
		for i := range state.Schedules {
			if state.Schedules[i].ID == item.ID {
				index = i
				break
			}
		}
		if index >= 0 {
			previousTrigger := state.Schedules[index].Trigger
			triggerChanged = item.Trigger != previousTrigger
			item.CreatedAt = state.Schedules[index].CreatedAt
			if item.LastJobID == "" {
				item.LastJobID = state.Schedules[index].LastJobID
			}
			if item.Trigger == previousTrigger && item.NextRunAt == "" {
				item.NextRunAt = state.Schedules[index].NextRunAt
			}
		} else {
			item.CreatedAt = now
		}
		if item.Trigger.Kind == TriggerOnce {
			item.NextRunAt = item.Trigger.RunAt
		} else if item.NextRunAt == "" || index < 0 || triggerChanged {
			next, err := cronNext(item.Trigger, s.now().UTC())
			if err != nil {
				return err
			}
			item.NextRunAt = next.Format(time.RFC3339Nano)
		}
		item.UpdatedAt = now
		if index >= 0 {
			state.Schedules[index] = item
		} else {
			state.Schedules = append(state.Schedules, item)
		}
		sortSchedules(state.Schedules)
		saved = item
		return nil
	})
	return saved, err
}

func (s *Store) DeleteSchedule(id string) (bool, error) {
	id = strings.TrimSpace(id)
	if err := validateID("schedule", id); err != nil {
		return false, err
	}
	deleted := false
	err := s.update(func(state *State) error {
		hasJobs := false
		for _, job := range state.Jobs {
			if job.ScheduleID == id {
				hasJobs = true
				break
			}
		}
		items := state.Schedules[:0]
		for _, item := range state.Schedules {
			if item.ID == id {
				deleted = true
				if hasJobs {
					item.Enabled = false
					item.Archived = true
					item.DeletedAt = s.now().UTC().Format(time.RFC3339Nano)
					item.UpdatedAt = item.DeletedAt
					items = append(items, item)
				}
				continue
			}
			items = append(items, item)
		}
		state.Schedules = items
		return nil
	})
	return deleted, err
}

func (s *Store) ListJobs(scheduleID string) ([]Job, error) {
	state, err := s.Snapshot()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(scheduleID) == "" {
		return state.Jobs, nil
	}
	items := make([]Job, 0)
	for _, item := range state.Jobs {
		if item.ScheduleID == scheduleID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (s *Store) GetJob(id string) (Job, bool, error) {
	id = strings.TrimSpace(id)
	state, err := s.Snapshot()
	if err != nil {
		return Job{}, false, err
	}
	for _, item := range state.Jobs {
		if item.ID == id {
			return item, true, nil
		}
	}
	return Job{}, false, nil
}

func (s *Store) PutJob(item Job) (Job, error) {
	var saved Job
	err := s.update(func(state *State) error {
		now := s.now().UTC().Format(time.RFC3339Nano)
		item = normalizeJob(item)
		if err := validateJob(item, state.Schedules); err != nil {
			return err
		}
		index := -1
		for i := range state.Jobs {
			if state.Jobs[i].ID == item.ID {
				index = i
				break
			}
			if state.Jobs[i].ExecutionKey == item.ExecutionKey {
				return fmt.Errorf("execution key %q already belongs to job %q", item.ExecutionKey, state.Jobs[i].ID)
			}
		}
		if index >= 0 {
			item.CreatedAt = state.Jobs[index].CreatedAt
		} else {
			item.CreatedAt = now
		}
		item.UpdatedAt = now
		if index >= 0 {
			state.Jobs[index] = item
		} else {
			state.Jobs = append(state.Jobs, item)
		}
		for i := range state.Schedules {
			if state.Schedules[i].ID == item.ScheduleID {
				state.Schedules[i].LastJobID = item.ID
				state.Schedules[i].UpdatedAt = now
			}
		}
		sortJobs(state.Jobs)
		saved = item
		return nil
	})
	return saved, err
}

func (s *Store) ensureDueJobs(now time.Time) ([]Job, error) {
	created := make([]Job, 0)
	err := s.updateIfChanged(func(state *State) (bool, error) {
		existing := make(map[string]struct{}, len(state.Jobs))
		for _, item := range state.Jobs {
			existing[item.ExecutionKey] = struct{}{}
		}
		changed := false
		for scheduleIndex := range state.Schedules {
			item := state.Schedules[scheduleIndex]
			if !item.Enabled {
				continue
			}
			if strings.TrimSpace(item.NextRunAt) == "" {
				continue
			}
			dueAt, err := requiredTimestamp("next_run_at", item.NextRunAt)
			if err != nil {
				return false, err
			}
			if dueAt.After(now) {
				continue
			}
			key := executionKey(item.ID, dueAt)
			if _, ok := existing[key]; ok {
				if err := advanceScheduleAfterOccurrence(&state.Schedules[scheduleIndex], now); err != nil {
					return false, err
				}
				changed = true
				continue
			}
			timestamp := now.UTC().Format(time.RFC3339Nano)
			status := JobPending
			errorText := ""
			if grace := time.Duration(item.Policy.MisfireGraceSeconds) * time.Second; grace > 0 && now.Sub(dueAt) > grace {
				status = JobSkipped
				errorText = "scheduled occurrence exceeded the misfire grace period"
			}
			job := Job{
				ID:           jobIDForExecutionKey(key),
				ScheduleID:   item.ID,
				ExecutionKey: key,
				ScheduledFor: dueAt.UTC().Format(time.RFC3339Nano),
				Status:       status,
				Error:        errorText,
				CreatedAt:    timestamp,
				UpdatedAt:    timestamp,
			}
			if status == JobSkipped {
				job.CompletedAt = timestamp
			}
			state.Jobs = append(state.Jobs, job)
			state.Schedules[scheduleIndex].LastJobID = job.ID
			state.Schedules[scheduleIndex].UpdatedAt = timestamp
			if err := advanceScheduleAfterOccurrence(&state.Schedules[scheduleIndex], now); err != nil {
				return false, err
			}
			existing[key] = struct{}{}
			created = append(created, job)
			changed = true
		}
		if changed {
			sortJobs(state.Jobs)
		}
		return changed, nil
	})
	return created, err
}

func advanceScheduleAfterOccurrence(item *Schedule, now time.Time) error {
	if item == nil {
		return nil
	}
	if item.Trigger.Kind == TriggerOnce {
		item.NextRunAt = ""
		return nil
	}
	next, err := cronNext(item.Trigger, now)
	if err != nil {
		return err
	}
	item.NextRunAt = next.Format(time.RFC3339Nano)
	return nil
}

func (s *Store) claimNextJob(now time.Time, overlapOnly bool) (Job, bool, error) {
	var claimed Job
	found := false
	err := s.updateIfChanged(func(state *State) (bool, error) {
		for index := len(state.Jobs) - 1; index >= 0; index-- {
			item := state.Jobs[index]
			if item.Status != JobPending && !(item.Status == JobRetryWaiting && timestampDue(item.NextAttemptAt, now)) {
				continue
			}
			scheduledFor, err := requiredTimestamp("scheduled_for", item.ScheduledFor)
			if err != nil {
				return false, err
			}
			if scheduledFor.After(now) {
				continue
			}
			if overlapOnly && !scheduleAllowsOverlap(item.ScheduleID, state.Schedules) {
				continue
			}
			timestamp := now.UTC().Format(time.RFC3339Nano)
			item.Status = JobClaimed
			item.ClaimedAt = timestamp
			item.UpdatedAt = timestamp
			item.NextAttemptAt = ""
			item.Attempt++
			item.Attempts = append(item.Attempts, Attempt{Number: item.Attempt, Status: AttemptClaimed, ClaimedAt: timestamp})
			state.Jobs[index] = item
			claimed = item
			found = true
			return true, nil
		}
		return false, nil
	})
	return claimed, found, err
}

func scheduleAllowsOverlap(id string, schedules []Schedule) bool {
	for _, item := range schedules {
		if item.ID == id {
			return item.Policy.AllowOverlap
		}
	}
	return false
}

func (s *Store) updateJob(id string, change func(*Job) error) (Job, bool, error) {
	id = strings.TrimSpace(id)
	var saved Job
	found := false
	err := s.updateIfChanged(func(state *State) (bool, error) {
		for i := range state.Jobs {
			if state.Jobs[i].ID != id {
				continue
			}
			before, err := json.Marshal(state.Jobs[i])
			if err != nil {
				return false, err
			}
			item := state.Jobs[i]
			if err := change(&item); err != nil {
				return false, err
			}
			item = normalizeJob(item)
			if err := validateJob(item, state.Schedules); err != nil {
				return false, err
			}
			after, err := json.Marshal(item)
			if err != nil {
				return false, err
			}
			found = true
			saved = item
			if bytes.Equal(before, after) {
				return false, nil
			}
			state.Jobs[i] = item
			return true, nil
		}
		return false, nil
	})
	return saved, found, err
}

func (s *Store) update(change func(*State) error) error {
	return s.updateIfChanged(func(state *State) (bool, error) {
		return true, change(state)
	})
}

func (s *Store) updateIfChanged(change func(*State) (bool, error)) error {
	if err := s.validateStore(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, legacyRaw, err := s.loadLocked(true)
	if err != nil {
		return err
	}
	before := state.Revision
	changed, err := change(&state)
	if err != nil {
		return err
	}
	if !changed && legacyRaw == nil {
		return nil
	}
	if err := validateState(state); err != nil {
		return err
	}
	state.Revision = before + 1
	return s.writeLocked(state)
}

func timestampDue(value string, now time.Time) bool {
	parsed, err := parseTimestamp(strings.TrimSpace(value))
	return err == nil && !parsed.After(now)
}

func (s *Store) loadLocked(create bool) (State, []byte, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return State{}, nil, fmt.Errorf("create schedule store: %w", err)
	}
	path := filepath.Join(s.root, stateFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return State{SchemaVersion: SchemaVersion}, nil, nil
		}
		return State{SchemaVersion: SchemaVersion}, nil, nil
	}
	if err != nil {
		return State{}, nil, fmt.Errorf("read schedule state: %w", err)
	}
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return State{}, nil, fmt.Errorf("parse schedule state: %w", err)
	}
	if header.SchemaVersion == 0 {
		state, err := migrateSchemaZero(raw)
		if err != nil {
			return State{}, nil, err
		}
		if err := s.backupLegacyLocked(raw); err != nil {
			return State{}, nil, err
		}
		return state, raw, nil
	}
	if header.SchemaVersion != SchemaVersion {
		return State{}, nil, fmt.Errorf("unsupported schedule schema version %d", header.SchemaVersion)
	}
	var state State
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, nil, fmt.Errorf("parse schedule state: %w", err)
	}
	if err := validateState(state); err != nil {
		return State{}, nil, err
	}
	return state, nil, nil
}

func (s *Store) backupLegacyLocked(raw []byte) error {
	path := filepath.Join(s.root, legacyBackup)
	if existing, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(existing, raw) {
			return fmt.Errorf("legacy schedule backup already exists with different content")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read legacy schedule backup: %w", err)
	}
	return persistence.WriteFileAtomic(path, raw, 0o600)
}

func (s *Store) writeLocked(state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode schedule state: %w", err)
	}
	data = append(data, '\n')
	return persistence.WriteFileAtomic(filepath.Join(s.root, stateFileName), data, 0o600)
}

func (s *Store) validateStore() error {
	if s == nil || s.mu == nil || strings.TrimSpace(s.root) == "" || filepath.Clean(s.root) == storeDirectory {
		return fmt.Errorf("schedule data root is not configured")
	}
	return nil
}

func migrateSchemaZero(raw []byte) (State, error) {
	var legacy struct {
		Revision  uint64     `json:"revision"`
		Schedules []Schedule `json:"schedules"`
		Jobs      []Job      `json:"jobs"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err != nil {
		return State{}, fmt.Errorf("parse legacy schedule state: %w", err)
	}
	state := State{SchemaVersion: SchemaVersion, Revision: legacy.Revision, Schedules: legacy.Schedules, Jobs: legacy.Jobs}
	for i := range state.Schedules {
		state.Schedules[i] = normalizeSchedule(state.Schedules[i])
		if state.Schedules[i].Trigger.Kind == TriggerOnce {
			state.Schedules[i].NextRunAt = state.Schedules[i].Trigger.RunAt
		} else if state.Schedules[i].NextRunAt == "" {
			next, nextErr := cronNext(state.Schedules[i].Trigger, time.Now().UTC())
			if nextErr != nil {
				return State{}, nextErr
			}
			state.Schedules[i].NextRunAt = next.Format(time.RFC3339Nano)
		}
	}
	for i := range state.Jobs {
		state.Jobs[i] = normalizeJob(state.Jobs[i])
	}
	if err := validateState(state); err != nil {
		return State{}, fmt.Errorf("migrate legacy schedule state: %w", err)
	}
	return state, nil
}

func normalizeSchedule(item Schedule) Schedule {
	item.ID = strings.TrimSpace(item.ID)
	item.Name = strings.TrimSpace(item.Name)
	item.Trigger.Kind = strings.ToLower(strings.TrimSpace(item.Trigger.Kind))
	item.Trigger.RunAt = strings.TrimSpace(item.Trigger.RunAt)
	item.Trigger.Expression = strings.TrimSpace(item.Trigger.Expression)
	item.Trigger.Timezone = strings.TrimSpace(item.Trigger.Timezone)
	item.Task.Kind = strings.ToLower(strings.TrimSpace(item.Task.Kind))
	item.Task.Request = strings.TrimSpace(item.Task.Request)
	item.Task.AgentID = strings.TrimSpace(item.Task.AgentID)
	item.Task.WorkflowName = strings.TrimSpace(item.Task.WorkflowName)
	item.Task.SkillName = strings.TrimSpace(item.Task.SkillName)
	item.Task.ToolName = strings.TrimSpace(item.Task.ToolName)
	item.Task.TargetID = strings.TrimSpace(item.Task.TargetID)
	item.Notify.Kind = strings.ToLower(strings.TrimSpace(item.Notify.Kind))
	item.Notify.URL = strings.TrimSpace(item.Notify.URL)
	item.Notify.Topic = strings.TrimSpace(item.Notify.Topic)
	if item.Notify.Kind != "" && !item.Notify.OnSuccess && !item.Notify.OnFailure {
		item.Notify.OnFailure = true
	}
	if item.Task.TargetID == "" {
		item.Task.TargetID = "local"
	}
	if item.Policy.MaxAttempts == 0 {
		item.Policy.MaxAttempts = 1
	}
	if item.Policy.TimeoutSeconds == 0 {
		item.Policy.TimeoutSeconds = 1800
	}
	if item.Policy.RetryDelaySeconds == 0 {
		item.Policy.RetryDelaySeconds = 60
	}
	if item.Policy.MisfireGraceSeconds == 0 {
		item.Policy.MisfireGraceSeconds = 300
	}
	if item.Policy.ApprovalExpirySeconds == 0 {
		item.Policy.ApprovalExpirySeconds = 86400
	}
	return item
}

func normalizeJob(item Job) Job {
	item.ID = strings.TrimSpace(item.ID)
	item.ScheduleID = strings.TrimSpace(item.ScheduleID)
	item.ExecutionKey = strings.TrimSpace(item.ExecutionKey)
	item.Status = strings.ToLower(strings.TrimSpace(item.Status))
	item.RunID = strings.TrimSpace(item.RunID)
	return item
}

func validateState(state State) error {
	if state.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schedule schema version %d", state.SchemaVersion)
	}
	scheduleIDs := make(map[string]struct{}, len(state.Schedules))
	for _, item := range state.Schedules {
		if err := validateSchedule(item); err != nil {
			return err
		}
		if _, exists := scheduleIDs[item.ID]; exists {
			return fmt.Errorf("duplicate schedule id %q", item.ID)
		}
		scheduleIDs[item.ID] = struct{}{}
	}
	jobIDs := make(map[string]struct{}, len(state.Jobs))
	executionKeys := make(map[string]struct{}, len(state.Jobs))
	for _, item := range state.Jobs {
		if err := validateJob(item, state.Schedules); err != nil {
			return err
		}
		if _, exists := jobIDs[item.ID]; exists {
			return fmt.Errorf("duplicate job id %q", item.ID)
		}
		jobIDs[item.ID] = struct{}{}
		if _, exists := executionKeys[item.ExecutionKey]; exists {
			return fmt.Errorf("duplicate execution key %q", item.ExecutionKey)
		}
		executionKeys[item.ExecutionKey] = struct{}{}
	}
	return nil
}

func validateSchedule(item Schedule) error {
	if err := validateID("schedule", item.ID); err != nil {
		return err
	}
	if item.Name == "" {
		return fmt.Errorf("schedule %q name is required", item.ID)
	}
	if item.Task.Request == "" {
		return fmt.Errorf("schedule %q request is required", item.ID)
	}
	if !item.Task.ReadOnly {
		return fmt.Errorf("schedule %q must be read-only", item.ID)
	}
	if item.Task.TargetID != "" && item.Task.TargetID != "local" {
		return fmt.Errorf("schedule %q target must be local", item.ID)
	}
	switch item.Task.Kind {
	case TaskAgent:
		if item.Task.WorkflowName != "" {
			return fmt.Errorf("agent schedule %q cannot select a workflow", item.ID)
		}
	case TaskWorkflow:
		if item.Task.WorkflowName == "" {
			return fmt.Errorf("workflow schedule %q requires workflow_name", item.ID)
		}
		if item.Task.AgentID != "" || item.Task.SkillName != "" || item.Task.ToolName != "" {
			return fmt.Errorf("workflow schedule %q cannot override agent resources", item.ID)
		}
	default:
		return fmt.Errorf("schedule %q task kind must be agent or workflow", item.ID)
	}
	switch item.Trigger.Kind {
	case TriggerOnce:
		if _, err := requiredTimestamp("run_at", item.Trigger.RunAt); err != nil {
			return fmt.Errorf("schedule %q: %w", item.ID, err)
		}
		if item.Trigger.Expression != "" {
			return fmt.Errorf("one-shot schedule %q cannot use a cron expression", item.ID)
		}
	case TriggerCron:
		if item.Trigger.Expression == "" {
			return fmt.Errorf("cron schedule %q expression is required", item.ID)
		}
		if item.Trigger.RunAt != "" {
			return fmt.Errorf("cron schedule %q cannot use run_at", item.ID)
		}
		if _, err := cronNext(item.Trigger, time.Now().UTC()); err != nil {
			return fmt.Errorf("schedule %q: %w", item.ID, err)
		}
	default:
		return fmt.Errorf("schedule %q trigger kind must be once or cron", item.ID)
	}
	if item.Policy.TimeoutSeconds < 0 || item.Policy.RetryDelaySeconds < 0 || item.Policy.MisfireGraceSeconds < 0 || item.Policy.ApprovalExpirySeconds < 0 {
		return fmt.Errorf("schedule %q policy durations cannot be negative", item.ID)
	}
	if item.Policy.MaxAttempts < 1 || item.Policy.MaxAttempts > 20 {
		return fmt.Errorf("schedule %q max_attempts must be between 1 and 20", item.ID)
	}
	if item.Notify.Kind != "" {
		if item.Notify.Kind != "webhook" && item.Notify.Kind != "ntfy" {
			return fmt.Errorf("schedule %q notification kind must be webhook or ntfy", item.ID)
		}
		if item.Notify.URL == "" {
			return fmt.Errorf("schedule %q notification URL is required", item.ID)
		}
		if item.Notify.Kind == "ntfy" && item.Notify.Topic == "" {
			return fmt.Errorf("schedule %q ntfy topic is required", item.ID)
		}
	}
	return nil
}

func validateJob(item Job, schedules []Schedule) error {
	if err := validateID("job", item.ID); err != nil {
		return err
	}
	if err := validateID("schedule", item.ScheduleID); err != nil {
		return err
	}
	found := false
	for _, schedule := range schedules {
		if schedule.ID == item.ScheduleID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("job %q references missing schedule %q", item.ID, item.ScheduleID)
	}
	if item.ExecutionKey == "" {
		return fmt.Errorf("job %q execution_key is required", item.ID)
	}
	if _, err := requiredTimestamp("scheduled_for", item.ScheduledFor); err != nil {
		return fmt.Errorf("job %q: %w", item.ID, err)
	}
	if !validJobStatus(item.Status) {
		return fmt.Errorf("job %q has invalid status %q", item.ID, item.Status)
	}
	if item.Attempt < 0 {
		return fmt.Errorf("job %q attempt cannot be negative", item.ID)
	}
	for i, attempt := range item.Attempts {
		if attempt.Number < 1 || !validAttemptStatus(attempt.Status) {
			return fmt.Errorf("job %q has invalid attempt at index %d", item.ID, i)
		}
	}
	return nil
}

func validateID(kind, value string) error {
	if value == "" || len(value) > 128 {
		return fmt.Errorf("%s id is required and must be at most 128 characters", kind)
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			continue
		}
		return fmt.Errorf("invalid %s id %q", kind, value)
	}
	return nil
}

func requiredTimestamp(field, value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	parsed, err := parseTimestamp(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must use RFC3339Nano: %w", field, err)
	}
	return parsed, nil
}

func validJobStatus(value string) bool {
	switch value {
	case JobPending, JobClaimed, JobRunning, JobAwaitingApproval, JobRetryWaiting, JobSucceeded, JobFailed, JobTimedOut, JobApprovalExpired, JobCancelled, JobSkipped:
		return true
	default:
		return false
	}
}

func validAttemptStatus(value string) bool {
	switch value {
	case AttemptClaimed, AttemptRunning, AttemptAwaitingApproval, AttemptSucceeded, AttemptFailed, AttemptTimedOut, AttemptApprovalExpired, AttemptCancelled:
		return true
	default:
		return false
	}
}

func sortSchedules(items []Schedule) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
}

func sortJobs(items []Job) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ScheduledFor != items[j].ScheduledFor {
			return items[i].ScheduledFor > items[j].ScheduledFor
		}
		return items[i].ID > items[j].ID
	})
}

func cloneState(state State) State {
	data, _ := json.Marshal(state)
	var cloned State
	_ = json.Unmarshal(data, &cloned)
	return cloned
}
