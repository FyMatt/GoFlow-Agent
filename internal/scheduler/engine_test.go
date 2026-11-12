package scheduler

import (
	"path/filepath"
	"testing"
	"time"
)

type fakeTaskRunner struct {
	starts     int
	cancels    int
	runs       map[string]TaskRunState
	runsByJob  map[string]TaskRunState
	startError error
}

type fakeNotifier struct {
	calls int
	err   error
}

func (f *fakeNotifier) Notify(Schedule, Job) error {
	f.calls++
	return f.err
}

func (f *fakeTaskRunner) StartScheduledTask(_ Schedule, job Job) (string, error) {
	f.starts++
	if f.startError != nil {
		return "", f.startError
	}
	run := TaskRunState{RunID: "run-" + job.ID, Status: "running"}
	if f.runs == nil {
		f.runs = map[string]TaskRunState{}
	}
	if f.runsByJob == nil {
		f.runsByJob = map[string]TaskRunState{}
	}
	f.runs[run.RunID] = run
	f.runsByJob[job.ID] = run
	return run.RunID, nil
}

func (f *fakeTaskRunner) FindScheduledTask(jobID string) (TaskRunState, bool) {
	run, ok := f.runsByJob[jobID]
	return run, ok
}

func (f *fakeTaskRunner) ScheduledTask(runID string) (TaskRunState, bool) {
	run, ok := f.runs[runID]
	return run, ok
}

func (f *fakeTaskRunner) CancelScheduledTask(string, string) bool {
	f.cancels++
	return true
}

func TestEngineCreatesAndStartsOneShotExactlyOnce(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	store := NewStore(t.TempDir())
	item := testSchedule("one-shot")
	item.Trigger.RunAt = now.Add(-time.Minute).Format(time.RFC3339Nano)
	if _, err := store.PutSchedule(item); err != nil {
		t.Fatal(err)
	}
	runner := &fakeTaskRunner{}
	engine := NewEngine(store, runner, EngineOptions{Now: func() time.Time { return now }})
	if err := engine.Tick(); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if err := engine.Tick(); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	jobs, err := store.ListJobs("")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || runner.starts != 1 || jobs[0].Status != JobRunning || jobs[0].RunID == "" {
		t.Fatalf("unexpected exactly-once state jobs=%#v starts=%d", jobs, runner.starts)
	}
}

