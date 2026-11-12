package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/llm"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestProviderTransportRetriesDoNotMultiplyWorkflowStageRetries(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) <= 3 {
			http.Error(w, "provider returned 503 Service Unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered report\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer provider.Close()
	root := t.TempDir()
	writeWorkflowGraph(t, root, "transport-overload", `name: transport-overload
stages:
  - name: report
    agent: planner
    skill: execution-plan
    retry:
      max_attempts: 3
`)
	client := llm.NewClient(config.LLMConfig{BaseURL: provider.URL, APIKey: "test-key", WireAPI: "responses", RetryCount: 2, RetryBackoff: time.Millisecond})
	runtimeRef := newWorkflowGraphRuntime(t, root, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"planner": client})
	paused, err := runtimeRef.WorkflowRunner().Run(context.Background(), "transport-overload", "完成报告", true, nil)
	if err != nil || paused.Status != "paused_need_more_budget" || requests.Load() != 3 {
		t.Fatalf("transport retries must pause before a second stage retry cycle: result=%#v requests=%d err=%v", paused, requests.Load(), err)
	}
	finished, err := runtimeRef.WorkflowRunner().ContinueOutput(context.Background(), paused.RunID, nil)
	if err != nil || finished.Status != "completed" || finished.RunID != paused.RunID || requests.Load() != 4 {
		t.Fatalf("user continuation must renew the request in the same run: result=%#v requests=%d err=%v", finished, requests.Load(), err)
	}
}

func TestTruncatedResponsesReportPausesForSameRunContinuation(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"人工\"}\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"完整验收报告\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer provider.Close()
	root := t.TempDir()
	writeWorkflowGraph(t, root, "truncated-report", `name: truncated-report
stages:
  - name: report
    agent: planner
    skill: execution-plan
`)
	client := llm.NewClient(config.LLMConfig{BaseURL: provider.URL, APIKey: "test-key", WireAPI: "responses", RetryCount: 2, RetryBackoff: time.Millisecond})
	runtimeRef := newWorkflowGraphRuntime(t, root, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"planner": client})
	paused, err := runtimeRef.WorkflowRunner().Run(context.Background(), "truncated-report", "生成完整验收报告", true, nil)
	if err != nil || paused.Status != "paused_need_more_budget" || requests.Load() != 1 {
		t.Fatalf("truncated response must pause without automatic replay: result=%#v requests=%d err=%v", paused, requests.Load(), err)
	}
	finished, err := runtimeRef.WorkflowRunner().ContinueOutput(context.Background(), paused.RunID, nil)
	if err != nil || finished.Status != "completed" || finished.RunID != paused.RunID || requests.Load() != 2 {
		t.Fatalf("user continuation must finish the same run: result=%#v requests=%d err=%v", finished, requests.Load(), err)
	}
}

