package api

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestBlockedDeliveryDoesNotClaimItIsStillRunning(t *testing.T) {
	allowed, reason := stagedChangesDeliveryAllowed(runDeliveryManifest{Status: "blocked", Conclusion: "waiting_input"})
	if allowed || !strings.Contains(reason, "质量门禁未通过") || strings.Contains(reason, "仍在执行") {
		t.Fatalf("quality-blocked delivery needs a precise explanation, allowed=%v reason=%s", allowed, reason)
	}
}

func TestDeliveryMetricsPreferDurableUsageOverBudgetAndReplay(t *testing.T) {
	records := []session.UsageRecord{
		{At: "2026-10-22T01:00:00Z", PromptTokens: 100, OutputTokens: 20, CachedTokens: 80},
		{At: "2026-10-22T01:01:00Z", PromptTokens: 200, OutputTokens: 30, CachedTokens: 160},
	}
	workflow := session.WorkflowRunSnapshot{BudgetLLMCalls: 60, BudgetReportedPromptTokens: 9999, UsageRecords: records}
	agent := session.AgentRunSnapshot{UsageRecords: records}
	for name, metrics := range map[string]runDeliveryMetrics{
		"workflow": workflowRunDeliveryMetrics(workflow, nil, nil),
		"agent":    agentRunDeliveryMetrics(agent, nil, nil),
	} {
		if metrics.LLMCalls != 2 || metrics.PromptTokens != 300 || metrics.OutputTokens != 50 || metrics.CachedTokens != 240 || metrics.TotalTokens != 350 {
			t.Errorf("%s delivery disagrees with the durable ledger: %#v", name, metrics)
		}
	}
}

func TestWorkbenchOutcomeDeduplicatesChangedFiles(t *testing.T) {
	outcome := summarizeWorkbenchOutcome(runDeliveryManifest{Changes: []runDiffItem{
		{Path: "static/style.css", Status: "modified"}, {Path: "static/style.css", Status: "modified"}, {Path: "app.py", Status: "modified"},
	}})
	if outcome.ChangeCount != 2 || len(outcome.ChangedFiles) != 2 {
		t.Fatalf("each changed file must appear once: %#v", outcome)
	}
}

func TestDeliveryToolCountsIncludeReadsWithoutArtifacts(t *testing.T) {
	result := schema.AgentResult{ToolResults: []schema.ToolResult{
		{CallID: "read-1", ToolName: "read_file"},
		{CallID: "write-1", ToolName: "write_file"},
		{CallID: "test-1", ToolName: "run_command"},
	}}
	artifacts := []runArtifactItem{{ToolCallID: "write-1"}, {ToolCallID: "test-1"}}
	agentRun := session.AgentRunSnapshot{Result: &result, Events: []session.AgentRunEventSnapshot{{ToolCallID: "test-1"}}}
	workflowRun := session.WorkflowRunSnapshot{
		CompletedStages: []session.WorkflowRunStageSnapshot{{Stage: "implement", Result: result}},
		Events:          []session.WorkflowRunEventSnapshot{{ToolCallID: "test-1"}},
	}
	for name, metrics := range map[string]runDeliveryMetrics{
		"agent":    agentRunDeliveryMetrics(agentRun, artifacts, nil),
		"workflow": workflowRunDeliveryMetrics(workflowRun, artifacts, nil),
	} {
		if metrics.ToolCalls != 3 {
			t.Errorf("%s must count each call once, including reads without artifacts: %#v", name, metrics)
		}
	}
}

func TestDeliveryToolCountsRetainDurableOperationsWhenEventsExpire(t *testing.T) {
	run := session.WorkflowRunSnapshot{
		CompletedStages: []session.WorkflowRunStageSnapshot{{Result: schema.AgentResult{ToolResults: []schema.ToolResult{{CallID: "written"}}}}},
		ToolOperations: []session.WorkflowToolOperationSnapshot{
			{Result: schema.ToolResult{CallID: "written"}},
			{Result: schema.ToolResult{CallID: "old-read"}},
			{Result: schema.ToolResult{CallID: "live-check"}},
		},
		Events: []session.WorkflowRunEventSnapshot{{ToolCallID: "live-check"}},
	}
	if metrics := workflowRunDeliveryMetrics(run, nil, nil); metrics.ToolCalls != 3 {
		t.Fatalf("durable calls must survive the retained-event window and be deduplicated: %#v", metrics)
	}
}

func TestEffectiveWorkflowStatusUsesLatestQualityGate(t *testing.T) {
	failed := session.WorkflowRunStageSnapshot{Stage: "quality", NodeType: "quality_gate", Outputs: map[string]string{"quality_status": "failed"}}
	passed := session.WorkflowRunStageSnapshot{Stage: "final-quality", NodeType: "quality_gate", Outputs: map[string]string{"quality_status": "passed"}}
	for _, tc := range []struct {
		name   string
		stages []session.WorkflowRunStageSnapshot
		want   string
	}{
		{"unresolved gate", []session.WorkflowRunStageSnapshot{failed}, "blocked"},
		{"recovered gate", []session.WorkflowRunStageSnapshot{failed, passed}, "completed"},
		{"recovered earlier decision", []session.WorkflowRunStageSnapshot{{Outputs: map[string]string{"decision": "failed"}}, failed, passed}, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveWorkflowRunStatus(session.WorkflowRunSnapshot{Status: "completed", CompletedStages: tc.stages}); got != tc.want {
				t.Fatalf("status=%s, want %s", got, tc.want)
			}
		})
	}
}

func TestPythonStandardLibraryCommandsCountAsVerification(t *testing.T) {
	for _, content := range []string{
		`{"program":".venv/Scripts/python.exe","args":["-m","unittest","discover","-s","tests"],"exit_code":0}`,
		`{"program":".venv/Scripts/python.exe","args":["-m","py_compile","app.py"],"exit_code":0}`,
	} {
		if !runDeliveryArtifactIsVerification(runArtifactItem{ToolName: "run_command", Content: content}) {
			t.Fatalf("successful project verification was ignored: %s", content)
		}
	}
}
