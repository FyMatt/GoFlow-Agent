package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

func TestWorkflowGraphStageRequirementsUseRuntimeToolSelection(t *testing.T) {
	runner := NewWorkflowRunner(nil)
	doc := WorkflowGraphDocument{Stages: []WorkflowGraphStageDocument{{
		Name:     "implement",
		Agent:    "fixer",
		Skill:    "code-writing",
		Tool:     "file_tools/read_file",
		Params:   map[string]string{"allowed_tools": "file_tools/apply_patch, command_tools/run_command"},
		NodeType: "task",
	}}}
	requirements := runner.WorkflowGraphStageRequirements(doc)
	if len(requirements) != 1 || !requirements[0].Executable || len(requirements[0].Tools) != 3 {
		t.Fatalf("unexpected stage requirements: %#v", requirements)
	}
}

func TestWorkflowGraphStageRequirementsCanDisableAllTools(t *testing.T) {
	runner := NewWorkflowRunner(nil)
	doc := WorkflowGraphDocument{Stages: []WorkflowGraphStageDocument{{
		Name:     "review",
		Agent:    "auditor",
		Skill:    "code-audit",
		NodeType: "skill",
		Params: map[string]string{
			"disable_tools": "true",
			"tools":         "file_tools/read_file,command_tools/run_command",
		},
	}}}
	requirements := runner.WorkflowGraphStageRequirements(doc)
	if len(requirements) != 1 || !requirements[0].Executable || !requirements[0].DisableTools || len(requirements[0].Tools) != 0 {
		t.Fatalf("unexpected no-tool stage requirements: %#v", requirements)
	}
	allowed := workflowGraphStageAllowedTools(workflowGraphStage{Params: doc.Stages[0].Params})
	if len(allowed) != 1 || allowed[0] != "__none__" {
		t.Fatalf("expected runtime no-tool sentinel, got %#v", allowed)
	}
}

func TestValidateWorkflowGraphDocumentRejectsIndirectSubWorkflowCycle(t *testing.T) {
	runtimeHome := t.TempDir()
	runner := NewWorkflowRunner(&Runtime{cfg: &config.Config{RuntimeHome: runtimeHome}})
	writeWorkflowGraph(t, runtimeHome, "child-flow", `
name: child-flow
stages:
  - name: parent
    node_type: sub_workflow
    params:
      workflow: parent-flow
`)
	doc := WorkflowGraphDocument{Name: "parent-flow", Stages: []WorkflowGraphStageDocument{{
		Name:     "child",
		NodeType: "sub_workflow",
		Params:   map[string]string{"workflow": "child-flow"},
	}}}
	result := runner.ValidateWorkflowGraphDocument(doc.Name, doc)
	if result.Valid || !workflowPreflightMessageContains(result.Issues, "indirect sub-workflow cycle: parent-flow -> child-flow -> parent-flow") {
		t.Fatalf("expected indirect cycle diagnostic, got %#v", result)
	}
	if err := runner.SaveWorkflowGraphDocument(doc.Name, doc); err == nil || !strings.Contains(err.Error(), "indirect sub-workflow cycle") {
		t.Fatalf("expected save to reject indirect cycle, got %v", err)
	}
}

func TestValidateWorkflowGraphDocumentRejectsSubWorkflowDepthOverEight(t *testing.T) {
	runtimeHome := t.TempDir()
	runner := NewWorkflowRunner(&Runtime{cfg: &config.Config{RuntimeHome: runtimeHome}})
	for index := 2; index <= 8; index++ {
		writeWorkflowGraph(t, runtimeHome, fmt.Sprintf("flow-%d", index), fmt.Sprintf(`
name: flow-%d
stages:
  - name: next
    node_type: sub_workflow
    params:
      workflow: flow-%d
`, index, index+1))
	}
	doc := WorkflowGraphDocument{Name: "flow-1", Stages: []WorkflowGraphStageDocument{{
		Name:     "next",
		NodeType: "sub_workflow",
		Params:   map[string]string{"workflow": "flow-2"},
	}}}
	result := runner.ValidateWorkflowGraphDocument(doc.Name, doc)
	if result.Valid || !workflowPreflightMessageContains(result.Issues, "depth exceeds maximum 8") {
		t.Fatalf("expected maximum depth diagnostic, got %#v", result)
	}
}

func TestWorkflowContextCallPathGuardsCycleAndDepthAtRuntime(t *testing.T) {
	ctx, err := workflowContextWithCallPath(context.Background(), "parent")
	if err != nil {
		t.Fatalf("add root workflow: %v", err)
	}
	ctx, err = workflowContextWithCallPath(ctx, "child")
	if err != nil {
		t.Fatalf("add child workflow: %v", err)
	}
	if _, err := workflowContextWithCallPath(ctx, "parent"); err == nil || !strings.Contains(err.Error(), "parent -> child -> parent") {
		t.Fatalf("expected runtime cycle guard, got %v", err)
	}

	ctx = context.Background()
	for index := 1; index <= maxWorkflowSubWorkflowDepth; index++ {
		ctx, err = workflowContextWithCallPath(ctx, fmt.Sprintf("flow-%d", index))
		if err != nil {
			t.Fatalf("add workflow depth %d: %v", index, err)
		}
	}
	if _, err := workflowContextWithCallPath(ctx, "flow-9"); err == nil || !strings.Contains(err.Error(), "depth exceeds maximum 8") {
		t.Fatalf("expected runtime depth guard, got %v", err)
	}
}

func TestValidateWorkflowGraphStartupChecksLiveReadinessAndInputSchema(t *testing.T) {
	runner := NewWorkflowRunner(nil)
	doc := WorkflowGraphDocument{Name: "preflight", Stages: []WorkflowGraphStageDocument{
		{
			Name:  "change",
			Agent: "fixer",
			Skill: "code-writing",
			Execution: WorkflowGraphExecutionContractDocument{
				Mode:             "live",
				RequiresRollback: true,
			},
		},
		{
			Name:     "input",
			NodeType: "input_gate",
			Params:   map[string]string{"fields_json": "{"},
		},
	}}
	issues := runner.ValidateWorkflowGraphStartup(doc.Name, doc, false)
	if !workflowPreflightFieldExists(issues, "params.input_fields") && !workflowPreflightFieldExists(issues, "params.fields_json") {
		t.Fatalf("expected invalid input schema issue, got %#v", issues)
	}
	if !workflowPreflightMessageContains(issues, "missing execution readiness") {
		t.Fatalf("expected live readiness issue, got %#v", issues)
	}
}

func workflowPreflightFieldExists(issues []WorkflowGraphValidationIssue, field string) bool {
	for _, issue := range issues {
		if issue.Field == field {
			return true
		}
	}
	return false
}

func workflowPreflightMessageContains(issues []WorkflowGraphValidationIssue, fragment string) bool {
	for _, issue := range issues {
		if strings.Contains(issue.Message, fragment) {
			return true
		}
	}
	return false
}
