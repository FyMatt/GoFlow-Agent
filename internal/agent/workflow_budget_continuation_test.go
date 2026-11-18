package agent

import (
	"context"
	"os"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"gopkg.in/yaml.v3"
)

func loadComplexDeliveryGraph(t *testing.T) workflowGraph {
	t.Helper()
	data, err := os.ReadFile("templates/workflows/complex-project-delivery.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var resource struct {
		Graph workflowGraph `yaml:"graph"`
	}
	if err := yaml.Unmarshal(data, &resource); err != nil {
		t.Fatal(err)
	}
	return resource.Graph
}

func TestComplexProjectDeliveryDeclaresBoundedProviderBudget(t *testing.T) {
	graph := loadComplexDeliveryGraph(t)
	budget := graph.Budget
	if budget.SoftLLMCalls <= 0 || budget.HardLLMCalls <= 0 || budget.SoftLLMCalls > budget.HardLLMCalls {
		t.Fatalf("complex delivery must declare a bounded model-call budget: %#v", budget)
	}
	if budget.SoftContinuations <= 0 || budget.HardContinuations <= 0 || budget.SoftContinuations > budget.HardContinuations {
		t.Fatalf("complex delivery must bound incomplete-stage continuations: %#v", budget)
	}
}

// An incomplete stage is re-entered with a fresh iteration budget, so the
// continuation path must honour the workflow budget instead of silently
// multiplying the provider calls of one stage.
func TestIncompleteStageContinuationStopsAtTheWorkflowBudget(t *testing.T) {
	graph := loadComplexDeliveryGraph(t)
	state := session.New(8)
	runID := state.StartWorkflowRun("complex-project-delivery", "keep retrying a stage")
	runner := NewWorkflowRunner(&Runtime{session: state})
	runner.runID = runID

	index := workflowGraphStageIndexByName(graph, "implement")
	if index < 0 {
		t.Fatal("expected an implement stage in the complex delivery graph")
	}
	stage := graph.Stages[index]
	for calls := 0; calls <= graph.Budget.HardLLMCalls; calls++ {
		state.AppendWorkflowRunEvent(runID, session.WorkflowRunEventSnapshot{Type: "token_usage", PromptTokens: 100, OutputTokens: 10})
	}

	_, pending, err := runner.continueWorkflowGraphExecutableStage(
		context.Background(), graph, index, stage, "finish the delivery", nil,
		WorkflowStageResult{Stage: "implement", Status: "running"}, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("continuation must pause on the budget instead of failing: %v", err)
	}
	if pending == nil {
		t.Fatal("continuation over the hard budget must not start another provider run")
	}
	if pending.Status != "awaiting_budget_approval" {
		t.Fatalf("expected a hard-budget pause, got %#v", pending)
	}
}

func TestIncompleteStageContinuationRunsBelowTheWorkflowBudget(t *testing.T) {
	graph := loadComplexDeliveryGraph(t)
	state := session.New(8)
	runID := state.StartWorkflowRun("complex-project-delivery", "continue once below budget")
	runner := NewWorkflowRunner(&Runtime{session: state})
	runner.runID = runID
	if hit := runner.workflowGraphHardBudgetLimitHit(graph, graph.Stages[0], 1); hit.Hit {
		t.Fatalf("a fresh run must be below every hard budget: %#v", hit)
	}
}
