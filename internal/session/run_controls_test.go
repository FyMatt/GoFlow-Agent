package session

import "testing"

func TestRunControlsPersistOnAgentAndWorkflowRuns(t *testing.T) {
	state := New(8)
	agentID := state.StartAgentRunWithOptions("inspect", AgentRunStartOptions{ReasoningEffort: "high", PermissionMode: "workspace"})
	agentRun, ok := state.AgentRun(agentID)
	if !ok || agentRun.ReasoningEffort != "high" || agentRun.PermissionMode != "workspace" {
		t.Fatalf("agent controls were not preserved: %#v", agentRun)
	}
	workflowID := state.StartWorkflowRunWithOptions("review", "inspect", WorkflowRunStartOptions{ReasoningEffort: "low", PermissionMode: "full"})
	workflowRun, ok := state.WorkflowRun(workflowID)
	if !ok || workflowRun.ReasoningEffort != "low" || workflowRun.PermissionMode != "full" {
		t.Fatalf("workflow controls were not preserved: %#v", workflowRun)
	}
}
