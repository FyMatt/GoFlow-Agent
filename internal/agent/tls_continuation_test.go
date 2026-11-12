package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type tlsContinuationClient struct {
	workflowRecordingLLMClient
	calls int
}

func TestRestartBeforeFirstProviderOutputContinuesOnlyCurrentStage(t *testing.T) {
	home := t.TempDir()
	writeWorkflowGraphYAML(t, home, "provider-wait", `
name: provider-wait
stages:
  - name: clarify
    agent: planner
    skill: execution-plan
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
`)
	client := &tlsContinuationClient{}
	runtime := newWorkflowGraphRuntime(t, home, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"planner": client})
	id := runtime.session.StartWorkflowRun("provider-wait", "complete the task")
	runtime.session.CompleteWorkflowRun(id, "running", "clarification completed", "report", "", nil,
		[]session.WorkflowRunStageSnapshot{{Stage: "clarify", AgentID: "planner", Skill: "execution-plan", Status: "completed", Result: schema.AgentResult{Output: "saved clarification"}}})
	runtime.session.AppendWorkflowRunEvent(id, session.WorkflowRunEventSnapshot{Type: "status", Stage: "report", Reason: "provider_request_started", AgentID: "planner"})
	if _, err := runtime.session.ReconcileInterruptedWorkflowRuns(); err != nil {
		t.Fatal(err)
	}
	finished, err := runtime.WorkflowRunner().ContinueOutput(t.Context(), id, nil)
	if err != nil || finished.Status != "completed" || finished.RunID != id || client.calls != 1 || len(finished.CompletedStages) != 2 {
		t.Fatalf("restart replayed prior stages or discarded the run: status=%s calls=%d err=%v", finished.Status, client.calls, err)
	}
}

func TestProviderTypedTransportTimeoutIsRetryable(t *testing.T) {
	err := fmt.Errorf("llm chat: %w", &url.Error{Op: "Post", URL: "https://provider.invalid", Err: &net.DNSError{Err: "handshake stalled", IsTimeout: true}})
	if !workflowGraphStageErrorIsRetryable(t.Context(), err) {
		t.Fatal("typed transport timeout was not recoverable")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if workflowGraphStageErrorIsRetryable(ctx, err) {
		t.Fatal("user cancellation was turned into an automatic network retry")
	}
}

func (c *tlsContinuationClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	c.calls++
	if c.calls == 2 || c.calls == 3 {
		return schema.ChatResponse{}, errors.New("llm chat: send Responses stream request: net/http: TLS handshake timeout")
	}
	return schema.ChatResponse{Message: schema.Message{Content: "阶段结果完整"}}, nil
}

func (c *tlsContinuationClient) StreamChat(ctx context.Context, request schema.ChatRequest, _ interfaces.StreamHandler) (schema.ChatResponse, error) {
	return c.Chat(ctx, request)
}

func TestTLSFailureDuringContinuationRemainsResumable(t *testing.T) {
	home := t.TempDir()
	writeWorkflowGraphYAML(t, home, "tls-continuation", `
name: tls-continuation
stages:
  - name: clarify
    agent: planner
    skill: execution-plan
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
`)
	client := &tlsContinuationClient{}
	runtime := newWorkflowGraphRuntime(t, home, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"planner": client})
	first, err := runtime.WorkflowRunner().Run(t.Context(), "tls-continuation", "完成各阶段报告", true, nil)
	if err != nil || first.Status != "paused_need_more_budget" {
		t.Fatalf("TLS outage became terminal: status=%s err=%v", first.Status, err)
	}
	second, err := runtime.WorkflowRunner().ContinueOutput(t.Context(), first.RunID, nil)
	if err != nil || second.Status != "paused_need_more_budget" || second.RunID != first.RunID {
		t.Fatalf("second outage lost continuation: status=%s err=%v", second.Status, err)
	}
	// Old versions saved these transport failures as action_failed and cleared
	// NextStage, while the incomplete stage and its evidence were still durable.
	runtime.session.UpdateWorkflowRunState(session.WorkflowSnapshot{RunID: first.RunID, Name: "tls-continuation", Status: "action_failed", Request: "完成各阶段报告", Summary: "llm chat: TLS handshake timeout"})
	legacy, _ := runtime.session.WorkflowRun(first.RunID)
	if err := runtime.WorkflowRunner().CanContinueOutput(legacy); err != nil {
		t.Fatalf("legacy transport failure with saved context cannot resume: %v", err)
	}
	finished, err := runtime.WorkflowRunner().ContinueOutput(t.Context(), first.RunID, nil)
	if err != nil || finished.Status != "completed" || client.calls != 4 || len(finished.CompletedStages) != 2 {
		t.Fatalf("saved stages were replayed or lost: status=%s calls=%d stages=%d err=%v", finished.Status, client.calls, len(finished.CompletedStages), err)
	}
	legacy.Summary = "llm chat: provider returned 401 Unauthorized"
	if err := runtime.WorkflowRunner().CanContinueOutput(legacy); err == nil {
		t.Fatal("authentication failure was treated as a transient network outage")
	}
}