func TestEngineRestartFindsTaskCreatedBeforeRunWasAttached(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	store := NewStore(t.TempDir())
	item := testSchedule("restart-window")
	if _, err := store.PutSchedule(item); err != nil {
		t.Fatal(err)
	}
	job, err := store.PutJob(Job{ID: "job-restart", ScheduleID: item.ID, ExecutionKey: "restart/key", ScheduledFor: now.Format(time.RFC3339Nano), Status: JobClaimed, ClaimedAt: now.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	run := TaskRunState{RunID: "existing-run", Status: "running"}
	runner := &fakeTaskRunner{runs: map[string]TaskRunState{run.RunID: run}, runsByJob: map[string]TaskRunState{job.ID: run}}
	engine := NewEngine(NewStore(filepath.Dir(store.Root())), runner, EngineOptions{Now: func() time.Time { return now.Add(time.Second) }})
	if err := engine.Tick(); err != nil {
		t.Fatalf("restart tick: %v", err)
	}
	updated, ok, err := store.GetJob(job.ID)
	if err != nil || !ok {
		t.Fatalf("get job: ok=%t err=%v", ok, err)
	}
	if runner.starts != 0 || updated.RunID != run.RunID || updated.Status != JobRunning {
		t.Fatalf("existing task was not reconciled: job=%#v starts=%d", updated, runner.starts)
	}
}

func TestEngineCompletesJobFromOrdinaryTaskStatus(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	store := NewStore(t.TempDir())
	item := testSchedule("complete")
	item.Trigger.RunAt = now.Format(time.RFC3339Nano)
	if _, err := store.PutSchedule(item); err != nil {
		t.Fatal(err)
	}
	runner := &fakeTaskRunner{}
	engine := NewEngine(store, runner, EngineOptions{Now: func() time.Time { return now }})
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.ListJobs("")
	run := runner.runs[jobs[0].RunID]
	run.Terminal = true
	run.Success = true
	run.Status = "completed"
	runner.runs[run.RunID] = run
	runner.runsByJob[jobs[0].ID] = run
	now = now.Add(time.Second)
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	completed, _, _ := store.GetJob(jobs[0].ID)
	if completed.Status != JobSucceeded || completed.CompletedAt == "" || len(completed.Attempts) != 1 || completed.Attempts[0].Status != JobSucceeded {
		t.Fatalf("unexpected completed job %#v", completed)
	}
}

func TestEngineCronAdvancesWithoutCatchupFlood(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 30, 0, time.UTC)
	store := NewStore(t.TempDir())
	store.now = func() time.Time { return now }
	item := testSchedule("cron-review")
	item.Trigger = Trigger{Kind: TriggerCron, Expression: "* * * * *", Timezone: "UTC"}
	item.Policy.MisfireGraceSeconds = 120
	saved, err := store.PutSchedule(item)
	if err != nil {
		t.Fatal(err)
	}
	if saved.NextRunAt != "2026-07-22T10:01:00Z" {
		t.Fatalf("unexpected first cron time %q", saved.NextRunAt)
	}
	now = time.Date(2026, 7, 22, 10, 5, 0, 0, time.UTC)
	runner := &fakeTaskRunner{}
	engine := NewEngine(store, runner, EngineOptions{Now: func() time.Time { return now }})
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.ListJobs("")
	updated, _, _ := store.GetSchedule(item.ID)
	if len(jobs) != 1 || jobs[0].Status != JobSkipped || runner.starts != 0 || updated.NextRunAt != "2026-07-22T10:06:00Z" {
		t.Fatalf("unexpected cron misfire jobs=%#v schedule=%#v starts=%d", jobs, updated, runner.starts)
	}
}

func TestEngineRetriesAfterConfiguredDelay(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	store := NewStore(t.TempDir())
	item := testSchedule("retry")
	item.Trigger.RunAt = now.Format(time.RFC3339Nano)
	item.Policy.MaxAttempts = 2
	item.Policy.RetryDelaySeconds = 30
	if _, err := store.PutSchedule(item); err != nil {
		t.Fatal(err)
	}
	runner := &fakeTaskRunner{startError: errFakeStart}
	engine := NewEngine(store, runner, EngineOptions{Now: func() time.Time { return now }})
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.ListJobs("")
	if jobs[0].Status != JobRetryWaiting || jobs[0].Attempt != 1 {
		t.Fatalf("first failure did not wait for retry: %#v", jobs[0])
	}
	runner.startError = nil
	now = now.Add(29 * time.Second)
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	if runner.starts != 1 {
		t.Fatalf("retry started too early: %d", runner.starts)
	}
	now = now.Add(time.Second)
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	job, _, _ := store.GetJob(jobs[0].ID)
	if runner.starts != 2 || job.Status != JobRunning || job.Attempt != 2 || len(job.Attempts) != 2 {
		t.Fatalf("unexpected retry result job=%#v starts=%d", job, runner.starts)
	}
}

var errFakeStart = &fakeStartError{}

type fakeStartError struct{}

func (*fakeStartError) Error() string { return "injected start failure" }

func TestEngineTimesOutActiveTask(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	store := NewStore(t.TempDir())
	item := testSchedule("timeout")
	item.Trigger.RunAt = now.Format(time.RFC3339Nano)
	item.Policy.TimeoutSeconds = 10
	if _, err := store.PutSchedule(item); err != nil {
		t.Fatal(err)
	}
	runner := &fakeTaskRunner{}
	engine := NewEngine(store, runner, EngineOptions{Now: func() time.Time { return now }})
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.ListJobs("")
	now = now.Add(10 * time.Second)
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	job, _, _ := store.GetJob(jobs[0].ID)
	if runner.cancels != 1 || job.Status != JobTimedOut || job.Attempts[0].Status != AttemptTimedOut {
		t.Fatalf("unexpected timeout job=%#v cancels=%d", job, runner.cancels)
	}
}

func TestEngineExpiresPendingApproval(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	store := NewStore(t.TempDir())
	item := testSchedule("approval-expiry")
	item.Trigger.RunAt = now.Format(time.RFC3339Nano)
	item.Policy.ApprovalExpirySeconds = 20
	if _, err := store.PutSchedule(item); err != nil {
		t.Fatal(err)
	}
	runner := &fakeTaskRunner{}
	engine := NewEngine(store, runner, EngineOptions{Now: func() time.Time { return now }})
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.ListJobs("")
	run := runner.runs[jobs[0].RunID]
	run.AwaitingApproval = true
	runner.runs[run.RunID] = run
	now = now.Add(time.Second)
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(20 * time.Second)
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	job, _, _ := store.GetJob(jobs[0].ID)
	if runner.cancels != 1 || job.Status != JobApprovalExpired || job.ApprovalExpiry == "" {
		t.Fatalf("unexpected approval expiry job=%#v cancels=%d", job, runner.cancels)
	}
}

func TestEngineAllowsExplicitOverlap(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	store := NewStore(t.TempDir())
	first := testSchedule("serial-first")
	first.Trigger.RunAt = now.Format(time.RFC3339Nano)
	second := testSchedule("overlap-second")
	second.Trigger.RunAt = now.Format(time.RFC3339Nano)
	second.Policy.AllowOverlap = true
	if _, err := store.PutSchedule(first); err != nil {
		t.Fatal(err)
	}
	runner := &fakeTaskRunner{}
	engine := NewEngine(store, runner, EngineOptions{Now: func() time.Time { return now }})
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutSchedule(second); err != nil {
		t.Fatal(err)
	}
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	if runner.starts != 2 {
		t.Fatalf("explicit overlap did not start second task: starts=%d", runner.starts)
	}
}

func TestEngineDeliversTerminalNotificationOnce(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	store := NewStore(t.TempDir())
	item := testSchedule("notify")
	item.Trigger.RunAt = now.Format(time.RFC3339Nano)
	item.Notify = Notification{Kind: "webhook", URL: "https://example.com/hook", OnSuccess: true}
	if _, err := store.PutSchedule(item); err != nil {
		t.Fatal(err)
	}
	runner := &fakeTaskRunner{}
	notifier := &fakeNotifier{}
	engine := NewEngine(store, runner, EngineOptions{Now: func() time.Time { return now }, Notifier: notifier})
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	jobs, _ := store.ListJobs("")
	run := runner.runs[jobs[0].RunID]
	run.Terminal = true
	run.Success = true
	runner.runs[run.RunID] = run
	now = now.Add(time.Second)
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Tick(); err != nil {
		t.Fatal(err)
	}
	job, _, _ := store.GetJob(jobs[0].ID)
	if notifier.calls != 1 || job.NotifyStatus != "sent" || job.NotifiedAt == "" {
		t.Fatalf("unexpected notification job=%#v calls=%d", job, notifier.calls)
	}
}
