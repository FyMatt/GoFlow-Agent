package slicescheduler

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestReadyBatchesParallelizeReadsAndSerializeWrites(t *testing.T) {
	plan, err := New([]session.WorkflowChangeSliceSnapshot{
		{ID: "inspect-a", ExecutionMode: ModeRead, Status: StatusPending},
		{ID: "inspect-b", ExecutionMode: ModeRead, Status: StatusPending},
		{ID: "write-a", ExecutionMode: ModeWrite, DependsOn: []string{"inspect-a", "inspect-b"}, Status: StatusPending},
		{ID: "write-b", ExecutionMode: ModeWrite, DependsOn: []string{"write-a"}, Status: StatusPending},
	})
	if err != nil {
		t.Fatal(err)
	}
	batches, err := plan.ReadyBatches()
	if err != nil || len(batches) != 1 || !batches[0].Parallel || len(batches[0].IDs) != 2 {
		t.Fatalf("expected one parallel read batch, got %#v err=%v", batches, err)
	}
	plan, err = plan.Start("inspect-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Start("write-a"); err == nil {
		t.Fatal("write must not start before both read dependencies complete")
	}
	plan, _ = plan.Complete("inspect-a", StatusImplemented)
	plan, _ = plan.Complete("inspect-b", StatusImplemented)
	batches, err = plan.ReadyBatches()
	if err != nil || len(batches) != 1 || batches[0].Parallel || batches[0].IDs[0] != "write-a" {
		t.Fatalf("expected first serialized write, got %#v err=%v", batches, err)
	}
}

func TestSchedulerRejectsCyclesAndUnknownDependencies(t *testing.T) {
	if _, err := New([]session.WorkflowChangeSliceSnapshot{
		{ID: "a", DependsOn: []string{"b"}}, {ID: "b", DependsOn: []string{"a"}},
	}); err == nil {
		t.Fatal("expected cycle rejection")
	}
	if _, err := New([]session.WorkflowChangeSliceSnapshot{{ID: "a", DependsOn: []string{"missing"}}}); err == nil {
		t.Fatal("expected unknown dependency rejection")
	}
}

func TestSchedulerRejectsUnorderedOverlappingWrites(t *testing.T) {
	if _, err := New([]session.WorkflowChangeSliceSnapshot{
		{ID: "a", ExecutionMode: ModeWrite, TargetFiles: []string{"config.json"}},
		{ID: "b", ExecutionMode: ModeWrite, TargetFiles: []string{"config.json"}},
	}); err == nil {
		t.Fatal("overlapping writes must declare an explicit dependency")
	}
	if _, err := New([]session.WorkflowChangeSliceSnapshot{
		{ID: "a", ExecutionMode: ModeWrite, TargetFiles: []string{"config.json"}},
		{ID: "b", ExecutionMode: ModeWrite, DependsOn: []string{"a"}, TargetFiles: []string{"config.json"}},
	}); err != nil {
		t.Fatalf("ordered overlapping writes should be valid: %v", err)
	}
	if _, err := New([]session.WorkflowChangeSliceSnapshot{
		{ID: "a", ExecutionMode: ModeWrite, TargetFiles: []string{"internal/config"}},
		{ID: "b", ExecutionMode: ModeWrite, TargetFiles: []string{"internal/config/config.go"}},
	}); err == nil {
		t.Fatal("directory and nested file targets overlap and must declare a dependency")
	}
}

func TestSchedulerRecoveryDoesNotReplayCompletedSideEffects(t *testing.T) {
	plan, err := New([]session.WorkflowChangeSliceSnapshot{
		{ID: "done", Status: StatusImplemented},
		{ID: "in-flight", Status: StatusRunning},
		{ID: "verified", Status: StatusVerified},
	})
	if err != nil {
		t.Fatal(err)
	}
	recovered, count, err := plan.RecoverInterrupted()
	if err != nil || count != 1 {
		t.Fatalf("expected one recovered slice, count=%d err=%v", count, err)
	}
	for _, slice := range recovered.Slices {
		switch slice.ID {
		case "done":
			if slice.Status != StatusImplemented {
				t.Fatalf("completed side effect was changed: %#v", slice)
			}
		case "in-flight":
			if slice.Status != StatusPending {
				t.Fatalf("in-flight slice was not reset: %#v", slice)
			}
		case "verified":
			if slice.Status != StatusVerified {
				t.Fatalf("verified slice was changed: %#v", slice)
			}
		}
	}
}

func TestSchedulerBlocksInvalidBackwardTransitions(t *testing.T) {
	plan, err := New([]session.WorkflowChangeSliceSnapshot{{ID: "slice", Status: StatusVerified}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Complete("slice", StatusImplemented); err == nil {
		t.Fatal("verified slice must not move backward")
	}
	plan, _ = New([]session.WorkflowChangeSliceSnapshot{{ID: "slice", Status: StatusBlocked}})
	if _, err := plan.Start("slice"); err == nil {
		t.Fatal("blocked slice requires explicit reset before restart")
	}
}
