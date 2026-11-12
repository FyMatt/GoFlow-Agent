package session

import (
	"path/filepath"
	"testing"
)

func TestStageCheckpointPublishesProgressWithoutChangingRunState(t *testing.T) {
	s := New(8)
	id := s.StartWorkflowRun("flow", "task")
	s.UpdateWorkflowRunState(WorkflowSnapshot{RunID: id, Name: "flow", Status: "running", NextStage: "verify"})
	if err := s.CheckpointWorkflowRunStages(id, []WorkflowRunStageSnapshot{{Stage: "implement", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	run, _ := s.WorkflowRun(id)
	if run.Status != "running" || run.NextStage != "verify" || len(run.CompletedStages) != 1 || run.CompletedStages[0].Status != "completed" {
		t.Fatalf("stale or incorrect live checkpoint: %#v", run)
	}
}

func TestUsageSurvivesReplayEvictionAndRestart(t *testing.T) {
	s := New(8)
	id := s.StartWorkflowRun("long-task", "complete the task")
	agentID := s.StartAgentRun("ordinary long task")
	for i := 0; i < 240; i++ {
		s.AppendWorkflowRunEvent(id, WorkflowRunEventSnapshot{Type: "token_usage", PromptTokens: 10, OutputTokens: 3, CachedTokens: 5})
		s.AppendAgentRunEvent(agentID, AgentRunEventSnapshot{Type: "token_usage", PromptTokens: 10, OutputTokens: 3, CachedTokens: 5})
	}
	for i := 0; i < 400; i++ {
		s.AppendWorkflowRunEvent(id, WorkflowRunEventSnapshot{Type: "status", Content: "progress"})
		s.AppendAgentRunEvent(agentID, AgentRunEventSnapshot{Type: "status", Content: "progress"})
	}
	check := func(state *State) {
		run, _ := state.WorkflowRun(id)
		ordinary, _ := state.AgentRun(agentID)
		if len(run.Events) > maxWorkflowRunEvents || run.BudgetReportedPromptTokens != 2400 || run.BudgetOutputTokens != 720 || run.BudgetCachedTokens != 1200 || len(run.UsageRecords) != 240 || len(ordinary.UsageRecords) != 240 {
			t.Fatalf("replay retention lost usage: input=%d output=%d cached=%d workflow=%d ordinary=%d", run.BudgetReportedPromptTokens, run.BudgetOutputTokens, run.BudgetCachedTokens, len(run.UsageRecords), len(ordinary.UsageRecords))
		}
		if n := len(SnapshotUsageRecords(state.Snapshot())); n != 480 {
			t.Fatalf("duplicate or lost usage: %d", n)
		}
	}
	check(s)
	path := filepath.Join(t.TempDir(), "session.json")
	s.SetPersistPath(path)
	if err := s.PersistConfigured(); err != nil {
		t.Fatal(err)
	}
	restored := New(8)
	if err := restored.Load(path); err != nil {
		t.Fatal(err)
	}
	check(restored)
	for i := 0; i <= maxWorkflowRuns; i++ {
		restored.StartWorkflowRun("new", "other task")
	}
	if n := len(SnapshotUsageRecords(restored.Snapshot())); n != 480 {
		t.Fatalf("run retention lost global usage: %d", n)
	}
}
