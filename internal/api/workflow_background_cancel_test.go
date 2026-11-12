package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestBackgroundWorkflowCancellationIsNotOverwrittenByActionFailure(t *testing.T) {
	state := session.New(8)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	server := NewServer(runtimeRef)
	runID := state.StartWorkflowRun("cancel-flow", "cancel this task")
	done := make(chan struct{})
	_, err := server.startWorkflowRunBackgroundAction(runID, "cancel-flow", "continue-output", func(context.Context) (agent.WorkflowResult, error) {
		defer close(done)
		state.CancelWorkflowRun(runID, "user cancelled")
		return agent.WorkflowResult{}, fmt.Errorf("workflow stage verify: %w", context.Canceled)
	})
	if err != nil {
		t.Fatal(err)
	}
	<-done
	server.workflowExecMu.Lock()
	defer server.workflowExecMu.Unlock()
	run, ok := state.WorkflowRun(runID)
	if !ok || run.Status != "cancelled" {
		t.Fatalf("cancellation must remain terminal: %#v", run)
	}
	for _, event := range run.Events {
		if event.WorkflowStatus == "action_failed" {
			t.Fatalf("cancellation was overwritten: %#v", event)
		}
	}
}
