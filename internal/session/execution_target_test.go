package session

import "testing"

func TestNewRunsPersistLocalExecutionTarget(t *testing.T) {
	state := New(8)
	agentID := state.StartAgentRun("inspect project")
	agentRun, ok := state.AgentRun(agentID)
	if !ok || agentRun.TargetID != LocalTargetID {
		t.Fatalf("expected Agent run target %q, got %#v", LocalTargetID, agentRun)
	}

	workflowID := state.StartWorkflowRun("review", "inspect project")
	workflowRun, ok := state.WorkflowRun(workflowID)
	if !ok || workflowRun.TargetID != LocalTargetID {
		t.Fatalf("expected Workflow run target %q, got %#v", LocalTargetID, workflowRun)
	}
}

func TestRunStartOptionsPreserveExplicitExecutionTarget(t *testing.T) {
	state := New(8)
	agentID := state.StartAgentRunWithOptions("inspect host", AgentRunStartOptions{TargetID: "LOCAL"})
	agentRun, _ := state.AgentRun(agentID)
	if agentRun.TargetID != LocalTargetID {
		t.Fatalf("expected normalized Agent target, got %q", agentRun.TargetID)
	}

	workflowID := state.StartWorkflowRunWithOptions("review", "inspect host", WorkflowRunStartOptions{TargetID: " local "})
	workflowRun, _ := state.WorkflowRun(workflowID)
	if workflowRun.TargetID != LocalTargetID {
		t.Fatalf("expected normalized Workflow target, got %q", workflowRun.TargetID)
	}
}

func TestWorkflowTransitionsKeepTheSavedExecutionTarget(t *testing.T) {
	state := New(8)
	remote := state.StartWorkflowRunWithOptions("inspect", "read host", WorkflowRunStartOptions{TargetID: "edge-a"})
	other := state.StartWorkflowRunWithOptions("inspect", "read another host", WorkflowRunStartOptions{TargetID: "edge-b"})
	for _, supplied := range []string{"", "local", "edge-b"} {
		state.SetWorkflow(WorkflowSnapshot{RunID: other, Name: "inspect", TargetID: "edge-b"})
		state.SetWorkflow(WorkflowSnapshot{RunID: remote, Name: "inspect", Status: "running", TargetID: supplied})
		if got := state.Snapshot().Workflow.TargetID; got != "edge-a" {
			t.Errorf("resuming saved run with target %q selected %q", supplied, got)
		}
	}
	local := state.StartWorkflowRun("inspect", "local task")
	state.SetWorkflow(WorkflowSnapshot{RunID: local, Name: "inspect", Status: "running"})
	if got := state.Snapshot().Workflow.TargetID; got != LocalTargetID {
		t.Fatalf("a new local task inherited a remote target: %q", got)
	}
}
