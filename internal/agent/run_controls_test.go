package agent

import (
	"context"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestRunControlsRestoreFromDurableRunSnapshots(t *testing.T) {
	state := session.New(8)
	agentRunID := state.StartAgentRunWithOptions("inspect", session.AgentRunStartOptions{ReasoningEffort: "high", PermissionMode: RunPermissionWorkspace})
	runtimeRef := &Runtime{session: state}
	controls := runControlsFromContext(WithAgentRunID(context.Background(), agentRunID), runtimeRef)
	if controls.ReasoningEffort != "high" || controls.PermissionMode != RunPermissionWorkspace {
		t.Fatalf("agent controls were not restored: %#v", controls)
	}

	workflowRunID := state.StartWorkflowRunWithOptions("review", "inspect", session.WorkflowRunStartOptions{ReasoningEffort: "low", PermissionMode: RunPermissionFull})
	state.SetWorkflow(session.WorkflowSnapshot{RunID: workflowRunID, Name: "review", Status: "running"})
	controls = runControlsFromContext(context.Background(), runtimeRef)
	if controls.ReasoningEffort != "low" || controls.PermissionMode != RunPermissionFull {
		t.Fatalf("workflow controls were not restored: %#v", controls)
	}
}
