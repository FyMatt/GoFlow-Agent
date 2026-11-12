package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func taskReadFixture(t *testing.T, count int, marker string) *Store {
	t.Helper()
	store := NewStore(t.TempDir())
	dir := filepath.Join(store.Root(), taskMemoryDirRelativePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		task := TaskSummary{
			ID: fmt.Sprintf("task-%03d", count-i), UpdatedAt: fmt.Sprintf("2026-10-24T00:00:0%dZ", i%3),
			UserGoal: marker + strings.Repeat(" evidence", (i%5)*100), KeyDecisions: []string{marker},
		}
		if err := writeJSONFile(filepath.Join(dir, fmt.Sprintf("entry-%03d.json", i)), task); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func checkTasksMatchSerial(t *testing.T, store *Store, limit int) []TaskSummary {
	t.Helper()
	want, wantErr := tasksSerial(store, limit)
	got, err := store.Tasks(limit)
	if (err == nil) != (wantErr == nil) || (err != nil && err.Error() != wantErr.Error()) || !reflect.DeepEqual(got, want) {
		t.Fatalf("limit %d: got=%#v err=%v want=%#v err=%v", limit, got, err, want, wantErr)
	}
	return got
}

func TestTasksPreserveSerialOrderingAndLimits(t *testing.T) {
	for _, count := range []int{0, 16, 32, 33, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			store := taskReadFixture(t, count, "domain")
			for range 3 {
				for _, limit := range []int{-1, 0, 1, 20, 200} {
					got := checkTasksMatchSerial(t, store, limit)
					wantCount := count
					if limit > 0 {
						wantCount = min(count, limit)
					}
					if len(got) != wantCount {
						t.Fatalf("returned %d tasks, want %d", len(got), wantCount)
					}
				}
			}
		})
	}
}

func TestTasksSkipCorruptFilesAndReadEditsOutsideLimit(t *testing.T) {
	store := taskReadFixture(t, 64, "domain")
	dir := filepath.Join(store.Root(), taskMemoryDirRelativePath)
	for name, data := range map[string]string{
		"corrupt.json":     `{"id":"partial","updated_at":`,
		"wrong-type.json":  `{"id":"partial","key_decisions":["discard"],"updated_at":123}`,
		"empty.json":       `{}`,
		"null.json":        `null`,
		"uppercase.JSON":   `{"id":"uppercase"}`,
		"trailing.json":    "{\"id\":\"trailing\"}\ntrailing content",
		"ignored.txt":      `{"id":"ignored"}`,
		"ignored.json.bak": `{"id":"ignored"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSONFile(filepath.Join(dir, "directory.json", "nested.json"), TaskSummary{ID: "nested"}); err != nil {
		t.Fatal(err)
	}
	got := checkTasksMatchSerial(t, store, 0)
	if len(got) != 68 {
		t.Fatalf("expected valid, empty, null, uppercase and trailing JSON only; got %d", len(got))
	}
	for _, unused := range got[len(got):cap(got)] {
		if !reflect.DeepEqual(unused, TaskSummary{}) {
			t.Fatalf("discarded task retained in slice capacity: %#v", unused)
		}
	}
	checkTasksMatchSerial(t, store, 1)
	// This entry was outside the limited result. Change it without changing its
	// size or timestamp: every call must still read its current contents.
	path := filepath.Join(dir, "entry-063.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var edited TaskSummary
	if err := readJSONFile(path, &edited); err != nil {
		t.Fatal(err)
	}
	edited.UpdatedAt = "2030-10-24T00:00:00Z"
	if err := writeJSONFile(path, edited); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		t.Fatalf("same-metadata edit not established: info=%v err=%v", after, err)
	}
	if got := checkTasksMatchSerial(t, store, 1); got[0].ID != edited.ID {
		t.Fatalf("edited task outside previous limit was missed: %#v", got)
	}
	if err := writeJSONFile(filepath.Join(dir, "corrupt.json"), TaskSummary{ID: "repaired", UpdatedAt: "2031-10-24T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if got := checkTasksMatchSerial(t, store, 1); got[0].ID != "repaired" {
		t.Fatalf("repaired task was not reread: %#v", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	checkTasksMatchSerial(t, store, 0)
}

func TestTasksConcurrentCallsKeepResultsAndWorkspacesIsolated(t *testing.T) {
	stores := []*Store{taskReadFixture(t, 48, "operations"), taskReadFixture(t, 48, "research")}
	var callers sync.WaitGroup
	for i := range 8 {
		for _, store := range stores {
			callers.Go(func() {
				limit := []int{1, 20, 0, -1}[i%4]
				want, err := tasksSerial(store, limit)
				if err != nil {
					t.Error(err)
					return
				}
				for range 3 {
					got, err := store.Tasks(limit)
					if err != nil || !reflect.DeepEqual(got, want) {
						t.Errorf("concurrent results crossed calls: limit=%d err=%v", limit, err)
						return
					}
					got[0].UserGoal = "caller mutation"
					got[0].KeyDecisions[0] = "caller mutation"
				}
			})
		}
	}
	callers.Wait()
}

func TestTasksDirectoryErrorsMatchSerial(t *testing.T) {
	checkTasksMatchSerial(t, NewStore(""), 20)
	checkTasksMatchSerial(t, (*Store)(nil), 20)
	store := NewStore(t.TempDir())
	if got := checkTasksMatchSerial(t, store, 20); got != nil {
		t.Fatalf("missing directory should return nil, got %#v", got)
	}
	dir := filepath.Join(store.Root(), taskMemoryDirRelativePath)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	checkTasksMatchSerial(t, store, 20)
}
