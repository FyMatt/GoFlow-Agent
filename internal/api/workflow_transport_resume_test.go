package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestWorkflowActionsRecoverOnlySavedTransientProviderFailures(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	if err := server.runtime.WorkflowRunner().SaveWorkflowGraphDocument("transport-resume", agent.WorkflowGraphDocument{Name: "transport-resume", Stages: []agent.WorkflowGraphStageDocument{{Name: "report", Agent: "planner", Skill: "execution-plan"}}}); err != nil {
		t.Fatal(err)
	}
	run := session.WorkflowRunSnapshot{ID: "wf-transport", Name: "transport-resume", Status: "action_failed", Summary: "llm chat: TLS handshake timeout", CompletedStages: []session.WorkflowRunStageSnapshot{{Stage: "report", Status: "incomplete", Result: schema.AgentResult{Incomplete: true, Output: "已保存的报告草稿"}}}}
	findContinue := func() bool {
		for _, action := range server.workflowRunActions(run) {
			if action.Name == "continue_output" && action.Available && action.Durable && action.Background {
				return true
			}
		}
		return false
	}
	if !findContinue() {
		t.Fatal("saved transient provider failure has no durable continuation action")
	}
	run.Summary = "llm chat: provider returned 401 Unauthorized"
	if findContinue() {
		t.Fatal("credentials error exposed a transport continuation action")
	}
	run.Summary, run.CompletedStages = "llm chat: TLS handshake timeout", nil
	if findContinue() {
		t.Fatal("missing saved context exposed a continuation action")
	}
}

func TestWorkflowTransportRecoveryEndpointContinuesTheSameRun(t *testing.T) {
	server, state, model, _ := newIntegrityTestServer(t)
	if err := server.runtime.WorkflowRunner().SaveWorkflowGraphDocument("transport-resume", agent.WorkflowGraphDocument{Name: "transport-resume", Stages: []agent.WorkflowGraphStageDocument{{Name: "report", Agent: "planner", Skill: "execution-plan"}}}); err != nil {
		t.Fatal(err)
	}
	id := state.StartWorkflowRun("transport-resume", "完成原报告")
	state.CompleteWorkflowRun(id, "action_failed", "llm chat: TLS handshake timeout", "", "", nil, []session.WorkflowRunStageSnapshot{{Stage: "report", Status: "incomplete", Result: schema.AgentResult{Incomplete: true, Output: "保存的草稿"}}})
	response := kitRoutingRequest(t, server, http.MethodPost, "/api/workflow-runs/"+id+"/continue-output", map[string]bool{"background": true})
	if response.Code != http.StatusAccepted {
		t.Fatalf("recovery endpoint rejected saved context: %d %s", response.Code, response.Body.String())
	}
	select {
	case <-model.started:
	case <-time.After(3 * time.Second):
		t.Fatal("continued model request did not start")
	}
	close(model.release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, _ := state.WorkflowRun(id)
		if run.Status == "completed" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, _ := state.WorkflowRun(id)
	t.Fatalf("same run did not complete after recovery: %s", run.Status)
}
