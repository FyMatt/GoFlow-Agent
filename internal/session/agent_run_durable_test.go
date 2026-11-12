package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestDurableSnapshotWaitsForAgentRunTerminalPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	state := New(8)
	state.SetPersistPath(path)
	runID, err := state.StartAgentRunWithOptionsDurable("finish durably", AgentRunStartOptions{AgentID: "software-engineer"})
	if err != nil {
		t.Fatalf("StartAgentRunWithOptionsDurable: %v", err)
	}

	state.saveMu.Lock()
	completeDone := make(chan error, 1)
	go func() {
		_, _, completeErr := state.CompleteAgentRunDurable(runID, "completed", schema.AgentResult{
			Output:  "verified result",
			AgentID: "software-engineer",
			Mode:    "fix",
		})
		completeDone <- completeErr
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		run, ok := state.AgentRun(runID)
		if ok && run.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			state.saveMu.Unlock()
			t.Fatal("terminal transition did not reach memory")
		}
		time.Sleep(time.Millisecond)
	}

	snapshotDone := make(chan Snapshot, 1)
	go func() { snapshotDone <- state.DurableSnapshot() }()
	select {
	case <-snapshotDone:
		state.saveMu.Unlock()
		t.Fatal("durable snapshot exposed terminal state before persistence completed")
	case <-time.After(50 * time.Millisecond):
	}

	state.saveMu.Unlock()
	if err := <-completeDone; err != nil {
		t.Fatalf("CompleteAgentRunDurable: %v", err)
	}
	select {
	case snapshot := <-snapshotDone:
		if len(snapshot.AgentRuns) != 1 || snapshot.AgentRuns[0].Status != "completed" {
			t.Fatalf("unexpected durable snapshot: %#v", snapshot.AgentRuns)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("durable snapshot did not resume after persistence")
	}

	restored := New(8)
	if err := restored.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	persisted, ok := restored.AgentRun(runID)
	if !ok || persisted.Status != "completed" || persisted.Output != "verified result" {
		t.Fatalf("unexpected persisted run: %#v", persisted)
	}
}

func TestReconcileInterruptedAgentRunsPreservesResumableContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	state := New(8)
	state.SetPersistPath(path)
	runID, err := state.StartAgentRunWithOptionsDurable("finish the staged task", AgentRunStartOptions{AgentID: "software-engineer"})
	if err != nil {
		t.Fatalf("StartAgentRunWithOptionsDurable: %v", err)
	}
	if _, ok := state.SetAgentRunResumeContext(runID, AgentRunResumeContextSnapshot{
		AgentID:          "software-engineer",
		Messages:         []schema.Message{{Role: "user", Content: "finish the staged task"}},
		CollectedResults: []schema.ToolResult{{CallID: "call-1", Content: "saved result"}},
	}); !ok {
		t.Fatal("expected resume context to be stored")
	}
	if err := state.PersistConfigured(); err != nil {
		t.Fatalf("PersistConfigured: %v", err)
	}

	restarted := New(8)
	restarted.SetPersistPath(path)
	if err := restarted.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	count, err := restarted.ReconcileInterruptedAgentRuns()
	if err != nil {
		t.Fatalf("ReconcileInterruptedAgentRuns: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one interrupted Agent run, got %d", count)
	}
	run, ok := restarted.AgentRun(runID)
	if !ok || run.Status != "paused_need_more_budget" || run.ResumeContext == nil {
		t.Fatalf("expected resumable paused Agent run, got %#v", run)
	}
	last := run.Events[len(run.Events)-1]
	if last.Type != "agent_run_paused" || last.Reason != "process_restart" || !last.NeedsAction || !last.Suspended {
		t.Fatalf("expected explicit resumable restart event, got %#v", last)
	}

	reloaded := New(8)
	reloaded.SetPersistPath(path)
	if err := reloaded.Load(path); err != nil {
		t.Fatalf("reload reconciled session: %v", err)
	}
	persisted, ok := reloaded.AgentRun(runID)
	if !ok || persisted.Status != "paused_need_more_budget" || persisted.ResumeContext == nil {
		t.Fatalf("expected reconciled Agent state on disk, got %#v", persisted)
	}
}