func TestReportOnlyProviderOverloadCanContinueWithoutReplayingCompletedStage(t *testing.T) {
	root := t.TempDir()
	writeWorkflowGraph(t, root, "report-overload", `name: report-overload
stages:
  - name: plan
    agent: planner
    skill: execution-plan
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
    acceptance_criteria:
      - name: has-report
        ref: result.output
        contains: report
    artifacts:
      - name: final-report
        kind: report
        ref: result.output
`)
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{response: schema.ChatResponse{Message: schema.Message{Content: "plan finished"}}},
		{err: errors.New("temporary model failure: responses status failed: Our servers are currently overloaded. Please try again later.")},
		{response: schema.ChatResponse{Message: schema.Message{Content: "final report finished"}}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, root, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"planner": llm})
	persistPath := filepath.Join(root, "session.json")
	runtimeRef.session.SetPersistPath(persistPath)
	paused, err := runtimeRef.WorkflowRunner().Run(context.Background(), "report-overload", "完成已有任务的交付报告", true, nil)
	if err != nil || paused.Status != "paused_need_more_budget" || paused.NextStage != "report" || len(paused.CompletedStages) != 2 {
		t.Fatalf("report overload must save prior plan and pause: result=%#v err=%v", paused, err)
	}
	if len(paused.CompletedStages[1].Acceptance) != 0 || len(paused.CompletedStages[1].Output.Artifacts) != 0 {
		t.Fatalf("interrupted report must not emit completion contracts or delivery artifacts: %#v", paused.CompletedStages[1])
	}
	reloaded := session.New(8)
	if err := reloaded.Load(persistPath); err != nil {
		t.Fatal(err)
	}
	runtimeRef.session = reloaded
	finished, err := runtimeRef.WorkflowRunner().ContinueOutput(context.Background(), paused.RunID, nil)
	if err != nil || finished.Status != "completed" || finished.RunID != paused.RunID || len(finished.CompletedStages) != 2 || finished.CompletedStages[0].Result.Output != "plan finished" || finished.CompletedStages[1].Result.Output != "final report finished" {
		t.Fatalf("continuation must only complete the failed report: result=%#v err=%v", finished, err)
	}
	if len(llm.requests) != 3 {
		t.Fatalf("prior plan was replayed: requests=%d", len(llm.requests))
	}
	if len(finished.CompletedStages[1].Acceptance) != 1 || finished.CompletedStages[1].Acceptance[0].Status != "passed" || len(finished.CompletedStages[1].Output.Artifacts) == 0 {
		t.Fatalf("completed report must evaluate real contracts and emit artifacts: %#v", finished.CompletedStages[1])
	}
}

func TestIncompleteStageDoesNotRunCompletionEnforcementOrRetry(t *testing.T) {
	for _, name := range []string{"plan", "implement"} {
		t.Run(name, func(t *testing.T) {
			llm := &scriptedLLMClient{calls: []scriptedLLMCall{{err: errors.New("temporary model failure: responses status failed: overloaded")}}}
			runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"planner": llm})
			prior := []schema.ToolResult{{CallID: "prior-read", ToolName: "read_file", Content: "saved file evidence"}}
			stage := workflowGraphStage{Name: name, Agent: "planner", Skill: "execution-plan", Retry: workflowGraphRetry{MaxAttempts: 3}}
			result, attempts, err := runtimeRef.WorkflowRunner().runWorkflowGraphExecutableStageWithInitialResults(context.Background(), workflowGraph{Name: "plan-implement-audit"}, stage, "完成页面美化", workflowGraphTestSkills()[0], prior, nil)
			if err != nil || !result.Incomplete || attempts != 1 || len(llm.requests) != 1 || len(result.ToolResults) != 1 {
				t.Fatalf("incomplete stages must pause without completion checks or replay: result=%#v attempts=%d requests=%d err=%v", result, attempts, len(llm.requests), err)
			}
			if strings.Contains(result.Output, "goflow-change-slices") {
				t.Fatalf("interrupted stage must not fabricate a completed plan: %s", result.Output)
			}
		})
	}
}

func TestExactPatchFailureGetsOnlyConfiguredStageRetry(t *testing.T) {
	llm := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "未完成，补丁没有匹配。"}},
		{Message: schema.Message{Content: "仍然无法匹配，保留原文件并报告阻塞。"}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"fixer": llm})
	prior := []schema.ToolResult{{ToolName: "apply_patch", IsError: true, Content: "edit 1 expected 1 occurrence(s) but found 0; no changes were written"}}
	stage := workflowGraphStage{Name: "implement", Agent: "fixer", Params: map[string]string{"require_workspace_mutation": "true"}, Retry: workflowGraphRetry{MaxAttempts: 2}}
	_, attempts, err := runtimeRef.WorkflowRunner().runWorkflowGraphExecutableStageWithInitialResults(context.Background(), workflowGraph{Name: "plan-implement-audit"}, stage, "修改样式", workflowGraphTestSkills()[0], prior, nil)
	if err == nil || !strings.Contains(err.Error(), "unapplied exact patch") || attempts != 2 || len(llm.requests) != 2 {
		t.Fatalf("exact patch repair must use only its configured two attempts: attempts=%d requests=%d err=%v", attempts, len(llm.requests), err)
	}
	last := llm.requests[1].Messages[len(llm.requests[1].Messages)-1].Content
	if !strings.Contains(last, "exact unique anchor") {
		t.Fatalf("retry must explain how to recover the failed patch: %s", last)
	}
	if workflowGraphHasUnappliedExactPatch(schema.AgentResult{ToolResults: append(prior, schema.ToolResult{ToolName: "write_file", Content: `{"relative_path":"static/style.css"}`})}) {
		t.Fatal("a later successful write must not be classified as zero-write failure")
	}
}

