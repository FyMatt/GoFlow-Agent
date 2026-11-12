package agent

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestWorkflowBudgetDoesNotInheritOtherRuns(t *testing.T) {
	state := session.New(8)
	for i := 0; i < 60; i++ {
		state.SetPromptBudget(schema.PromptBudget{EstimatedPromptTokens: 10000})
		state.AddTokenUsage(schema.TokenUsageSample{PromptTokens: 10000, OutputTokens: 1000})
	}
	id := state.StartWorkflowRun("scoped-budget", "new task")
	runner := NewWorkflowRunner(&Runtime{session: state})
	runner.runID = id
	if usage := runner.workflowGraphBudgetUsage(); usage.LLMCalls != 0 || usage.PromptTokens != 0 {
		t.Fatalf("new workflow inherited previous session usage: %#v", usage)
	}
	state.AppendWorkflowRunEvent(id, session.WorkflowRunEventSnapshot{Type: "prompt_budget", PromptBudget: &schema.PromptBudget{EstimatedPromptTokens: 80}})
	state.AppendWorkflowRunEvent(id, session.WorkflowRunEventSnapshot{Type: "token_usage", PromptTokens: 100, OutputTokens: 20, CachedTokens: 70})
	if usage := runner.workflowGraphBudgetUsage(); usage.LLMCalls != 1 || usage.ReportedPromptTokens != 100 || usage.OutputTokens != 20 || usage.CachedTokens != 70 || usage.TotalTokens != 120 {
		t.Fatalf("workflow budget must contain only this run's usage: %#v", usage)
	}
}
