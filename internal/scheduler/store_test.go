package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testSchedule(id string) Schedule {
	return Schedule{
		ID: id, Name: "Schedule " + id, Enabled: true,
		Trigger: Trigger{Kind: TriggerOnce, RunAt: "2026-07-22T10:00:00Z"},
		Task:    TaskSpec{Kind: TaskAgent, Request: "inspect the repository", AgentID: "auditor", TargetID: "local", ReadOnly: true},
		Policy:  ExecutionPolicy{MaxAttempts: 1},
	}
}

func TestStoreScheduleAndJobRoundTrip(t *testing.T) {
	store := NewStore(t.TempDir())
	clock := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return clock }

	saved, err := store.PutSchedule(testSchedule("daily-review"))
	if err != nil {
		t.Fatalf("put schedule: %v", err)
	}
	if saved.CreatedAt != clock.Format(time.RFC3339Nano) || saved.Task.TargetID != "local" {
		t.Fatalf("unexpected saved schedule %#v", saved)
	}

	clock = clock.Add(time.Minute)
	saved.Name = "Updated review"
	updated, err := store.PutSchedule(saved)
	if err != nil {
		t.Fatalf("update schedule: %v", err)
	}
	if updated.CreatedAt != saved.CreatedAt || updated.UpdatedAt != clock.Format(time.RFC3339Nano) {
		t.Fatalf("timestamps not preserved: %#v", updated)
	}

	job, err := store.PutJob(Job{
		ID: "job-1", ScheduleID: saved.ID, ExecutionKey: "daily-review/2026-07-22T10:00:00Z",
		ScheduledFor: "2026-07-22T10:00:00Z", Status: JobPending,
	})
	if err != nil {
		t.Fatalf("put job: %v", err)
	}
	if job.CreatedAt == "" {
		t.Fatal("job creation time was not persisted")
	}

	reloaded := NewStore(filepath.Dir(store.Root()))
	snapshot, err := reloaded.Snapshot()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if snapshot.SchemaVersion != SchemaVersion || snapshot.Revision != 3 || len(snapshot.Schedules) != 1 || len(snapshot.Jobs) != 1 {
		t.Fatalf("unexpected snapshot %#v", snapshot)
	}
	if snapshot.Schedules[0].LastJobID != job.ID {
		t.Fatalf("schedule did not retain last job: %#v", snapshot.Schedules[0])
	}
}

func TestStoreEnsureIsIdempotent(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.Ensure(); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	before, err := store.Snapshot()
	if err != nil {
		t.Fatalf("snapshot before second ensure: %v", err)
	}
	if err := store.Ensure(); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	after, err := store.Snapshot()
	if err != nil {
		t.Fatalf("snapshot after second ensure: %v", err)
	}
	if after.Revision != before.Revision {
		t.Fatalf("ensure changed revision: before=%d after=%d", before.Revision, after.Revision)
	}
}

func TestStoreRejectsUnsafeScheduleAndDuplicateExecutionKey(t *testing.T) {
	store := NewStore(t.TempDir())
	unsafe := testSchedule("unsafe")
	unsafe.Task.ReadOnly = false
	if _, err := store.PutSchedule(unsafe); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only rejection, got %v", err)
	}
	remote := testSchedule("remote")
	remote.Task.TargetID = "server-1"
	if _, err := store.PutSchedule(remote); err == nil || !strings.Contains(err.Error(), "local") {
		t.Fatalf("expected local target rejection, got %v", err)
	}
	if _, err := store.PutSchedule(testSchedule("valid")); err != nil {
		t.Fatalf("put valid schedule: %v", err)
	}
	first := Job{ID: "job-1", ScheduleID: "valid", ExecutionKey: "unique-key", ScheduledFor: "2026-07-22T10:00:00Z", Status: JobPending}
	if _, err := store.PutJob(first); err != nil {
		t.Fatalf("put first job: %v", err)
	}
	first.ID = "job-2"
	if _, err := store.PutJob(first); err == nil || !strings.Contains(err.Error(), "execution key") {
		t.Fatalf("expected duplicate execution key rejection, got %v", err)
	}
}

func TestStoreMigratesSchemaZeroWithExactBackup(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, storeDirectory)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := struct {
		Schedules []Schedule `json:"schedules"`
	}{Schedules: []Schedule{testSchedule("legacy")}}
	raw, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(filepath.Join(root, stateFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dataRoot)
	if err := store.Ensure(); err != nil {
		t.Fatalf("ensure migrated store: %v", err)
	}
	backup, err := os.ReadFile(filepath.Join(root, legacyBackup))
	if err != nil {
		t.Fatalf("read migration backup: %v", err)
	}
	if string(backup) != string(raw) {
		t.Fatalf("migration backup changed source\nwant=%s\ngot=%s", raw, backup)
	}
	snapshot, err := store.Snapshot()
	if err != nil || snapshot.SchemaVersion != SchemaVersion || len(snapshot.Schedules) != 1 {
		t.Fatalf("unexpected migrated snapshot %#v err=%v", snapshot, err)
	}
}

func TestStoreCorruptStateIsNotReplaced(t *testing.T) {
	dataRoot := t.TempDir()
	root := filepath.Join(dataRoot, storeDirectory)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, stateFileName)
	raw := []byte("{not-json\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dataRoot)
	if err := store.Ensure(); err == nil {
		t.Fatal("expected corrupt state error")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(raw) {
		t.Fatalf("corrupt state was changed: %q err=%v", after, err)
	}
}

func TestStoreSerializesConcurrentWritersAcrossInstances(t *testing.T) {
	dataRoot := t.TempDir()
	const writers = 40
	var wg sync.WaitGroup
	errorsByWriter := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			store := NewStore(dataRoot)
			_, err := store.PutSchedule(testSchedule("schedule-" + strconv.Itoa(index)))
			errorsByWriter <- err
		}(i)
	}
	wg.Wait()
	close(errorsByWriter)
	for err := range errorsByWriter {
		if err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}
	snapshot, err := NewStore(dataRoot).Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Schedules) != writers || snapshot.Revision != writers {
		t.Fatalf("lost concurrent writes: schedules=%d revision=%d", len(snapshot.Schedules), snapshot.Revision)
	}
}