func TestFinalizeWithProviderInterruptionPausesAndPreservesEvidence(t *testing.T) {
	runner := &AgentRunner{id: "fixer", profile: config.AgentProfile{Mode: "fix", Model: "test-model"}}
	events := make([]schema.StreamEvent, 0)
	result, err := runner.finalizeWithProviderInterruption(context.Background(), agentConversationState{
		Profile: config.AgentProfile{Mode: "fix", Model: "test-model"},
		Mode:    "fix",
		Input:   "运行项目测试",
	}, []schema.Message{{Role: "user", Content: "运行项目测试"}}, []schema.ToolResult{{CallID: "call-1", ToolName: "run_command", Content: "tests passed"}}, false, 2, context.DeadlineExceeded, func(event schema.StreamEvent) error {
		events = append(events, event)
		return nil
	}, nil, nil)
	if err != nil {
		t.Fatalf("finalizeWithProviderInterruption: %v", err)
	}
	if !result.Incomplete || result.StopReason != schema.StopReasonError || len(result.ToolResults) != 1 {
		t.Fatalf("expected retryable incomplete result with preserved tool evidence, got %#v", result)
	}
	if !strings.Contains(result.Output, "安全暂停") || !strings.Contains(result.Output, "继续执行") {
		t.Fatalf("expected actionable Chinese recovery summary, got %q", result.Output)
	}
	if len(events) != 2 || events[0].Reason != "provider_interruption" || !events[0].NeedsAction {
		t.Fatalf("expected actionable provider interruption events, got %#v", events)
	}
}

func TestFinalizeWithProviderInterruptionKeepsOriginalUserLanguageOnContinue(t *testing.T) {
	runner := &AgentRunner{id: "fixer", profile: config.AgentProfile{Mode: "fix", Model: "test-model"}}
	result, err := runner.finalizeWithProviderInterruption(context.Background(), agentConversationState{
		Profile: runner.profile,
		Mode:    "fix",
		Input:   "Continue the saved task without replaying completed tools.",
	}, []schema.Message{
		{Role: "user", Content: "帮我美化这个网页"},
		{Role: "assistant", Content: "I will inspect the relevant files."},
	}, []schema.ToolResult{{CallID: "call-1", ToolName: "read_file", Content: "saved file content"}}, false, 2, context.DeadlineExceeded, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "安全暂停") || strings.Contains(result.Output, "The model response") {
		t.Fatalf("continuation interruption must retain the original Chinese user language, got %q", result.Output)
	}
}

func TestProviderInterruptionPersistsOrdinaryContinuationContext(t *testing.T) {
	state := session.New(4)
	runID := state.StartAgentRun("继续完成页面修改")
	runtimeRef := &Runtime{session: state}
	runner := &AgentRunner{id: "fixer", profile: config.AgentProfile{Mode: "fix", Model: "test-model"}}
	ctx := WithAgentRunID(context.Background(), runID)
	_, err := runner.finalizeWithProviderInterruption(ctx, agentConversationState{
		Profile:      runner.profile,
		Mode:         "fix",
		SystemPrompt: "stable system prompt",
		Input:        "继续完成页面修改",
	}, []schema.Message{{Role: "user", Content: "继续完成页面修改"}, {Role: "tool", Name: "read_file", ToolCallID: "call-read", Content: "saved file content"}}, []schema.ToolResult{{CallID: "call-read", ToolName: "read_file", Content: "saved file content"}}, false, 2, context.DeadlineExceeded, nil, nil, runtimeRef)
	if err != nil {
		t.Fatal(err)
	}
	run, ok := state.AgentRun(runID)
	if !ok || run.ResumeContext == nil || len(run.ResumeContext.Messages) != 2 || len(run.ResumeContext.CollectedResults) != 1 {
		t.Fatalf("provider interruption continuation was not persisted: %#v", run)
	}
}

func TestIterationBudgetPersistsOrdinaryContinuationContext(t *testing.T) {
	state := session.New(4)
	runID := state.StartAgentRun("完成剩余实现和测试")
	runtimeRef := &Runtime{session: state}
	runner := &AgentRunner{
		id:      "fixer",
		profile: config.AgentProfile{Mode: "fix", Model: "test-model", MaxTokens: 256},
		llm: &stubLLMClient{responses: []schema.ChatResponse{{
			Message:    schema.Message{Role: "assistant", Content: "当前实现尚未完成。"},
			StopReason: schema.StopReasonStop,
		}}},
	}
	ctx := WithAgentRunID(context.Background(), runID)
	_, err := runner.finalizeAfterIterationBudget(ctx, agentConversationState{
		Profile:      runner.profile,
		Mode:         "fix",
		SystemPrompt: "stable system prompt",
		Input:        "完成剩余实现和测试",
	}, []schema.Message{{Role: "user", Content: "完成剩余实现和测试"}, {Role: "tool", Name: "write_file", ToolCallID: "call-write", Content: "saved change"}}, []schema.ToolResult{{CallID: "call-write", ToolName: "write_file", Content: "saved change"}}, false, 18, nil, nil, runtimeRef)
	if err != nil {
		t.Fatal(err)
	}
	run, ok := state.AgentRun(runID)
	if !ok || run.ResumeContext == nil || len(run.ResumeContext.Messages) != 2 || len(run.ResumeContext.CollectedResults) != 1 {
		t.Fatalf("iteration-budget continuation was not persisted: %#v", run)
	}
}

func TestTransientModelInterruptionAfterResultsDoesNotMaskCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if transientModelInterruptionAfterResults(ctx, context.DeadlineExceeded, []schema.ToolResult{{ToolName: "read_file"}}) {
		t.Fatal("caller cancellation must remain a cancellation")
	}
	if transientModelInterruptionAfterResults(context.Background(), errors.New("provider returned 400 Bad Request"), []schema.ToolResult{{ToolName: "read_file"}}) {
		t.Fatal("non-transient provider errors must remain failures")
	}
}

func TestContinueOutputPromptReusesProviderInterruptionEvidence(t *testing.T) {
	prompt := workflowGraphContinueOutputPrompt("base prompt", WorkflowStageResult{Result: schema.AgentResult{
		Output:           "模型响应在工具执行后超时，本阶段已安全暂停。",
		Incomplete:       true,
		IncompleteReason: "provider response was interrupted after tool results were collected: context deadline exceeded",
		ToolResults:      []schema.ToolResult{{CallID: "call-test", ToolName: "run_command", Content: "tests passed"}},
	}})
	if !strings.Contains(prompt, "Do not rerun completed tool calls") || !strings.Contains(prompt, "run_command") || !strings.Contains(prompt, "tests passed") {
		t.Fatalf("continuation prompt must preserve completed tool evidence, got %q", prompt)
	}
}

func TestContinueOutputPromptReusesIterationBudgetEvidenceAndRenewsTools(t *testing.T) {
	prompt := workflowGraphContinueOutputPrompt("base prompt", WorkflowStageResult{Result: schema.AgentResult{
		Output:           "已修改样式并启动服务，尚需完成浏览器验证。",
		Incomplete:       true,
		IncompleteReason: "tool iteration budget reached before the task could safely continue",
		StopReason:       schema.StopReasonIterationBudget,
		ToolResults: []schema.ToolResult{
			{CallID: "call-patch", ToolName: "apply_patch", Content: `{"relative_path":"static/style.css","status":"modified"}`},
			{CallID: "call-test", ToolName: "run_command", Content: `{"exit_code":0,"stdout":"tests passed"}`},
		},
	}})
	for _, expected := range []string{
		"new bounded tool-call budget is available",
		"Do not rerun completed tool calls",
		"previous tool-budget segment",
		"apply_patch",
		"run_command",
		"tests passed",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("iteration-budget continuation prompt is missing %q: %q", expected, prompt)
		}
	}
}

func TestMergeWorkflowGraphContinuationToolResultsPreservesPriorEvidence(t *testing.T) {
	previous := []schema.ToolResult{
		{CallID: "patch", ToolName: "apply_patch", Content: "changed"},
		{CallID: "test", ToolName: "run_command", Content: "old result"},
	}
	current := []schema.ToolResult{
		{CallID: "test", ToolName: "run_command", Content: "new result"},
		{CallID: "browser", ToolName: "browser_snapshot", Content: "visible"},
	}
	merged := mergeWorkflowGraphContinuationToolResults(previous, current)
	if len(merged) != 3 {
		t.Fatalf("expected accumulated evidence without duplicate call IDs, got %#v", merged)
	}
	if merged[0].CallID != "patch" || merged[1].Content != "new result" || merged[2].CallID != "browser" {
		t.Fatalf("unexpected continuation evidence order or replacement: %#v", merged)
	}
}

func TestWorkflowContinuationTreatsPriorEvidenceAsRecoverableProviderProgress(t *testing.T) {
	interruption := errors.New("read Responses stream: context deadline exceeded (Client.Timeout while reading body)")
	llm := &scriptedLLMClient{calls: []scriptedLLMCall{
		{err: interruption},
		{err: interruption},
		{err: interruption},
	}}
	mcp := &stubRuntimeMCP{}
	state := session.New(4)
	runner := &AgentRunner{id: "planner", profile: config.AgentProfile{
		Name:          "Planner",
		Mode:          "plan",
		Model:         "test-model",
		MaxIterations: 3,
	}, llm: llm, executor: NewExecutor(mcp)}
	prior := schema.ToolResult{CallID: "call-read", ToolName: "read_file", Content: "preserved repository evidence"}
	result, err := runner.runStreamWithOptions(context.Background(), "continue the saved plan", stubSkillManager{}, mcp, state, runtime.NewAuditLogger(false, false), &Runtime{session: state}, agentRunOptions{
		InitialToolResults:   []schema.ToolResult{prior},
		IsolatePromptContext: true,
	}, nil)
	if err != nil {
		t.Fatalf("provider interruption after a user continuation must pause again instead of failing the run: %v", err)
	}
	if !result.Incomplete || result.StopReason != schema.StopReasonError || len(result.ToolResults) != 1 || result.ToolResults[0].CallID != prior.CallID {
		t.Fatalf("expected resumable interruption with prior evidence preserved, got %#v", result)
	}
}

func TestDuplicateToolReplayNudgeRequiresForwardProgress(t *testing.T) {
	prompt := duplicateToolReplayNudge(3)
	if !strings.Contains(prompt, "3 repeated") || !strings.Contains(prompt, "Do not request") || !strings.Contains(prompt, "next missing implementation") {
		t.Fatalf("duplicate replay nudge is not actionable: %q", prompt)
	}
}
