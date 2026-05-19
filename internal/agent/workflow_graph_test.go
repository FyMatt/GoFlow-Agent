package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/memory"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestWorkflowRunnerRunsCustomWorkflowGraph(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "release-check", `
name: release-check
description: Validate and improve a change.
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    next_strategy: select
    next: [implement, audit]
  - name: implement
    agent: fixer
    skill: code-writing
  - name: audit
    agent: auditor
    skill: code-audit
`)
	skills := workflowGraphTestSkills()
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan ready.\nNext skill: implement"}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Implementation done."}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Audit done."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, skills, &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "release-check", "ship this feature", true, nil)
	if err != nil {
		t.Fatalf("Run custom workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 3 {
		t.Fatalf("expected completed custom workflow with three stages, got %#v", result)
	}
	expected := []struct {
		stage string
		agent string
	}{
		{"triage", "planner"},
		{"implement", "fixer"},
		{"audit", "auditor"},
	}
	for i, want := range expected {
		if string(result.CompletedStages[i].Stage) != want.stage || result.CompletedStages[i].Agent != want.agent {
			t.Fatalf("unexpected stage %d: got %#v want %#v", i, result.CompletedStages[i], want)
		}
	}
	if !strings.Contains(plannerLLM.requests[0].System, "Matched skill: execution-plan") {
		t.Fatalf("expected planner stage to use execution-plan skill, got %q", plannerLLM.requests[0].System)
	}
	if !strings.Contains(fixerLLM.requests[0].Messages[len(fixerLLM.requests[0].Messages)-1].Content, "Completed prior stages") {
		t.Fatalf("expected implement prompt to include prior stage context, got %#v", fixerLLM.requests[0].Messages)
	}
	if runtimeRef.ActiveAgent() != "chat" {
		t.Fatalf("expected default agent restored after custom workflow, got %q", runtimeRef.ActiveAgent())
	}
}

func TestWorkflowRunnerPersistsAcceptanceCriteria(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "quality-flow", `
name: quality-flow
description: Capture acceptance evidence.
stages:
  - name: audit
    agent: auditor
    skill: code-audit
    acceptance_criteria:
      - name: mentions-risk
        description: Audit should mention risk.
        ref: result.output
        contains: risk
        expected: audit includes risk
      - name: mentions-tests
        ref: result.output
        contains: tests
`)
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "risk reviewed"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "quality-flow", "review quality", true, nil)
	if err != nil {
		t.Fatalf("Run quality workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 1 {
		t.Fatalf("expected completed quality workflow, got %#v", result)
	}
	acceptance := result.CompletedStages[0].Acceptance
	if len(acceptance) != 2 {
		t.Fatalf("expected two acceptance results, got %#v", acceptance)
	}
	if acceptance[0].Status != "passed" || acceptance[1].Status != "failed" {
		t.Fatalf("expected pass/fail acceptance evidence, got %#v", acceptance)
	}
	run := runtimeRef.SessionSnapshot().WorkflowRuns[0]
	if len(run.CompletedStages) != 1 || len(run.CompletedStages[0].Acceptance) != 2 {
		t.Fatalf("expected acceptance persisted in run snapshot, got %#v", run.CompletedStages)
	}
	var acceptanceArtifact bool
	for _, artifact := range run.Artifacts {
		if artifact.Kind == "acceptance" && strings.Contains(artifact.Summary, "tests") {
			if artifact.Metadata["producer_stage"] != "audit" || artifact.Metadata["producer_agent"] != "auditor" || artifact.Metadata["evidence_category"] != "acceptance" || artifact.Metadata["validation_status"] != "failed" || artifact.Metadata["refs"] != "result.output" {
				t.Fatalf("expected acceptance artifact provenance metadata, got %#v", artifact.Metadata)
			}
			acceptanceArtifact = true
			break
		}
	}
	if !acceptanceArtifact {
		t.Fatalf("expected acceptance replay artifact, got %#v", run.Artifacts)
	}
}

func TestWorkflowRunnerQualityGateRoutesFailedEvidence(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "quality-gate-flow", `
name: quality-gate-flow
description: Route failed quality evidence to remediation.
stages:
  - name: audit
    agent: auditor
    skill: code-audit
    next: [quality]
    acceptance_criteria:
      - name: mentions-risk
        ref: result.output
        contains: risk
      - name: mentions-tests
        ref: result.output
        contains: tests
  - name: quality
    node_type: quality_gate
    params:
      require_acceptance: "true"
      min_score: "90"
    routes:
      pass: done
      fail: remediate
  - name: remediate
    agent: fixer
    skill: code-writing
  - name: done
    node_type: end
`)
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "risk reviewed"}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "remediation complete"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "quality-gate-flow", "review quality", true, nil)
	if err != nil {
		t.Fatalf("Run quality gate workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 3 {
		t.Fatalf("expected quality gate workflow to route through remediation, got %#v", result)
	}
	if string(result.CompletedStages[1].Stage) != "quality" || result.CompletedStages[1].NodeType != "quality_gate" {
		t.Fatalf("expected quality gate as second stage, got %#v", result.CompletedStages[1])
	}
	quality := result.CompletedStages[1]
	if quality.Output.Variables["route"] != "fail" || quality.Output.Variables["quality_status"] != "failed" || quality.Output.Variables["acceptance_failed"] != "1" {
		t.Fatalf("expected failed quality gate variables, got %#v", quality.Output.Variables)
	}
	if string(result.CompletedStages[2].Stage) != "remediate" || len(fixerLLM.requests) != 1 {
		t.Fatalf("expected remediation stage execution, stages=%#v fixerRequests=%d", result.CompletedStages, len(fixerLLM.requests))
	}
}

func TestWorkflowRunnerSkipsVisualStartAndEndNodes(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "visual-flow", `
name: visual-flow
description: Visual start/end nodes should not execute as LLM stages.
stages:
  - name: start
    node_type: start
    next: [plan]
    position: {x: 40, y: 120}
  - name: plan
    node_type: agent
    agent: planner
    skill: execution-plan
    next: [audit]
    params:
      goal: scoped plan
    position: {x: 260, y: 120}
  - name: audit
    node_type: skill
    agent: auditor
    skill: code-audit
    next: [end]
    position: {x: 520, y: 120}
  - name: end
    node_type: end
    position: {x: 760, y: 120}
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan ready."}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Audit done."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "visual-flow", "review this", true, nil)
	if err != nil {
		t.Fatalf("Run visual workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 {
		t.Fatalf("expected only executable stages to complete, got %#v", result)
	}
	if result.CompletedStages[0].Stage != "plan" || result.CompletedStages[1].Stage != "audit" {
		t.Fatalf("unexpected completed stages: %#v", result.CompletedStages)
	}
	if !strings.Contains(plannerLLM.requests[0].Messages[len(plannerLLM.requests[0].Messages)-1].Content, "goal: scoped plan") {
		t.Fatalf("expected stage params in prompt, got %#v", plannerLLM.requests[0].Messages)
	}
}

func TestWorkflowRunnerConfiguredWorkflowOverridesLegacyRegistry(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "plan-fix-audit", `
name: plan-fix-audit
description: Configured graph should be used before the legacy registry entry.
stages:
  - name: start
    node_type: start
    next: [plan]
  - name: plan
    node_type: agent
    agent: planner
    skill: execution-plan
    next: [end]
  - name: end
    node_type: end
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Configured plan done."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "legacy fixer should not run"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "legacy auditor should not run"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "plan-fix-audit", "plan only", false, nil)
	if err != nil {
		t.Fatalf("Run configured plan-fix-audit: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 1 || result.PendingApproval {
		t.Fatalf("expected configured graph to complete without legacy approval gate, got %#v", result)
	}
	summaries := runtimeRef.WorkflowRunner().ListWorkflowGraphs()
	if len(summaries) != 1 || summaries[0].Name != "plan-fix-audit" || summaries[0].Source != "default" {
		t.Fatalf("expected configured default workflow summary, got %#v", summaries)
	}
	executors := runtimeRef.WorkflowRunner().WorkflowExecutors()
	var foundGraph bool
	for _, executor := range executors {
		if executor.Name == "plan-fix-audit" && executor.Source == "default" && executor.Editable && executor.OverridesLegacy && !executor.Overridden && !executor.Legacy {
			foundGraph = true
		}
		if executor.Name == "plan-fix-audit" && executor.Source == "legacy_executor" && executor.Valid {
			t.Fatalf("expected legacy plan-fix-audit executor to be hidden by configured graph, got %#v", executor)
		}
	}
	if !foundGraph {
		t.Fatalf("expected configured graph executor to mark legacy override, got %#v", executors)
	}
}

func TestWorkflowRunnerWorkflowExecutorsExposeLegacyCompatibilityEntries(t *testing.T) {
	runtimeHome := t.TempDir()
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())

	executors := runtimeRef.WorkflowRunner().WorkflowExecutors()
	seen := map[string]agentWorkflowExecutorAssertion{}
	for _, executor := range executors {
		seen[executor.Name] = agentWorkflowExecutorAssertion{
			source:        executor.Source,
			valid:         executor.Valid,
			editable:      executor.Editable,
			legacy:        executor.Legacy,
			compatibility: executor.Compatibility,
			detail:        executor.Detail,
		}
	}
	for _, name := range []string{"plan-fix-audit", "skill-chain"} {
		item, ok := seen[name]
		if !ok {
			t.Fatalf("expected legacy executor %s in workflow executors, got %#v", name, executors)
		}
		if item.source != "legacy_executor" || !item.valid || item.editable || !item.legacy || !item.compatibility || item.detail == "" {
			t.Fatalf("expected legacy executor metadata for %s, got %#v", name, item)
		}
	}
}

func TestWorkflowRunnerWorkflowExecutorsExposeInvalidGraphAsEditableAndBlockLegacy(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "skill-chain", `
name: skill-chain
description: Broken override that should still be editable.
`)
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())

	executors := runtimeRef.WorkflowRunner().WorkflowExecutors()
	var graphEntry, legacyEntry *WorkflowExecutorOption
	for i := range executors {
		executor := &executors[i]
		if executor.Name == "skill-chain" && executor.Source == "default" {
			graphEntry = executor
		}
		if executor.Name == "skill-chain" && executor.Source == "legacy_executor" {
			legacyEntry = executor
		}
	}
	if graphEntry == nil || graphEntry.Valid || !graphEntry.Editable || !graphEntry.OverridesLegacy || graphEntry.Error == "" {
		t.Fatalf("expected invalid graph override to stay editable, got graph=%#v executors=%#v", graphEntry, executors)
	}
	if legacyEntry == nil || legacyEntry.Valid || !legacyEntry.Legacy || !legacyEntry.Compatibility || !legacyEntry.Overridden || legacyEntry.Error == "" {
		t.Fatalf("expected invalid graph to block legacy compatibility executor, got legacy=%#v executors=%#v", legacyEntry, executors)
	}
}

type agentWorkflowExecutorAssertion struct {
	source        string
	valid         bool
	editable      bool
	legacy        bool
	compatibility bool
	detail        string
}

func TestWorkflowRunnerCustomWorkflowGraphSelectsBranch(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "release-check", `
name: release-check
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    next_strategy: select
    next: [implement, audit]
  - name: implement
    agent: fixer
    skill: code-writing
  - name: audit
    agent: auditor
    skill: code-audit
`)
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Security audit should run next."}}}},
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix should not run"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Audit done."}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "release-check", "review the login code", true, nil)
	if err != nil {
		t.Fatalf("Run custom workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 {
		t.Fatalf("expected triage plus selected audit stage, got %#v", result)
	}
	if result.CompletedStages[1].Stage != "audit" || result.CompletedStages[1].Agent != "auditor" {
		t.Fatalf("expected selected audit branch, got %#v", result.CompletedStages[1])
	}
}

func TestWorkflowRunnerCustomWorkflowGraphPassesMappedStageOutputs(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "data-flow", `
name: data-flow
stages:
  - name: plan
    agent: planner
    skill: execution-plan
    outputs:
      plan_text: result.output
    next: [implement]
  - name: implement
    agent: fixer
    skill: code-writing
    input:
      approved_plan: stages.plan.outputs.plan_text
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan ready with exact steps."}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Implemented from mapped plan."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "data-flow", "implement it", true, nil)
	if err != nil {
		t.Fatalf("Run data-flow workflow graph: %v", err)
	}
	if got := result.CompletedStages[0].Output.Variables["plan_text"]; got != "Plan ready with exact steps." {
		t.Fatalf("expected structured plan output, got %#v", result.CompletedStages[0].Output)
	}
	implementPrompt := fixerLLM.requests[0].Messages[len(fixerLLM.requests[0].Messages)-1].Content
	if !strings.Contains(implementPrompt, "Mapped stage inputs") || !strings.Contains(implementPrompt, "approved_plan") || !strings.Contains(implementPrompt, "Plan ready with exact steps.") {
		t.Fatalf("expected mapped plan in implement prompt, got %q", implementPrompt)
	}
	runs := runtimeRef.SessionSnapshot().WorkflowRuns
	if len(runs) == 0 || len(runs[0].CompletedStages) != 2 {
		t.Fatalf("expected persisted workflow run stages, got %#v", runs)
	}
	if got := runs[0].CompletedStages[0].Outputs["plan_text"]; got != "Plan ready with exact steps." {
		t.Fatalf("expected persisted stage output variable, got %#v", runs[0].CompletedStages[0])
	}
	if got := runs[0].CompletedStages[1].Inputs["approved_plan"]; got != "Plan ready with exact steps." {
		t.Fatalf("expected persisted mapped stage input, got %#v", runs[0].CompletedStages[1])
	}
}

func TestWorkflowRunnerCustomWorkflowGraphStageModelOverrideRoutesRequest(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "model-route-flow", `
name: model-route-flow
stages:
  - name: plan
    agent: planner
    skill: execution-plan
    model:
      provider: override
      model: override-model
      max_tokens: 123
      temperature: 0.7
    outputs:
      plan: result.output
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "planner should not be called"}}}}
	overrideLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "override route used"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":     &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner":  plannerLLM,
		"fixer":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor":  &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
		"override": overrideLLM,
	})
	runtimeRef.cfg.Providers = map[string]config.LLMConfig{
		"chat":     {Model: "chat-model"},
		"planner":  {Model: "planner-model"},
		"fixer":    {Model: "fixer-model"},
		"auditor":  {Model: "auditor-model"},
		"override": {Model: "override-default"},
	}

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "model-route-flow", "plan with override", true, nil)
	if err != nil {
		t.Fatalf("Run model-route-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 1 {
		t.Fatalf("expected one completed model route stage, got %#v", result)
	}
	if plannerLLM.calls != 0 {
		t.Fatalf("expected planner provider not to be called, got %d calls", plannerLLM.calls)
	}
	if overrideLLM.calls != 1 || len(overrideLLM.requests) != 1 {
		t.Fatalf("expected override provider to be called once, got calls=%d requests=%d", overrideLLM.calls, len(overrideLLM.requests))
	}
	request := overrideLLM.requests[0]
	if request.Model != "override-model" || request.MaxTokens != 123 || request.Temperature != 0.7 {
		t.Fatalf("expected overridden model settings, got %#v", request)
	}
	stage := result.CompletedStages[0]
	if stage.Agent != "planner" || stage.Result.Model != "override-model" {
		t.Fatalf("expected stage to keep logical planner agent and report override model, got %#v", stage)
	}
	if stage.Metadata["model.override"] != "true" ||
		stage.Metadata["model.provider"] != "override" ||
		stage.Metadata["model.model"] != "override-model" ||
		stage.Metadata["model.max_tokens"] != "123" ||
		stage.Metadata["model.temperature"] != "0.7" {
		t.Fatalf("expected model override metadata, got %#v", stage.Metadata)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphStageToolScopeFiltersPromptTools(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "tool-scope-flow", `
name: tool-scope-flow
stages:
  - name: inspect
    agent: fixer
    skill: code-writing
    params:
      tools: file_tools/read_file
    outputs:
      summary: result.summary
`)
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "inspected"}}}}
	readTool := schema.Tool{Name: "read_file", Server: "file_tools", Kind: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}
	writeTool := schema.Tool{Name: "write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}`)}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{tools: []schema.Tool{readTool, writeTool}}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":   fixerLLM,
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "tool-scope-flow", "inspect only", true, nil)
	if err != nil {
		t.Fatalf("Run tool-scope-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 1 {
		t.Fatalf("expected completed scoped tool stage, got %#v", result)
	}
	if len(fixerLLM.requests) != 1 {
		t.Fatalf("expected one fixer request, got %d", len(fixerLLM.requests))
	}
	tools := fixerLLM.requests[0].Tools
	if len(tools) != 1 || tools[0].Name != "read_file" || tools[0].Server != "file_tools" {
		t.Fatalf("expected stage-scoped read tool only, got %#v", tools)
	}
	stage := result.CompletedStages[0]
	if stage.Metadata["tool.stage_scoped"] != "true" || stage.Metadata["tool.allowed"] != "file_tools/read_file" {
		t.Fatalf("expected stage tool scope metadata, got %#v", stage.Metadata)
	}
}

func TestWorkflowRunnerValidationRejectsUnknownStageScopedTool(t *testing.T) {
	runtimeHome := t.TempDir()
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{tools: []schema.Tool{{Name: "read_file", Server: "file_tools", Kind: "read"}}}, workflowGraphTestClients())
	result := runtimeRef.WorkflowRunner().ValidateWorkflowGraphDocument("bad-tool-scope", WorkflowGraphDocument{
		Name: "bad-tool-scope",
		Stages: []WorkflowGraphStageDocument{{
			Name:  "inspect",
			Agent: "planner",
			Skill: "execution-plan",
			Params: map[string]string{
				"tools": "file_tools/missing_tool",
			},
		}},
	})
	if result.Valid {
		t.Fatalf("expected invalid stage scoped tool, got %#v", result)
	}
	if !workflowValidationIssueContains(result.Issues, "error", "tool") {
		t.Fatalf("expected tool validation error, got %#v", result.Issues)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphContextContractLimitsPriorContext(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "context-flow", `
name: context-flow
stages:
  - name: collect
    agent: planner
    skill: execution-plan
    outputs:
      public_summary: result.summary
      raw_blob: result.output
    artifacts:
      - name: report
        kind: report
        ref: result.output
        summary: result.summary
    next: [review]
  - name: review
    agent: auditor
    skill: code-audit
    input:
      approved_summary: stages.collect.outputs.public_summary
    context:
      include:
        - stages.collect.outputs.public_summary
        - stages.collect.artifacts.report
      exclude:
        - raw_output
        - raw_tool_logs
      max_tokens: 80
    outputs:
      review: result.output
`)
	rawOutput := strings.Repeat("safe summary sentence. ", 8) + strings.Repeat("internal-blob ", 120)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: rawOutput}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "reviewed"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "context-flow", "review context", true, nil)
	if err != nil {
		t.Fatalf("Run context-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 {
		t.Fatalf("expected context workflow to complete, got %#v", result)
	}
	if len(auditorLLM.requests) != 1 {
		t.Fatalf("expected one auditor request, got %d", len(auditorLLM.requests))
	}
	reviewPrompt := auditorLLM.requests[0].Messages[len(auditorLLM.requests[0].Messages)-1].Content
	if !strings.Contains(reviewPrompt, "Selected context") {
		t.Fatalf("expected selected context block, got %q", reviewPrompt)
	}
	if !strings.Contains(reviewPrompt, "stages.collect.outputs.public_summary") || !strings.Contains(reviewPrompt, "stages.collect.artifacts.report") {
		t.Fatalf("expected included summary and artifact refs, got %q", reviewPrompt)
	}
	if strings.Contains(reviewPrompt, "Completed prior stages") {
		t.Fatalf("expected context contract to replace broad prior-stage context, got %q", reviewPrompt)
	}
	if strings.Contains(reviewPrompt, "stages.collect.outputs.raw_blob") || strings.Contains(reviewPrompt, "internal-blob internal-blob internal-blob internal-blob internal-blob internal-blob") {
		t.Fatalf("expected raw output to be omitted or tightly truncated, got %q", reviewPrompt)
	}
	if !strings.Contains(reviewPrompt, "approved_summary") {
		t.Fatalf("expected explicit mapped input to stay available, got %q", reviewPrompt)
	}
	review := result.CompletedStages[1]
	if review.Metadata["context.include"] != "stages.collect.outputs.public_summary,stages.collect.artifacts.report" ||
		review.Metadata["context.exclude"] != "raw_output,raw_tool_logs" ||
		review.Metadata["context.max_tokens"] != "80" {
		t.Fatalf("expected context contract metadata, got %#v", review.Metadata)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphContextContractUsesStructuredOutputs(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "structured-context-flow", `
name: structured-context-flow
stages:
  - name: assess
    agent: planner
    skill: execution-plan
    outputs:
      decision: "'approve'"
      next_actions: '["ship release","notify ops"]'
      raw_blob: result.output
    artifacts:
      - name: review-report
        kind: report
        title: Review report
        ref: result.summary
    next: [route]
  - name: route
    node_type: condition
    condition: stages.assess.outputs.decision == "approve"
    routes:
      "true": deliver
      "false": stop
  - name: deliver
    agent: auditor
    skill: code-audit
    context:
      include:
        - previous.decision
        - stages.assess.outputs.next_actions
        - stages.assess.outputs.evidence
      exclude:
        - raw_blob
      max_tokens: 120
    outputs:
      summary: result.summary
  - name: stop
    node_type: end
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: strings.Repeat("raw-secret ", 80)}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "delivered"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "structured-context-flow", "release decision", true, nil)
	if err != nil {
		t.Fatalf("Run structured-context-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 3 {
		t.Fatalf("expected structured context workflow to complete, got %#v", result)
	}
	assess := result.CompletedStages[0]
	if assess.Output.Decision != "approve" {
		t.Fatalf("expected canonical decision, got %#v", assess.Output)
	}
	if len(assess.Output.NextActions) != 2 || assess.Output.NextActions[0] != "ship release" {
		t.Fatalf("expected canonical next actions, got %#v", assess.Output.NextActions)
	}
	if len(assess.Output.Evidence) == 0 {
		t.Fatalf("expected report artifact to be exposed as evidence, got %#v", assess.Output.Artifacts)
	}
	route := result.CompletedStages[1]
	if route.Output.Variables["route"] != "true" || route.Output.Variables["target"] != "deliver" {
		t.Fatalf("expected condition to route on canonical decision, got %#v", route.Output.Variables)
	}
	if len(auditorLLM.requests) != 1 {
		t.Fatalf("expected one auditor request, got %d", len(auditorLLM.requests))
	}
	deliverPrompt := auditorLLM.requests[0].Messages[len(auditorLLM.requests[0].Messages)-1].Content
	if !strings.Contains(deliverPrompt, "Selected context") ||
		!strings.Contains(deliverPrompt, "previous.decision: true") ||
		!strings.Contains(deliverPrompt, "stages.assess.outputs.next_actions") ||
		!strings.Contains(deliverPrompt, "stages.assess.outputs.evidence") {
		t.Fatalf("expected selected structured context, got %q", deliverPrompt)
	}
	if strings.Contains(deliverPrompt, "raw-secret raw-secret raw-secret") || strings.Contains(deliverPrompt, "raw_blob") {
		t.Fatalf("expected raw blob to stay out of selected context, got %q", deliverPrompt)
	}
}

func TestWorkflowRunnerWorkflowGraphContextContractInjectsMemoryAndFileSummaries(t *testing.T) {
	runtimeHome := t.TempDir()
	workspaceRoot := t.TempDir()
	authPath := filepath.Join(workspaceRoot, "auth.go")
	if err := os.WriteFile(authPath, []byte("package auth\n\nfunc LoginValidator() string { return \"secret-token\" }\n"), 0o644); err != nil {
		t.Fatalf("WriteFile auth.go: %v", err)
	}
	store := memory.NewStore(workspaceRoot)
	if err := store.Ensure(); err != nil {
		t.Fatalf("Ensure memory store: %v", err)
	}
	if _, err := store.UpdateProject("# Project Memory\n\n## Project Goal\n- Ship auth context contracts without raw file leakage.\n"); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if _, err := store.RecordTask(memory.TaskSummary{
		UserGoal:     "auth context retrieval",
		KeyDecisions: []string{"reuse auth file summaries"},
		ModifiedFiles: []string{
			"auth.go",
		},
	}); err != nil {
		t.Fatalf("RecordTask: %v", err)
	}
	if _, err := store.RebuildFiles(context.Background()); err != nil {
		t.Fatalf("RebuildFiles: %v", err)
	}
	writeWorkflowGraph(t, runtimeHome, "memory-context-flow", `
name: memory-context-flow
stages:
  - name: change
    agent: fixer
    skill: code-writing
    artifacts:
      - name: auth-change
        kind: change
        ref: result.summary
        summary: result.summary
        metadata:
          path: auth.go
    next: [review]
  - name: review
    agent: auditor
    skill: code-audit
    context:
      include:
        - memory.project
        - files.changed
      retrieval:
        enabled: true
        query: auth context
      max_tokens: 300
`)
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{
			Content: "changed auth",
		},
	}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "reviewed memory context"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})
	runtimeRef.SetMemoryStore(store)

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "memory-context-flow", "review auth context", true, nil)
	if err != nil {
		t.Fatalf("Run memory-context-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 {
		t.Fatalf("expected memory context workflow to complete, got %#v", result)
	}
	if len(auditorLLM.requests) != 1 {
		t.Fatalf("expected one auditor request, got %d", len(auditorLLM.requests))
	}
	reviewPrompt := auditorLLM.requests[0].Messages[len(auditorLLM.requests[0].Messages)-1].Content
	if !strings.Contains(reviewPrompt, "memory.project") || !strings.Contains(reviewPrompt, "Ship auth context contracts") {
		t.Fatalf("expected project memory summary in selected context, got %q", reviewPrompt)
	}
	if !strings.Contains(reviewPrompt, "files.changed.auth.go") || !strings.Contains(reviewPrompt, "content_mode=summary") || !strings.Contains(reviewPrompt, "hash=") {
		t.Fatalf("expected changed file summary/hash context, got %q", reviewPrompt)
	}
	if !strings.Contains(reviewPrompt, "memory.search.") || !strings.Contains(reviewPrompt, "auth context retrieval") {
		t.Fatalf("expected retrieval memory context, got %q", reviewPrompt)
	}
	if strings.Contains(reviewPrompt, "secret-token") {
		t.Fatalf("expected workflow context to avoid raw file content, got %q", reviewPrompt)
	}
	review := result.CompletedStages[1]
	if review.Metadata["context.retrieval.enabled"] != "true" || review.Metadata["context.retrieval.query"] != "auth context" {
		t.Fatalf("expected retrieval metadata, got %#v", review.Metadata)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphCanonicalizesJSONStageOutput(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "json-output-flow", `
name: json-output-flow
stages:
  - name: assess
    agent: planner
    skill: execution-plan
    next: [route]
  - name: route
    node_type: condition
    condition: stages.assess.outputs.decision == "approve"
    routes:
      "true": deliver
      "false": stop
  - name: deliver
    agent: auditor
    skill: code-audit
    context:
      include:
        - stages.assess.outputs.summary
        - stages.assess.outputs.next_actions
      max_tokens: 100
  - name: stop
    node_type: end
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "```json\n{\"summary\":\"release looks ready\",\"decision\":\"approve\",\"next_actions\":[\"ship\",\"notify\"]}\n```"}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "delivered"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "json-output-flow", "release json decision", true, nil)
	if err != nil {
		t.Fatalf("Run json-output-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 3 {
		t.Fatalf("expected json output workflow to complete, got %#v", result)
	}
	assess := result.CompletedStages[0]
	if assess.Output.Decision != "approve" || len(assess.Output.NextActions) != 2 || assess.Output.NextActions[0] != "ship" {
		t.Fatalf("expected JSON output fields to be canonicalized, got %#v", assess.Output)
	}
	route := result.CompletedStages[1]
	if route.Output.Variables["target"] != "deliver" {
		t.Fatalf("expected condition to use canonical JSON decision, got %#v", route.Output.Variables)
	}
	deliverPrompt := auditorLLM.requests[0].Messages[len(auditorLLM.requests[0].Messages)-1].Content
	if !strings.Contains(deliverPrompt, "release looks ready") || !strings.Contains(deliverPrompt, "stages.assess.outputs.next_actions") {
		t.Fatalf("expected canonical JSON summary and next actions in context, got %q", deliverPrompt)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphPromptBudgetsAndWorkerContract(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "budget-worker-flow", `
name: budget-worker-flow
description: This workflow has a very long description that should be preserved until the final prompt budget is reached.
stages:
  - name: plan
    agent: planner
    skill: execution-plan
    outputs:
      summary: result.summary
      long_report: result.output
    next: [worker]
  - name: worker
    agent: fixer
    skill: code-writing
    params:
      worker_contract: engineering_v1
      output_contract: Emit JSON worker fields.
      huge_param: "`+strings.Repeat("param-noise ", 120)+`"
    input:
      upstream: stages.plan.outputs.long_report
    context:
      include:
        - stages.plan.outputs.summary
      prompt_max_tokens: 520
      request_max_tokens: 40
      inputs_max_tokens: 45
      parameters_max_tokens: 50
      max_tokens: 90
    outputs:
      summary: result.summary
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: strings.Repeat("plan-context ", 200)}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "worker done"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "budget-worker-flow", strings.Repeat("request-noise ", 120), true, nil)
	if err != nil {
		t.Fatalf("Run budget-worker-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 {
		t.Fatalf("expected budget worker workflow to complete, got %#v", result)
	}
	prompt := fixerLLM.requests[0].Messages[len(fixerLLM.requests[0].Messages)-1].Content
	for _, want := range []string{
		"original request truncated by request_max_tokens",
		"mapped input truncated by inputs_max_tokens",
		"worker_contract: engineering_v1",
		"Worker contract engineering_v1",
		"changed_files",
		"blockers",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, prompt)
		}
	}
	stage := result.CompletedStages[1]
	if stage.Metadata["context.prompt_max_tokens"] != "520" ||
		stage.Metadata["context.request_max_tokens"] != "40" ||
		stage.Metadata["context.inputs_max_tokens"] != "45" ||
		stage.Metadata["context.parameters_max_tokens"] != "50" {
		t.Fatalf("expected prompt budget metadata, got %#v", stage.Metadata)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphJSONWorkerFieldsFeedContext(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "worker-json-flow", `
name: worker-json-flow
stages:
  - name: worker
    agent: fixer
    skill: code-writing
    params:
      worker_contract: engineering_v1
    outputs:
      summary: result.summary
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
    context:
      include:
        - stages.worker.outputs.changed_files
        - stages.worker.outputs.verification
        - stages.worker.outputs.blockers
        - stages.worker.outputs.next_actions
      max_tokens: 160
    outputs:
      report: result.output
`)
	workerJSON := `{"summary":"slice complete","changed_files":["internal/a.go"],"verification":["go test ./internal/a"],"blockers":[],"next_actions":["audit"]}`
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "```json\n" + workerJSON + "\n```"}}}}
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "reported"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "worker-json-flow", "ship worker json", true, nil)
	if err != nil {
		t.Fatalf("Run worker-json-flow workflow graph: %v", err)
	}
	worker := result.CompletedStages[0]
	if worker.Output.Variables["changed_files"] == "" || worker.Output.Variables["verification"] == "" || worker.Output.Variables["blockers"] == "" {
		t.Fatalf("expected worker JSON fields to be captured, got %#v", worker.Output)
	}
	reportPrompt := plannerLLM.requests[0].Messages[len(plannerLLM.requests[0].Messages)-1].Content
	for _, want := range []string{"stages.worker.outputs.changed_files", "internal/a.go", "stages.worker.outputs.verification", "go test ./internal/a", "stages.worker.outputs.blockers"} {
		if !strings.Contains(reportPrompt, want) {
			t.Fatalf("expected report prompt to contain %q, got %q", want, reportPrompt)
		}
	}
}

func TestWorkflowRunnerWorkflowGraphDocumentPersistsContextContract(t *testing.T) {
	runtimeHome := t.TempDir()
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})
	doc := WorkflowGraphDocument{
		Name: "document-context-flow",
		Stages: []WorkflowGraphStageDocument{
			{Name: "plan", Agent: "planner", Skill: "execution-plan", Next: []string{"review"}, Outputs: map[string]string{"summary": "result.summary"}},
			{
				Name:  "review",
				Agent: "auditor",
				Skill: "code-audit",
				Context: WorkflowGraphStageContextDocument{
					Include:             []string{"stages.plan.outputs.summary", "memory.project"},
					Exclude:             []string{"raw_tool_logs"},
					MaxTokens:           900,
					PromptMaxTokens:     1200,
					RequestMaxTokens:    300,
					InputsMaxTokens:     400,
					ParametersMaxTokens: 200,
					Retrieval:           WorkflowGraphContextRetrievalDocument{Enabled: true, Query: "{{input.goal}}"},
				},
			},
		},
	}
	if err := runtimeRef.WorkflowRunner().SaveWorkflowGraphDocument(doc.Name, doc); err != nil {
		t.Fatalf("SaveWorkflowGraphDocument with context contract: %v", err)
	}
	loaded, err := runtimeRef.WorkflowRunner().LoadWorkflowGraphDocument(doc.Name)
	if err != nil {
		t.Fatalf("LoadWorkflowGraphDocument with context contract: %v", err)
	}
	if len(loaded.Stages) != 2 {
		t.Fatalf("expected two loaded stages, got %#v", loaded)
	}
	context := loaded.Stages[1].Context
	if context.MaxTokens != 900 || !context.Retrieval.Enabled || context.Retrieval.Query != "{{input.goal}}" ||
		context.PromptMaxTokens != 1200 || context.RequestMaxTokens != 300 ||
		context.InputsMaxTokens != 400 || context.ParametersMaxTokens != 200 ||
		len(context.Include) != 2 || context.Include[0] != "stages.plan.outputs.summary" ||
		len(context.Exclude) != 1 || context.Exclude[0] != "raw_tool_logs" {
		t.Fatalf("expected context contract to round-trip, got %#v", context)
	}
	graph := loaded.toInternalGraph()
	if graph.Stages[1].Context.MaxTokens != 900 || !graph.Stages[1].Context.Retrieval.Enabled ||
		graph.Stages[1].Context.PromptMaxTokens != 1200 || graph.Stages[1].Context.RequestMaxTokens != 300 ||
		graph.Stages[1].Context.InputsMaxTokens != 400 || graph.Stages[1].Context.ParametersMaxTokens != 200 ||
		graph.Stages[1].Context.Include[1] != "memory.project" {
		t.Fatalf("expected context contract to convert to runtime graph, got %#v", graph.Stages[1].Context)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphDeclaresReplayArtifacts(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "artifact-flow", `
name: artifact-flow
stages:
  - name: plan
    agent: planner
    skill: execution-plan
    outputs:
      plan_text: result.output
    artifacts:
      - name: implementation-plan
        kind: report
        title: Implementation plan
        ref: result.output
        summary: result.summary
        metadata:
          audience: engineer
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan ready with exact steps."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "artifact-flow", "plan it", true, nil)
	if err != nil {
		t.Fatalf("Run artifact-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 1 {
		t.Fatalf("expected artifact flow to complete, got %#v", result)
	}
	declared := result.CompletedStages[0].Output.Artifacts
	if len(declared) != 1 || declared[0].Kind != "report" || declared[0].Title != "Implementation plan" || declared[0].Metadata["declared"] != "true" {
		t.Fatalf("expected declared stage artifact, got %#v", declared)
	}
	runs := runtimeRef.SessionSnapshot().WorkflowRuns
	if len(runs) == 0 || len(runs[0].CompletedStages) != 1 {
		t.Fatalf("expected persisted workflow run, got %#v", runs)
	}
	var foundDeclared bool
	var foundAutoOutput bool
	for _, artifact := range runs[0].CompletedStages[0].Artifacts {
		if artifact.Metadata["declared"] == "true" && artifact.Kind == "report" && strings.Contains(artifact.Content, "Plan ready") && artifact.Metadata["audience"] == "engineer" {
			foundDeclared = true
		}
		if artifact.Kind == "output" && artifact.Title == "Stage output" {
			foundAutoOutput = true
		}
	}
	if !foundDeclared || !foundAutoOutput {
		t.Fatalf("expected declared and automatic artifacts, got %#v", runs[0].CompletedStages[0].Artifacts)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphConditionRoutesByStageOutput(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "vuln-flow", `
name: vuln-flow
stages:
  - name: audit
    agent: auditor
    skill: code-audit
    outputs:
      audit_text: result.output
    next: [has-vuln]
  - name: has-vuln
    node_type: condition
    condition: contains(stages.audit.outputs.audit_text, "vulnerability")
    routes:
      true: verify
      false: report-clean
  - name: verify
    agent: auditor
    skill: code-audit
    next: [end]
  - name: report-clean
    agent: planner
    skill: execution-plan
    next: [end]
  - name: end
    node_type: end
`)
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Potential vulnerability found in auth flow."}},
		{Message: schema.Message{Content: "Verification done."}},
	}}
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "clean report should not run"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "vuln-flow", "audit target", true, nil)
	if err != nil {
		t.Fatalf("Run vuln-flow workflow graph: %v", err)
	}
	if len(result.CompletedStages) != 3 || result.CompletedStages[1].Stage != "has-vuln" || result.CompletedStages[2].Stage != "verify" {
		t.Fatalf("expected audit then verify branch, got %#v", result.CompletedStages)
	}
	if result.CompletedStages[1].NodeType != "condition" || result.CompletedStages[1].Output.Variables["route"] != "true" || result.CompletedStages[1].Output.Variables["target"] != "verify" {
		t.Fatalf("expected persisted condition decision, got %#v", result.CompletedStages[1])
	}
	if plannerLLM.calls != 0 {
		t.Fatalf("expected false branch not to run, got %d planner calls", plannerLLM.calls)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphSwitchRoutesByCase(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "switch-flow", `
name: switch-flow
stages:
  - name: classify
    agent: planner
    skill: execution-plan
    outputs:
      finding_type: result.output
    next: [route]
  - name: route
    node_type: switch
    switch_on: stages.classify.outputs.finding_type
    cases:
      xss: xss-review
      default: generic-review
  - name: xss-review
    agent: auditor
    skill: code-audit
    next: [end]
  - name: generic-review
    agent: fixer
    skill: code-writing
    next: [end]
  - name: end
    node_type: end
`)
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "XSS review done."}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "generic should not run"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "XSS"}}}},
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "switch-flow", "classify finding", true, nil)
	if err != nil {
		t.Fatalf("Run switch-flow workflow graph: %v", err)
	}
	if len(result.CompletedStages) != 3 || result.CompletedStages[1].Stage != "route" || result.CompletedStages[2].Stage != "xss-review" {
		t.Fatalf("expected xss-review branch, got %#v", result.CompletedStages)
	}
	if result.CompletedStages[1].NodeType != "switch" || result.CompletedStages[1].Output.Variables["value"] != "xss" || result.CompletedStages[1].Output.Variables["target"] != "xss-review" {
		t.Fatalf("expected persisted switch decision, got %#v", result.CompletedStages[1])
	}
	if fixerLLM.calls != 0 {
		t.Fatalf("expected default branch not to run, got %d fixer calls", fixerLLM.calls)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphParallelJoinWaitsForBranches(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "parallel-flow", `
name: parallel-flow
stages:
  - name: start
    node_type: start
    next: [split]
  - name: split
    node_type: parallel
    next: [research, audit]
  - name: research
    agent: planner
    skill: execution-plan
    outputs:
      research_output: result.output
    next: [join]
  - name: audit
    agent: auditor
    skill: code-audit
    outputs:
      audit_output: result.output
    next: [join]
  - name: join
    node_type: join
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
    input:
      research: stages.research.outputs.research_output
      audit: stages.audit.outputs.audit_output
  - name: end
    node_type: end
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Research branch complete."}},
		{Message: schema.Message{Content: "Joined report complete."}},
	}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Audit branch complete."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "parallel-flow", "run branches", true, nil)
	if err != nil {
		t.Fatalf("Run parallel-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 5 {
		t.Fatalf("expected split, two branches, join, report stages, got %#v", result.CompletedStages)
	}
	wantStages := []WorkflowStage{"split", "research", "audit", "join", "report"}
	for i, want := range wantStages {
		if result.CompletedStages[i].Stage != want {
			t.Fatalf("expected stage %d to be %s, got %#v", i, want, result.CompletedStages)
		}
	}
	if result.CompletedStages[0].NodeType != "parallel" || result.CompletedStages[0].Output.Variables["branch_count"] != "2" {
		t.Fatalf("expected persisted parallel fan-out decision, got %#v", result.CompletedStages[0])
	}
	join := result.CompletedStages[3]
	if join.NodeType != "join" || join.Output.Variables["route"] != "joined" || !strings.Contains(join.Output.Variables["wait_for"], "research") || !strings.Contains(join.Output.Variables["wait_for"], "audit") {
		t.Fatalf("expected persisted join decision after both branches, got %#v", join)
	}
	if plannerLLM.calls != 2 || auditorLLM.calls != 1 {
		t.Fatalf("expected planner twice and auditor once, got planner=%d auditor=%d", plannerLLM.calls, auditorLLM.calls)
	}
	reportPrompt := plannerLLM.requests[1].Messages[len(plannerLLM.requests[1].Messages)-1].Content
	if !strings.Contains(reportPrompt, "Research branch complete") || !strings.Contains(reportPrompt, "Audit branch complete") {
		t.Fatalf("expected joined branch outputs in report prompt, got %q", reportPrompt)
	}
}

type workflowBlockingLLMClient struct {
	started  chan struct{}
	release  chan struct{}
	response string
}

func (s *workflowBlockingLLMClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	panic("unused")
}

func (s *workflowBlockingLLMClient) StreamChat(ctx context.Context, _ schema.ChatRequest, handler interfaces.StreamHandler) (schema.ChatResponse, error) {
	select {
	case <-s.started:
	default:
		close(s.started)
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return schema.ChatResponse{}, ctx.Err()
	}
	if handler != nil {
		if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: s.response}); err != nil {
			return schema.ChatResponse{}, err
		}
	}
	return schema.ChatResponse{Message: schema.Message{Content: s.response}}, nil
}

func (s *workflowBlockingLLMClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

func TestWorkflowRunnerCustomWorkflowGraphParallelCanRunBranchesConcurrently(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "concurrent-parallel-flow", `
name: concurrent-parallel-flow
stages:
  - name: split
    node_type: parallel
    params:
      concurrent: true
    next: [research, audit]
  - name: research
    agent: planner
    skill: execution-plan
    next: [join]
  - name: audit
    agent: auditor
    skill: code-audit
    next: [join]
  - name: join
    node_type: join
    params:
      wait_for: research,audit
    next: [report]
  - name: report
    agent: fixer
    skill: execution-plan
    input:
      research: stages.research.outputs.summary
      audit: stages.audit.outputs.summary
`)
	release := make(chan struct{})
	plannerBranch := &workflowBlockingLLMClient{started: make(chan struct{}), release: release, response: "Concurrent research complete."}
	auditorBranch := &workflowBlockingLLMClient{started: make(chan struct{}), release: release, response: "Concurrent audit complete."}
	reportLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Concurrent report complete."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerBranch,
		"fixer":   reportLLM,
		"auditor": auditorBranch,
	})

	done := make(chan struct {
		result WorkflowResult
		err    error
	}, 1)
	go func() {
		result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "concurrent-parallel-flow", "run concurrent branches", true, nil)
		done <- struct {
			result WorkflowResult
			err    error
		}{result: result, err: err}
	}()

	select {
	case <-plannerBranch.started:
	case <-time.After(500 * time.Millisecond):
		close(release)
		t.Fatal("planner branch did not start")
	}
	select {
	case <-auditorBranch.started:
	case <-time.After(500 * time.Millisecond):
		close(release)
		t.Fatal("auditor branch did not start before planner branch was released; parallel node did not run concurrently")
	}
	close(release)

	var outcome struct {
		result WorkflowResult
		err    error
	}
	select {
	case outcome = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("workflow did not finish after releasing concurrent branches")
	}
	if outcome.err != nil {
		t.Fatalf("Run concurrent-parallel-flow workflow graph: %v", outcome.err)
	}
	if outcome.result.Status != "completed" {
		t.Fatalf("expected completed concurrent workflow, got %#v", outcome.result)
	}
	if len(outcome.result.CompletedStages) != 5 {
		t.Fatalf("expected split, two branches, join, report stages, got %#v", outcome.result.CompletedStages)
	}
	if outcome.result.CompletedStages[1].Metadata["parallel_branch"] != "true" || outcome.result.CompletedStages[2].Metadata["parallel_branch"] != "true" {
		t.Fatalf("expected branch metadata, got %#v", outcome.result.CompletedStages)
	}
	reportPrompt := reportLLM.requests[0].Messages[len(reportLLM.requests[0].Messages)-1].Content
	if !strings.Contains(reportPrompt, "Concurrent research complete") || !strings.Contains(reportPrompt, "Concurrent audit complete") {
		t.Fatalf("expected concurrent branch outputs in report prompt, got %q", reportPrompt)
	}
}

func TestWorkflowRunnerValidationReportsParallelConcurrencyEligibility(t *testing.T) {
	runtimeHome := t.TempDir()
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result := runtimeRef.WorkflowRunner().ValidateWorkflowGraphDocument("eligible-parallel", WorkflowGraphDocument{
		Name: "eligible-parallel",
		Stages: []WorkflowGraphStageDocument{
			{Name: "split", NodeType: "parallel", Params: map[string]string{"concurrent": "true"}, Next: []string{"research", "audit"}},
			{Name: "research", Agent: "planner", Skill: "execution-plan", Next: []string{"join"}},
			{Name: "audit", Agent: "auditor", Skill: "code-audit", Next: []string{"join"}},
			{Name: "join", NodeType: "join", Params: map[string]string{"wait_for": "research,audit"}},
		},
	})
	if !result.Valid || len(result.Parallel) != 1 {
		t.Fatalf("expected valid graph with one parallel diagnostic, got %#v", result)
	}
	parallel := result.Parallel[0]
	if !parallel.Enabled || !parallel.Eligible || parallel.BranchCount != 2 || parallel.Join != "join" || len(parallel.Branches) != 2 {
		t.Fatalf("expected eligible concurrent parallel diagnostic, got %#v", parallel)
	}
	if !parallel.Branches[0].Eligible || len(parallel.Branches[0].Stages) != 1 || !parallel.Branches[1].Eligible {
		t.Fatalf("expected eligible branch diagnostics, got %#v", parallel.Branches)
	}

	conflict := runtimeRef.WorkflowRunner().ValidateWorkflowGraphDocument("agent-conflict-parallel", WorkflowGraphDocument{
		Name: "agent-conflict-parallel",
		Stages: []WorkflowGraphStageDocument{
			{Name: "split", NodeType: "parallel", Params: map[string]string{"concurrent": "true"}, Next: []string{"research", "audit"}},
			{Name: "research", Agent: "planner", Skill: "execution-plan", Next: []string{"join"}},
			{Name: "audit", Agent: "planner", Skill: "code-audit", Next: []string{"join"}},
			{Name: "join", NodeType: "join", Params: map[string]string{"wait_for": "research,audit"}},
		},
	})
	if !conflict.Valid || len(conflict.Parallel) != 1 {
		t.Fatalf("expected structurally valid conflict graph with parallel diagnostic, got %#v", conflict)
	}
	if conflict.Parallel[0].Eligible || len(conflict.Parallel[0].Issues) == 0 || !strings.Contains(conflict.Parallel[0].Issues[0].Message, "used by both") {
		t.Fatalf("expected agent-conflict parallel diagnostic, got %#v", conflict.Parallel[0])
	}

	approval := runtimeRef.WorkflowRunner().ValidateWorkflowGraphDocument("approval-parallel", WorkflowGraphDocument{
		Name: "approval-parallel",
		Stages: []WorkflowGraphStageDocument{
			{Name: "split", NodeType: "parallel", Params: map[string]string{"concurrent": "true"}, Next: []string{"research", "audit"}},
			{Name: "research", Agent: "planner", Skill: "execution-plan", Approval: true, Next: []string{"join"}},
			{Name: "audit", Agent: "auditor", Skill: "code-audit", Next: []string{"join"}},
			{Name: "join", NodeType: "join", Params: map[string]string{"wait_for": "research,audit"}},
		},
	})
	if !approval.Valid || approval.Parallel[0].Eligible || approval.Parallel[0].Branches[0].Eligible || !strings.Contains(approval.Parallel[0].Branches[0].Reason, "approval") {
		t.Fatalf("expected approval branch ineligibility diagnostic, got %#v", approval.Parallel[0])
	}
}

func TestWorkflowRunnerParallelUsesSelectedBranchesAndJoinRefs(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "selected-parallel-flow", `
name: selected-parallel-flow
stages:
  - name: decompose
    agent: planner
    skill: execution-plan
    outputs:
      active_branches: result.active_branches
      summary: result.summary
    next: [dispatch]
  - name: dispatch
    node_type: parallel
    params:
      concurrent: true
      active_branches_ref: stages.decompose.outputs.active_branches
    next: [software-worker, docs-worker, ops-worker]
  - name: software-worker
    agent: fixer
    skill: code-writing
    next: [join]
  - name: docs-worker
    agent: auditor
    skill: execution-plan
    next: [join]
  - name: ops-worker
    agent: chat
    skill: execution-plan
    next: [join]
  - name: join
    node_type: join
    params:
      wait_for_ref: stages.dispatch.outputs.branches
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
    input:
      branches: stages.join.outputs.wait_for
      software: stages.software-worker.outputs.summary
      docs: stages.docs-worker.outputs.summary
      ops: stages.ops-worker.outputs.summary
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: `{"summary":"select bounded branches","active_branches":["software-worker","docs-worker"]}`}},
		{Message: schema.Message{Content: "selected branch report"}},
	}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "software branch complete"}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "docs branch complete"}}}}
	chatLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "ops branch should not run"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    chatLLM,
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "selected-parallel-flow", "run selected branches", true, nil)
	if err != nil {
		t.Fatalf("Run selected-parallel-flow workflow graph: %v", err)
	}
	if workflowStageResultNamesContain(result.CompletedStages, "ops-worker") {
		t.Fatalf("expected unselected ops-worker not to run, got %#v", result.CompletedStages)
	}
	if chatLLM.calls != 0 {
		t.Fatalf("expected chat branch not to be called, got %d calls", chatLLM.calls)
	}
	dispatch := workflowGraphStageResultByName(t, result.CompletedStages, "dispatch")
	if dispatch.Output.Variables["branch_count"] != "2" || !strings.Contains(dispatch.Output.Variables["branches"], "software-worker") || !strings.Contains(dispatch.Output.Variables["branches"], "docs-worker") {
		t.Fatalf("expected dispatch to publish selected branches, got %#v", dispatch.Output)
	}
	join := workflowGraphStageResultByName(t, result.CompletedStages, "join")
	if join.Output.Variables["completed_count"] != "2" || strings.Contains(join.Output.Variables["wait_for"], "ops-worker") {
		t.Fatalf("expected join to wait only for selected branches, got %#v", join.Output)
	}
	reportPrompt := plannerLLM.requests[1].Messages[len(plannerLLM.requests[1].Messages)-1].Content
	if !strings.Contains(reportPrompt, "software branch complete") || !strings.Contains(reportPrompt, "docs branch complete") || strings.Contains(reportPrompt, "ops branch should not run") {
		t.Fatalf("expected final report prompt to include selected branches only, got %q", reportPrompt)
	}
}

func TestWorkflowRunnerValidationRejectsInvalidStageModelOverrides(t *testing.T) {
	runtimeHome := t.TempDir()
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})
	runtimeRef.cfg.Providers = map[string]config.LLMConfig{
		"chat":    {Model: "chat-model"},
		"planner": {Model: "planner-model"},
		"fixer":   {Model: "fixer-model"},
		"auditor": {Model: "auditor-model"},
	}

	result := runtimeRef.WorkflowRunner().ValidateWorkflowGraphDocument("invalid-stage-model", WorkflowGraphDocument{
		Name: "invalid-stage-model",
		Stages: []WorkflowGraphStageDocument{
			{
				Name:  "plan",
				Agent: "planner",
				Skill: "execution-plan",
				Model: WorkflowGraphStageModelDocument{
					Provider:  "missing-provider",
					MaxTokens: -1,
				},
			},
		},
	})
	if result.Valid {
		t.Fatalf("expected invalid stage model override validation, got %#v", result)
	}
	if !workflowValidationIssueContains(result.Issues, "error", "model.max_tokens") {
		t.Fatalf("expected model.max_tokens error, got %#v", result.Issues)
	}
	if !workflowValidationIssueContains(result.Issues, "error", "model.provider") {
		t.Fatalf("expected model.provider error, got %#v", result.Issues)
	}
}

func TestWorkflowRunnerValidationWarnsComplexWorkflowMissingQualityControls(t *testing.T) {
	runtimeHome := t.TempDir()
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result := runtimeRef.WorkflowRunner().ValidateWorkflowGraphDocument("complex-no-quality", WorkflowGraphDocument{
		Name: "complex-no-quality",
		Stages: []WorkflowGraphStageDocument{
			{Name: "plan", Agent: "planner", Skill: "execution-plan", Next: []string{"implement"}},
			{Name: "implement", Agent: "fixer", Skill: "code-writing", Next: []string{"ship"}},
			{Name: "ship", Agent: "chat", Skill: "execution-plan"},
		},
	})
	if !result.Valid {
		t.Fatalf("expected quality-design warnings to keep graph structurally valid, got %#v", result)
	}
	for _, field := range []string{"acceptance_criteria", "node_type", "skill", "artifacts", "outputs", "approval"} {
		if !workflowValidationIssueContains(result.Issues, "warning", field) {
			t.Fatalf("expected warning for %s, got %#v", field, result.Issues)
		}
	}
}

func workflowValidationIssueContains(issues []WorkflowGraphValidationIssue, level, field string) bool {
	for _, issue := range issues {
		if strings.EqualFold(issue.Level, level) && strings.EqualFold(issue.Field, field) {
			return true
		}
	}
	return false
}

func TestWorkflowRunnerCustomWorkflowGraphParallelCanRunLinearBranchChains(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "linear-concurrent-parallel-flow", `
name: linear-concurrent-parallel-flow
stages:
  - name: split
    node_type: parallel
    params:
      concurrent: true
    next: [research, audit]
  - name: research
    agent: planner
    skill: execution-plan
    next: [research-summary]
  - name: research-summary
    agent: planner
    skill: execution-plan
    outputs:
      research_output: result.output
    next: [join]
  - name: audit
    agent: auditor
    skill: code-audit
    next: [audit-verify]
  - name: audit-verify
    agent: auditor
    skill: code-audit
    outputs:
      audit_output: result.output
    next: [join]
  - name: join
    node_type: join
    params:
      wait_for: research-summary,audit-verify
    next: [report]
  - name: report
    agent: fixer
    skill: execution-plan
    input:
      research: stages.research-summary.outputs.research_output
      audit: stages.audit-verify.outputs.audit_output
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Research scan complete."}},
		{Message: schema.Message{Content: "Research summary complete."}},
	}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Audit scan complete."}},
		{Message: schema.Message{Content: "Audit verification complete."}},
	}}
	reportLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Linear branch report complete."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   reportLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "linear-concurrent-parallel-flow", "run linear concurrent branches", true, nil)
	if err != nil {
		t.Fatalf("Run linear-concurrent-parallel-flow workflow graph: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed workflow, got %#v", result)
	}
	wantStages := []WorkflowStage{"split", "research", "research-summary", "audit", "audit-verify", "join", "report"}
	if len(result.CompletedStages) != len(wantStages) {
		t.Fatalf("expected split, linear branches, join, report stages, got %#v", result.CompletedStages)
	}
	for i, want := range wantStages {
		if result.CompletedStages[i].Stage != want {
			t.Fatalf("expected stage %d to be %s, got %#v", i, want, result.CompletedStages)
		}
	}
	for _, index := range []int{1, 2, 3, 4} {
		if result.CompletedStages[index].Metadata["parallel_branch"] != "true" || result.CompletedStages[index].Metadata["parallel_parent"] != "split" {
			t.Fatalf("expected linear branch metadata on stage %d, got %#v", index, result.CompletedStages[index])
		}
	}
	if plannerLLM.calls != 2 || auditorLLM.calls != 2 || reportLLM.calls != 1 {
		t.Fatalf("expected planner=2 auditor=2 report=1, got planner=%d auditor=%d report=%d", plannerLLM.calls, auditorLLM.calls, reportLLM.calls)
	}
	reportPrompt := reportLLM.requests[0].Messages[len(reportLLM.requests[0].Messages)-1].Content
	if !strings.Contains(reportPrompt, "Research summary complete") || !strings.Contains(reportPrompt, "Audit verification complete") {
		t.Fatalf("expected linear branch outputs in report prompt, got %q", reportPrompt)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphPolicyGuardBlocksWithoutTarget(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "policy-flow", `
name: policy-flow
stages:
  - name: guard
    node_type: policy_guard
    policy: contains(workflow.input, "approved")
    params:
      reason: request did not include approval marker
`)
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "policy-flow", "unsafe request", true, nil)
	if err != nil {
		t.Fatalf("Run policy-flow workflow graph: %v", err)
	}
	if result.Status != "blocked" || len(result.CompletedStages) != 1 {
		t.Fatalf("expected blocked workflow with guard stage, got %#v", result)
	}
	guard := result.CompletedStages[0]
	if guard.Status != "blocked" || guard.NodeType != "policy_guard" || guard.Output.Variables["route"] != "deny" || guard.Output.Variables["reason"] == "" {
		t.Fatalf("expected persisted blocked policy decision, got %#v", guard)
	}
	runs := runtimeRef.SessionSnapshot().WorkflowRuns
	if len(runs) == 0 || runs[0].Status != "blocked" || runs[0].CompletedAt == "" {
		t.Fatalf("expected blocked run to be terminal, got %#v", runs)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphPolicyGuardRoutesDeniedBranch(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "policy-route", `
name: policy-route
stages:
  - name: guard
    node_type: policy_guard
    policy: contains(workflow.input, "approved")
    routes:
      allow: implement
      deny: explain
  - name: implement
    agent: fixer
    skill: code-writing
  - name: explain
    agent: planner
    skill: execution-plan
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Denied explanation."}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "should not run"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "policy-route", "unsafe request", true, nil)
	if err != nil {
		t.Fatalf("Run policy-route workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 || result.CompletedStages[1].Stage != "explain" {
		t.Fatalf("expected policy deny route to explanation stage, got %#v", result)
	}
	if result.CompletedStages[0].Output.Variables["target"] != "explain" {
		t.Fatalf("expected policy guard target explain, got %#v", result.CompletedStages[0])
	}
	if fixerLLM.calls != 0 {
		t.Fatalf("expected allow branch not to run, got %d fixer calls", fixerLLM.calls)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphPolicyGuardNamedRiskRule(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "policy-risk", `
name: policy-risk
stages:
  - name: classify
    agent: auditor
    skill: code-audit
    outputs:
      risk: result.output
    next: [guard]
  - name: guard
    node_type: policy_guard
    params:
      rule: risk_at_least
      ref: stages.classify.outputs.risk
      minimum: high
    routes:
      allow: verify
      deny: report
  - name: verify
    agent: auditor
    skill: code-audit
    next: [end]
  - name: report
    agent: planner
    skill: execution-plan
    next: [end]
  - name: end
    node_type: end
`)
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "high risk authentication bypass"}},
		{Message: schema.Message{Content: "verification complete"}},
	}}
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "should not report low risk"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "policy-risk", "classify risk", true, nil)
	if err != nil {
		t.Fatalf("Run policy-risk workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 3 || result.CompletedStages[2].Stage != "verify" {
		t.Fatalf("expected high risk to route to verify, got %#v", result.CompletedStages)
	}
	guard := result.CompletedStages[1]
	if guard.Output.Variables["rule"] != "risk_at_least" || guard.Output.Variables["threshold"] != "high" || guard.Output.Variables["value"] != "high" || guard.Output.Variables["target"] != "verify" {
		t.Fatalf("expected persisted named policy decision, got %#v", guard)
	}
	if plannerLLM.calls != 0 {
		t.Fatalf("expected deny branch not to run, got %d planner calls", plannerLLM.calls)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphPolicyGuardCustomRule(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowPolicyRule(t, runtimeHome, "critical-finding", `
name: critical-finding
label: Critical Finding
description: Pass when referenced evidence contains at least high severity.
operator: risk_at_least
defaults:
  ref: stages.audit.outputs.risk
  minimum: high
reason: referenced evidence did not meet high severity
params:
  - name: params.ref
    label: Evidence reference
    type: reference
  - name: params.minimum
    label: Minimum severity
    type: select
    options: [low, medium, high, critical]
`)
	writeWorkflowGraph(t, runtimeHome, "custom-policy", `
name: custom-policy
stages:
  - name: audit
    agent: auditor
    skill: code-audit
    outputs:
      risk: result.output
    next: [guard]
  - name: guard
    node_type: policy_guard
    params:
      rule: critical-finding
    routes:
      allow: verify
      deny: report
  - name: verify
    agent: auditor
    skill: code-audit
  - name: report
    agent: planner
    skill: execution-plan
`)
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "HIGH risk finding in auth."}},
		{Message: schema.Message{Content: "Verification complete."}},
	}}
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "should not report"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	options := runtimeRef.WorkflowRunner().WorkflowOptions()
	var foundCustom bool
	for _, rule := range options.PolicyRules {
		if rule.Name == "critical-finding" && rule.Custom && rule.Source == "custom" && rule.Operator == "risk_at_least" {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Fatalf("expected custom policy rule in workflow options, got %#v", options.PolicyRules)
	}

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "custom-policy", "classify risk", true, nil)
	if err != nil {
		t.Fatalf("Run custom-policy workflow graph: %v", err)
	}
	if len(result.CompletedStages) != 3 || result.CompletedStages[1].Stage != "guard" || result.CompletedStages[2].Stage != "verify" {
		t.Fatalf("expected custom policy to route to verify, got %#v", result.CompletedStages)
	}
	guard := result.CompletedStages[1]
	if guard.Output.Variables["rule"] != "critical-finding" || guard.Output.Variables["threshold"] != "high" || guard.Output.Variables["target"] != "verify" {
		t.Fatalf("expected custom policy metadata, got %#v", guard.Output)
	}
	if plannerLLM.calls != 0 {
		t.Fatalf("expected deny branch not to run, got planner calls=%d", plannerLLM.calls)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphPolicyGuardNamedContainsRule(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "policy-contains", `
name: policy-contains
stages:
  - name: collect
    agent: planner
    skill: execution-plan
    outputs:
      evidence: result.output
    next: [guard]
  - name: guard
    node_type: policy_guard
    params:
      rule: contains
      ref: stages.collect.outputs.evidence
      needle: approved
    routes:
      allow: implement
      deny: explain
  - name: implement
    agent: fixer
    skill: code-writing
  - name: explain
    agent: planner
    skill: execution-plan
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "not ready"}},
		{Message: schema.Message{Content: "explain denial"}},
	}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "should not implement"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "policy-contains", "collect evidence", true, nil)
	if err != nil {
		t.Fatalf("Run policy-contains workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 3 || result.CompletedStages[2].Stage != "explain" {
		t.Fatalf("expected contains rule deny branch, got %#v", result.CompletedStages)
	}
	guard := result.CompletedStages[1]
	if guard.Output.Variables["rule"] != "contains" || guard.Output.Variables["threshold"] != "approved" || guard.Output.Variables["target"] != "explain" {
		t.Fatalf("expected contains policy metadata, got %#v", guard)
	}
	if fixerLLM.calls != 0 {
		t.Fatalf("expected allow branch not to run, got %d fixer calls", fixerLLM.calls)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphInputGatePublishesVariables(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "input-flow", `
name: input-flow
stages:
  - name: collect
    node_type: input_gate
    params:
      target: docs
      severity: high
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      requested_target: stages.collect.outputs.target
      requested_severity: stages.collect.outputs.severity
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan for docs."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "input-flow", "prepare work", true, nil)
	if err != nil {
		t.Fatalf("Run input-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 {
		t.Fatalf("expected input gate plus plan stage, got %#v", result)
	}
	if got := result.CompletedStages[0].Output.Variables["target"]; got != "docs" {
		t.Fatalf("expected input gate target output, got %#v", result.CompletedStages[0].Output)
	}
	prompt := plannerLLM.requests[0].Messages[len(plannerLLM.requests[0].Messages)-1].Content
	if !strings.Contains(prompt, "requested_target") || !strings.Contains(prompt, "docs") || !strings.Contains(prompt, "requested_severity") {
		t.Fatalf("expected input gate variables in downstream prompt, got %q", prompt)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphTeamNodePublishesTemplateContext(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "team-flow", `
name: team-flow
stages:
  - name: team
    node_type: team
    agent: planner
    params:
      team: web-research-team
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      team_name: stages.team.outputs.team
      recommended_workflow: stages.team.outputs.recommended_workflow
      role_names: stages.team.outputs.role_names
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan with web research team."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "team-flow", "prepare web review", true, nil)
	if err != nil {
		t.Fatalf("Run team-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 {
		t.Fatalf("expected team context plus plan stage, got %#v", result)
	}
	team := result.CompletedStages[0]
	if team.NodeType != "team" || team.Agent != "planner" || team.Output.Variables["team"] != "web-research-team" || team.Output.Variables["recommended_workflow"] != "web-research-risk" {
		t.Fatalf("expected persisted team context, got %#v", team)
	}
	if !strings.Contains(team.Output.Variables["role_names"], "asset-collector") || !strings.Contains(team.Output.Variables["blackboard_kinds"], "evidence") {
		t.Fatalf("expected role and blackboard metadata, got %#v", team.Output.Variables)
	}
	prompt := plannerLLM.requests[0].Messages[len(plannerLLM.requests[0].Messages)-1].Content
	if !strings.Contains(prompt, "team_name") || !strings.Contains(prompt, "web-research-team") || !strings.Contains(prompt, "recommended_workflow") {
		t.Fatalf("expected team outputs in downstream prompt, got %q", prompt)
	}
	snapshot := runtimeRef.SessionSnapshot()
	foundTeamBlackboard := false
	for _, entry := range snapshot.Blackboard {
		if entry.Stage == "team" && entry.AgentID == "planner" && entry.Kind == "stage_output" {
			foundTeamBlackboard = true
			break
		}
	}
	if !foundTeamBlackboard {
		t.Fatalf("expected team stage mirrored into collaboration blackboard, got %#v", snapshot.Blackboard)
	}
	teamState := runtimeRef.TeamState(result.RunID, "web-research-team")
	if teamState.Team != "web-research-team" || teamState.Template == nil || teamState.Template.RecommendedWorkflow != "web-research-risk" {
		t.Fatalf("expected derived team state template context, got %#v", teamState)
	}
	if len(teamState.Handoffs) == 0 || len(teamState.UnresolvedItems) == 0 {
		t.Fatalf("expected team handoffs and unresolved blackboard items, got %#v", teamState)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphTeamNodeCanExecuteRoleStages(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "team-exec-flow", `
name: team-exec-flow
stages:
  - name: team
    node_type: team
    agent: planner
    params:
      team: software-task-team
      execute: true
    next: [handoff]
  - name: handoff
    agent: chat
    skill: execution-plan
    input:
      final_handoff: stages.team__reporter.outputs.summary
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Implementation plan ready.\nDecision: split the change into implementation and review.\nQuestion: confirm rollout window."}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Implementation changes ready.\nRisk: tests have not run yet.\n```json\n{\"team_packets\":[{\"kind\":\"decision\",\"content\":\"JSON packet accepted\"},{\"kind\":\"handoff\",\"content\":\"Review needs explicit owner\",\"to_role\":\"reviewer\",\"priority\":\"high\"},{\"kind\":\"escalation\",\"content\":\"Escalate missing test coverage\",\"to_agent\":\"auditor\",\"severity\":\"high\",\"priority\":\"p1\",\"sla_minutes\":30}],\"risks\":[{\"content\":\"JSON risk captured\"}],\"questions\":[\"JSON question captured\"]}\n```"}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Review findings ready.\nCritique: add an edge-case test before release."}}}}
	chatLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Final team handoff ready.\nDecision: ready for user handoff."}},
		{Message: schema.Message{Content: "User handoff complete."}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    chatLLM,
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "team-exec-flow", "ship a small change", true, nil)
	if err != nil {
		t.Fatalf("Run team-exec-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 6 {
		t.Fatalf("expected team context, four role stages, and handoff, got %#v", result.CompletedStages)
	}
	wantStages := []WorkflowStage{"team", "team__planner", "team__implementer", "team__reviewer", "team__reporter", "handoff"}
	for i, want := range wantStages {
		if result.CompletedStages[i].Stage != want {
			t.Fatalf("stage %d = %s, want %s; all stages %#v", i, result.CompletedStages[i].Stage, want, result.CompletedStages)
		}
	}
	if result.CompletedStages[1].Metadata["team_role"] != "planner" || result.CompletedStages[4].Metadata["team_role_final"] != "true" {
		t.Fatalf("expected executable team role metadata, got %#v and %#v", result.CompletedStages[1].Metadata, result.CompletedStages[4].Metadata)
	}
	implementPrompt := fixerLLM.requests[0].Messages[len(fixerLLM.requests[0].Messages)-1].Content
	if !strings.Contains(implementPrompt, "Implementation plan ready") || !strings.Contains(implementPrompt, "team_role: implementer") {
		t.Fatalf("expected planner handoff in implementer prompt, got %q", implementPrompt)
	}
	handoffPrompt := chatLLM.requests[1].Messages[len(chatLLM.requests[1].Messages)-1].Content
	if !strings.Contains(handoffPrompt, "final_handoff") || !strings.Contains(handoffPrompt, "Final team handoff ready") {
		t.Fatalf("expected final team role output in downstream handoff prompt, got %q", handoffPrompt)
	}
	snapshot := runtimeRef.SessionSnapshot()
	foundObservation := false
	foundFinal := false
	foundDecision := false
	foundCritique := false
	foundQuestion := false
	foundJSONDecision := false
	foundJSONRisk := false
	foundJSONHandoff := false
	foundJSONEscalation := false
	for _, message := range snapshot.Messages {
		if message.Kind == "team_observation" && message.Metadata["role"] == "planner" {
			foundObservation = true
		}
		if message.Kind == "team_final_handoff" && message.Metadata["role"] == "reporter" {
			foundFinal = true
		}
		if message.Kind == "team_decision" && strings.Contains(message.Content, "split the change") {
			foundDecision = true
		}
		if message.Kind == "team_critique" && strings.Contains(message.Content, "edge-case") {
			foundCritique = true
		}
		if message.Kind == "team_unresolved_question" && strings.Contains(message.Content, "rollout") {
			foundQuestion = true
		}
		if message.Kind == "team_decision" && strings.Contains(message.Content, "JSON packet accepted") {
			foundJSONDecision = true
		}
		if message.Kind == "team_risk" && strings.Contains(message.Content, "JSON risk captured") {
			foundJSONRisk = true
		}
		if message.Kind == "team_handoff" && strings.Contains(message.Content, "Review needs explicit owner") && message.ToAgent == "auditor" {
			foundJSONHandoff = true
		}
		if message.Kind == "team_escalation" && strings.Contains(message.Content, "Escalate missing test coverage") && message.ToAgent == "auditor" && message.Metadata["severity"] == "high" && message.Metadata["priority"] == "p1" && message.Metadata["sla_minutes"] == "30" && message.Metadata["due_at"] != "" {
			foundJSONEscalation = true
		}
	}
	if !foundObservation || !foundFinal || !foundDecision || !foundCritique || !foundQuestion || !foundJSONDecision || !foundJSONRisk || !foundJSONHandoff || !foundJSONEscalation {
		t.Fatalf("expected team observation/final/packet messages, got %#v", snapshot.Messages)
	}
	teamState := runtimeRef.TeamState(result.RunID, "software-task-team")
	if len(teamState.Blackboard) == 0 || len(teamState.Handoffs) == 0 {
		t.Fatalf("expected executable team state to include blackboard and handoffs, got %#v", teamState)
	}
	if len(teamState.Decisions) < 2 || len(teamState.Critiques) == 0 || len(teamState.Questions) == 0 || len(teamState.Risks) == 0 {
		t.Fatalf("expected structured team packets in team state, got %#v", teamState)
	}
	if len(teamState.UnresolvedItems) == 0 {
		t.Fatalf("expected unresolved team items, got %#v", teamState)
	}
	if len(teamState.Assignments) == 0 || len(teamState.Escalations) == 0 || teamState.ActiveOwner != "auditor" {
		t.Fatalf("expected assignment/escalation team state, got %#v", teamState)
	}
}

func TestWorkflowRunnerExecutableTeamRoutesByRoleHandoffPacket(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "team-conditional-flow", `
name: team-conditional-flow
stages:
  - name: team
    node_type: team
    agent: planner
    params:
      team: software-task-team
      execute: true
    next: [handoff]
  - name: handoff
    agent: chat
    skill: execution-plan
    input:
      final_handoff: stages.team__reporter.outputs.summary
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan can skip implementation.\n```json\n{\"team_packets\":[{\"kind\":\"handoff\",\"content\":\"skip directly to review\",\"to_role\":\"reviewer\",\"priority\":\"high\"}]}\n```"}}}}
	fixerLLM := &workflowRecordingLLMClient{}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Review accepted the planner-only handoff."}}}}
	chatLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Reporter summarized conditional handoff."}},
		{Message: schema.Message{Content: "User handoff complete."}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    chatLLM,
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "team-conditional-flow", "review a planning-only change", true, nil)
	if err != nil {
		t.Fatalf("Run team-conditional-flow workflow graph: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed workflow, got %#v", result)
	}
	wantStages := []WorkflowStage{"team", "team__planner", "team__reviewer", "team__reporter", "handoff"}
	if len(result.CompletedStages) != len(wantStages) {
		t.Fatalf("expected planner -> reviewer -> reporter -> handoff, got %#v", result.CompletedStages)
	}
	for i, want := range wantStages {
		if result.CompletedStages[i].Stage != want {
			t.Fatalf("stage %d = %s, want %s; all stages %#v", i, result.CompletedStages[i].Stage, want, result.CompletedStages)
		}
	}
	if len(fixerLLM.requests) != 0 {
		t.Fatalf("expected implementer role to be skipped by handoff packet, got %d fixer requests", len(fixerLLM.requests))
	}
	if len(auditorLLM.requests) != 1 {
		t.Fatalf("expected reviewer role to run once, got %d requests", len(auditorLLM.requests))
	}
	reviewerPrompt := auditorLLM.requests[0].Messages[len(auditorLLM.requests[0].Messages)-1].Content
	if !strings.Contains(reviewerPrompt, "team_role: reviewer") || !strings.Contains(reviewerPrompt, "skip directly to review") {
		t.Fatalf("expected reviewer prompt to include routed handoff context, got %q", reviewerPrompt)
	}
}

func TestWorkflowRunnerExecutableTeamAppliesEscalationPolicyThreshold(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "team-escalation-policy-flow", `
name: team-escalation-policy-flow
stages:
  - name: team
    node_type: team
    agent: planner
    params:
      team: software-task-team
      execute: true
      escalate_severity: high
      escalate_to: auditor
      escalation_sla_minutes: 15
    next: [handoff]
  - name: handoff
    agent: chat
    skill: execution-plan
    input:
      final_handoff: stages.team__reporter.outputs.summary
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan found a risk.\n```json\n{\"risks\":[{\"content\":\"Critical auth bypass risk\",\"severity\":\"high\",\"priority\":\"p2\"}]}\n```"}}}}
	fixerLLM := &workflowRecordingLLMClient{}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Reviewer handled the escalated risk."}}}}
	chatLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Reporter summarized escalation."}},
		{Message: schema.Message{Content: "User handoff complete."}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    chatLLM,
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "team-escalation-policy-flow", "review a risky change", true, nil)
	if err != nil {
		t.Fatalf("Run team-escalation-policy-flow workflow graph: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed workflow, got %#v", result)
	}
	wantStages := []WorkflowStage{"team", "team__planner", "team__reviewer", "team__reporter", "handoff"}
	if len(result.CompletedStages) != len(wantStages) {
		t.Fatalf("expected planner -> reviewer -> reporter -> handoff, got %#v", result.CompletedStages)
	}
	for i, want := range wantStages {
		if result.CompletedStages[i].Stage != want {
			t.Fatalf("stage %d = %s, want %s; all stages %#v", i, result.CompletedStages[i].Stage, want, result.CompletedStages)
		}
	}
	if result.CompletedStages[1].Metadata["escalate_severity"] != "high" || result.CompletedStages[1].Metadata["escalation_sla_minutes"] != "15" {
		t.Fatalf("expected inherited escalation policy metadata, got %#v", result.CompletedStages[1].Metadata)
	}
	if len(fixerLLM.requests) != 0 {
		t.Fatalf("expected implementer role to be skipped by escalation policy route, got %d fixer requests", len(fixerLLM.requests))
	}
	if len(auditorLLM.requests) != 1 {
		t.Fatalf("expected reviewer role to run once, got %d requests", len(auditorLLM.requests))
	}
	reviewerPrompt := auditorLLM.requests[0].Messages[len(auditorLLM.requests[0].Messages)-1].Content
	if !strings.Contains(reviewerPrompt, "team_role: reviewer") || !strings.Contains(reviewerPrompt, "Critical auth bypass risk") {
		t.Fatalf("expected reviewer prompt to include escalated risk context, got %q", reviewerPrompt)
	}

	snapshot := runtimeRef.SessionSnapshot()
	foundEscalation := false
	for _, message := range snapshot.Messages {
		if message.Kind == "team_escalation" &&
			strings.Contains(message.Content, "Critical auth bypass risk") &&
			message.ToAgent == "auditor" &&
			message.Metadata["escalation_policy"] == "true" &&
			message.Metadata["escalation_reason"] == "severity:high" &&
			message.Metadata["severity"] == "high" &&
			message.Metadata["priority"] == "p2" &&
			message.Metadata["sla_minutes"] == "15" &&
			message.Metadata["due_at"] != "" {
			foundEscalation = true
			break
		}
	}
	if !foundEscalation {
		t.Fatalf("expected policy escalation message, got %#v", snapshot.Messages)
	}
	teamState := runtimeRef.TeamState(result.RunID, "software-task-team")
	if len(teamState.Escalations) == 0 || teamState.ActiveOwner != "auditor" {
		t.Fatalf("expected policy escalation in team state, got %#v", teamState)
	}
}

func TestWorkflowRunnerExecutableTeamRecordsApprovalGateState(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "team-approval-gate-flow", `
name: team-approval-gate-flow
stages:
  - name: team
    node_type: team
    agent: planner
    params:
      team: software-task-team
      execute: true
      approval_quorum: 2
      approval_roles: planner,implementer,reviewer
      reject_blocks: true
    next: [handoff]
  - name: handoff
    agent: chat
    skill: execution-plan
    input:
      final_handoff: stages.team__reporter.outputs.summary
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan ready.\nApproval: planner approves the scoped plan."}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Implementation done.\nRejection: implementation is blocked until test evidence exists."}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Review complete.\nApproval: reviewer approves after checking the change."}}}}
	chatLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Reporter summarized the gate state."}},
		{Message: schema.Message{Content: "User handoff complete."}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    chatLLM,
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "team-approval-gate-flow", "ship with review gate", true, nil)
	if err != nil {
		t.Fatalf("Run team-approval-gate-flow workflow graph: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed workflow, got %#v", result)
	}
	if result.CompletedStages[1].Metadata["approval_quorum"] != "2" || result.CompletedStages[1].Metadata["approval_roles"] != "planner,implementer,reviewer" {
		t.Fatalf("expected inherited approval policy metadata, got %#v", result.CompletedStages[1].Metadata)
	}

	teamState := runtimeRef.TeamState(result.RunID, "software-task-team")
	if len(teamState.Approvals) != 2 || len(teamState.Rejections) != 1 {
		t.Fatalf("expected approval/rejection packets in team state, got %#v", teamState)
	}
	if teamState.ApprovalGate == nil {
		t.Fatalf("expected approval gate state, got %#v", teamState)
	}
	if teamState.ApprovalGate.Required != 2 ||
		teamState.ApprovalGate.Approved != 2 ||
		teamState.ApprovalGate.Rejected != 1 ||
		teamState.ApprovalGate.Pending != 0 ||
		teamState.ApprovalGate.Status != "blocked" {
		t.Fatalf("unexpected approval gate state: %#v", teamState.ApprovalGate)
	}
	if !workflowStringSliceContains(teamState.ApprovalGate.Approvers, "planner") || !workflowStringSliceContains(teamState.ApprovalGate.Approvers, "reviewer") || !workflowStringSliceContains(teamState.ApprovalGate.Rejectors, "implementer") {
		t.Fatalf("expected gate approvers/rejectors by role, got %#v", teamState.ApprovalGate)
	}
}

func TestWorkflowRunnerPolicyGuardCanUseTeamApprovalGate(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "team-approval-policy-flow", `
name: team-approval-policy-flow
stages:
  - name: team
    node_type: team
    agent: planner
    params:
      team: software-task-team
      execute: true
      approval_preset: software-review
    next: [review_gate]
  - name: review_gate
    node_type: policy_guard
    params:
      rule: team_approval_gate
      team: software-task-team
      approval_preset: software-review
      status: passed
    routes:
      allow: handoff
  - name: handoff
    agent: chat
    skill: execution-plan
    input:
      gate_status: stages.review_gate.outputs.value
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan ready.\nApproval: planner approves the plan."}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Implementation done."}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Review complete.\nApproval: reviewer approves the change."}}}}
	chatLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Reporter summarized approvals."}},
		{Message: schema.Message{Content: "User handoff complete."}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    chatLLM,
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "team-approval-policy-flow", "ship with a policy gate", true, nil)
	if err != nil {
		t.Fatalf("Run team-approval-policy-flow workflow graph: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("expected completed workflow, got %#v", result)
	}
	if len(result.CompletedStages) != 7 {
		t.Fatalf("expected team, four roles, gate, and handoff, got %#v", result.CompletedStages)
	}
	roleMetadata := result.CompletedStages[1].Metadata
	if roleMetadata["approval_preset"] != "software-review" || roleMetadata["approval_quorum"] != "2" || roleMetadata["approval_roles"] != "planner,reviewer" || roleMetadata["reject_blocks"] != "true" {
		t.Fatalf("expected team role metadata to inherit software-review preset, got %#v", roleMetadata)
	}
	gate := result.CompletedStages[5]
	if gate.Stage != "review_gate" || gate.NodeType != "policy_guard" {
		t.Fatalf("expected review_gate policy stage, got %#v", gate)
	}
	if gate.Output.Variables["route"] != "allow" ||
		gate.Output.Variables["rule"] != "team_approval_gate" ||
		gate.Output.Variables["value"] != "passed" ||
		gate.Output.Variables["threshold"] != "passed" ||
		gate.Output.Variables["approved"] != "2" ||
		gate.Output.Variables["required"] != "2" {
		t.Fatalf("unexpected approval gate policy variables: %#v", gate.Output.Variables)
	}
	handoffPrompt := chatLLM.requests[1].Messages[len(chatLLM.requests[1].Messages)-1].Content
	if !strings.Contains(handoffPrompt, "gate_status") || !strings.Contains(handoffPrompt, "passed") {
		t.Fatalf("expected handoff prompt to receive approval gate status, got %q", handoffPrompt)
	}
}

func TestWorkflowRunnerPolicyGuardTeamApprovalGateCanPauseForManualQuorum(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "team-approval-manual-flow", `
name: team-approval-manual-flow
stages:
  - name: team
    node_type: team
    agent: planner
    params:
      team: software-task-team
      execute: true
      approval_quorum: 2
      approval_roles: planner,reviewer
    next: [review_gate]
  - name: review_gate
    node_type: policy_guard
    params:
      rule: team_approval_gate
      team: software-task-team
      status: passed
      wait_for_quorum: true
    routes:
      allow: handoff
  - name: handoff
    agent: chat
    skill: execution-plan
    input:
      gate_status: stages.review_gate.outputs.value
      approvers: stages.review_gate.outputs.approvers
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan ready.\nApproval: planner approves the plan."}}}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Implementation done."}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Review complete without explicit approval."}}}}
	chatLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Reporter summarized pending review."}},
		{Message: schema.Message{Content: "Manual quorum handoff complete."}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    chatLLM,
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	first, err := runtimeRef.WorkflowRunner().Run(context.Background(), "team-approval-manual-flow", "ship with manual quorum", true, nil)
	if err != nil {
		t.Fatalf("Run team-approval-manual-flow workflow graph: %v", err)
	}
	if first.Status != "awaiting_input" || !first.PendingInput || first.NextStage != "review_gate" || first.RunID == "" {
		t.Fatalf("expected policy gate to pause for manual quorum, got %#v", first)
	}
	if len(first.PendingFields) != 3 || first.PendingFields[0].Name != "roles" || !first.PendingFields[0].Multiple || !workflowStringSliceContains(first.PendingFields[0].Options, "reviewer") {
		t.Fatalf("expected manual approval input fields with pending reviewer role, got %#v", first.PendingFields)
	}

	resumed, err := runtimeRef.WorkflowRunner().ResumeInput(context.Background(), first.RunID, map[string]string{
		"roles":    "reviewer",
		"decision": "approve",
		"comment":  "operator approved reviewer quorum",
	}, nil)
	if err != nil {
		t.Fatalf("ResumeInput team-approval-manual-flow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != first.RunID {
		t.Fatalf("expected completed workflow after manual quorum approval, got %#v", resumed)
	}
	gate := resumed.CompletedStages[len(resumed.CompletedStages)-2]
	if gate.Stage != "review_gate" || gate.Output.Variables["value"] != "passed" || gate.Output.Variables["approved"] != "2" {
		t.Fatalf("expected review gate to pass after manual approval, got %#v", gate)
	}
	foundManualApproval := false
	for _, stage := range resumed.CompletedStages {
		if stage.Metadata["team_manual_gate"] == "review_gate" && stage.Metadata["team_role"] == "reviewer" && strings.Contains(stage.Result.Output, "Approval:") {
			foundManualApproval = true
			break
		}
	}
	if !foundManualApproval {
		t.Fatalf("expected manual approval stage before review gate, got %#v", resumed.CompletedStages)
	}
	teamState := runtimeRef.TeamState(resumed.RunID, "software-task-team")
	if teamState.ApprovalGate == nil || teamState.ApprovalGate.Status != "passed" || !workflowStringSliceContains(teamState.ApprovalGate.Approvers, "reviewer") {
		t.Fatalf("expected team state approval gate to include manual reviewer approval, got %#v", teamState.ApprovalGate)
	}
}

func TestWorkflowTemplateCatalogIncludesSoftwareTeamReviewGate(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	template, ok := runtimeRef.WorkflowRunner().WorkflowTemplate("software-team-review-gate")
	if !ok {
		t.Fatal("expected software-team-review-gate template")
	}
	if template.Graph.Name != "software-team-review-gate" || len(template.Graph.Stages) != 6 {
		t.Fatalf("unexpected team review gate template: %#v", template)
	}
	foundTeam := false
	foundGate := false
	for _, stage := range template.Graph.Stages {
		if stage.Name == "team" && stage.NodeType == "team" && stage.Params["execute"] == "true" && stage.Params["approval_preset"] == "software-review" {
			foundTeam = true
		}
		if stage.Name == "review-gate" && stage.NodeType == "policy_guard" && stage.Params["rule"] == "team_approval_gate" && stage.Routes["allow"] == "handoff" && stage.Routes["deny"] == "revise" {
			foundGate = true
		}
	}
	if !foundTeam || !foundGate {
		t.Fatalf("expected executable team and team approval gate stages, got %#v", template.Graph.Stages)
	}
	rendered, err := RenderWorkflowTemplateYAML("team-review", "software-team-review-gate")
	if err != nil {
		t.Fatalf("RenderWorkflowTemplateYAML: %v", err)
	}
	if !strings.Contains(rendered, "team_approval_gate") || !strings.Contains(rendered, "software-task-team") {
		t.Fatalf("expected rendered template to include team gate details, got %q", rendered)
	}
}

func TestWorkflowTemplateCatalogIncludesTaskDecompositionPlan(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	template, ok := runtimeRef.WorkflowRunner().WorkflowTemplate("task-decomposition-plan")
	if !ok {
		t.Fatal("expected task-decomposition-plan template")
	}
	if template.Category != "planning" || template.Graph.Name != "task-decomposition-plan" || len(template.Graph.Stages) != 8 {
		t.Fatalf("unexpected task decomposition template: %#v", template)
	}
	foundDecompose := false
	foundQuality := false
	foundDraft := false
	for _, stage := range template.Graph.Stages {
		if stage.Name == "decompose" &&
			stage.Agent == "planner" &&
			stage.Params["output_contract"] != "" &&
			len(stage.AcceptanceCriteria) >= 4 &&
			len(stage.Artifacts) == 1 {
			foundDecompose = true
		}
		if stage.Name == "quality" &&
			stage.NodeType == "quality_gate" &&
			stage.Params["require_acceptance"] == "true" &&
			stage.Params["require_evidence"] == "true" &&
			stage.Routes["fail"] == "clarify-scope" {
			foundQuality = true
		}
		if stage.Name == "materialize-candidate" &&
			stage.Outputs["workflow_draft"] == "result.output" &&
			len(stage.AcceptanceCriteria) >= 3 {
			foundDraft = true
		}
	}
	if !foundDecompose || !foundQuality || !foundDraft {
		t.Fatalf("expected decomposition, quality gate, and workflow draft stages, got %#v", template.Graph.Stages)
	}
	rendered, err := RenderWorkflowTemplateYAML("complex-task-plan", "task-decomposition-plan")
	if err != nil {
		t.Fatalf("RenderWorkflowTemplateYAML: %v", err)
	}
	for _, want := range []string{"Plan Graph Candidate", "quality_gate", "acceptance_criteria", "workflow-draft", "fields_json"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered template to contain %q, got %q", want, rendered)
		}
	}
}

func TestWorkflowTemplateCatalogIncludesMultiDomainParallelRouter(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	runner := runtimeRef.WorkflowRunner()
	template, ok := runner.WorkflowTemplate("multi-domain-intake-router")
	if !ok {
		t.Fatal("expected multi-domain-intake-router template")
	}
	if template.Category != "starter" || template.Graph.Name != "multi-domain-intake-router" || len(template.Graph.Stages) != 21 {
		t.Fatalf("unexpected multi-domain parallel router template: %#v", template)
	}
	validation := runner.ValidateWorkflowGraphDocument("multi-domain-intake-router", template.Graph)
	if !validation.Valid {
		t.Fatalf("expected valid multi-domain parallel router graph, got %#v", validation)
	}
	for _, stageName := range []string{"intake", "decompose", "dispatch", "software-worker", "web-security-worker", "ops-worker", "platform-worker", "join", "aggregate", "audit", "quality", "final-report"} {
		if !workflowGraphHasStage(template.Graph, stageName) {
			t.Fatalf("expected multi-domain parallel router template to include stage %q, got %#v", stageName, template.Graph.Stages)
		}
	}
	foundStrongDecompose := false
	foundSelectedDispatch := false
	foundDynamicJoin := false
	foundCheapWorker := false
	foundAudit := false
	for _, stage := range template.Graph.Stages {
		if stage.Name == "decompose" && stage.Model.Provider == "primary" && stage.Outputs["active_branches"] == "result.active_branches" && strings.Contains(stage.Params["output_contract"], "active_branches") {
			foundStrongDecompose = true
		}
		if stage.Name == "dispatch" && stage.NodeType == "parallel" && stage.Params["active_branches_ref"] == "stages.decompose.outputs.active_branches" && len(stage.Next) == 9 {
			foundSelectedDispatch = true
		}
		if stage.Name == "join" && stage.NodeType == "join" && stage.Params["wait_for_ref"] == "stages.dispatch.outputs.branches" {
			foundDynamicJoin = true
		}
		if strings.HasSuffix(stage.Name, "-worker") && stage.Model.Provider == "backup" && stage.Context.MaxTokens > 0 {
			foundCheapWorker = true
		}
		if stage.Name == "audit" && stage.Model.Provider == "primary" && strings.Contains(stage.Params["output_contract"], "Token Discipline") {
			foundAudit = true
		}
	}
	if !foundStrongDecompose || !foundSelectedDispatch || !foundDynamicJoin || !foundCheapWorker || !foundAudit {
		t.Fatalf("expected decomposition, selected parallel dispatch, dynamic join, cheap workers, and audit, got %#v", template.Graph.Stages)
	}
	rendered, err := RenderWorkflowTemplateYAML("multi-domain-copy", "multi-domain-intake-router")
	if err != nil {
		t.Fatalf("RenderWorkflowTemplateYAML: %v", err)
	}
	for _, want := range []string{"active_branches_ref", "wait_for_ref", "Token Discipline", "domain_slice_v1", "provider: primary", "provider: backup"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered multi-domain parallel router template to contain %q, got %q", want, rendered)
		}
	}
}

func TestWorkflowTemplateCatalogIncludesComplexProjectDelivery(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	runner := runtimeRef.WorkflowRunner()
	template, ok := runner.WorkflowTemplate("complex-project-delivery")
	if !ok {
		t.Fatal("expected complex-project-delivery template")
	}
	if template.Category != "software" || template.Graph.Name != "complex-project-delivery" || len(template.Graph.Stages) != 13 {
		t.Fatalf("unexpected complex project delivery template: %#v", template)
	}
	validation := runner.ValidateWorkflowGraphDocument("complex-project-delivery", template.Graph)
	if !validation.Valid {
		t.Fatalf("expected valid complex project delivery graph, got %#v", validation)
	}
	requiredStages := []string{"intake", "plan", "confirm-plan", "delivery-loop", "iteration", "loop-quality", "final-validation", "final-quality", "final-report"}
	for _, stageName := range requiredStages {
		if !workflowGraphHasStage(template.Graph, stageName) {
			t.Fatalf("expected complex project delivery template to include stage %q, got %#v", stageName, template.Graph.Stages)
		}
	}
	foundConfirmation := false
	foundLoop := false
	foundIteration := false
	foundFinalValidation := false
	foundFinalQuality := false
	foundFinalReport := false
	for _, stage := range template.Graph.Stages {
		if stage.Name == "confirm-plan" && stage.NodeType == "checkpoint" && strings.Contains(stage.Params["prompt"], "Approve") {
			foundConfirmation = true
		}
		if stage.Name == "delivery-loop" && stage.NodeType == "loop" && stage.Params["stage"] == "iteration" && strings.Contains(stage.Params["until"], "PROJECT_COMPLETE") {
			foundLoop = true
		}
		if stage.Name == "iteration" && stage.Agent == "fixer" && stage.Approval && strings.Contains(stage.Params["output_contract"], "Plan Update") && len(stage.AcceptanceCriteria) >= 4 {
			foundIteration = true
		}
		if stage.Name == "final-validation" && stage.Agent == "auditor" && len(stage.Artifacts) == 1 && len(stage.Context.Include) >= 6 {
			foundFinalValidation = true
		}
		if stage.Name == "final-quality" && stage.NodeType == "quality_gate" && stage.Routes["pass"] == "final-report" && stage.Routes["fail"] == "final-report" {
			foundFinalQuality = true
		}
		if stage.Name == "final-report" && stage.Outputs["final_report"] == "result.output" && len(stage.AcceptanceCriteria) >= 3 {
			foundFinalReport = true
		}
	}
	if !foundConfirmation || !foundLoop || !foundIteration || !foundFinalValidation || !foundFinalQuality || !foundFinalReport {
		t.Fatalf("expected confirmation, loop, iteration, final validation, final quality, and final report stages, got %#v", template.Graph.Stages)
	}
	rendered, err := RenderWorkflowTemplateYAML("complex-delivery-copy", "complex-project-delivery")
	if err != nil {
		t.Fatalf("RenderWorkflowTemplateYAML: %v", err)
	}
	for _, want := range []string{"PROJECT_COMPLETE", "delivery-loop", "final-validation-report", "completion-report", "confirm-plan"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered complex project delivery template to contain %q, got %q", want, rendered)
		}
	}
}

func TestWorkflowTemplateCatalogIncludesEngineeringParallelDelivery(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	runner := runtimeRef.WorkflowRunner()
	template, ok := runner.WorkflowTemplate("engineering-parallel-delivery")
	if !ok {
		t.Fatal("expected engineering-parallel-delivery template")
	}
	if template.Category != "software" || template.Graph.Name != "engineering-parallel-delivery" || len(template.Graph.Stages) != 13 {
		t.Fatalf("unexpected engineering parallel delivery template: %#v", template)
	}
	validation := runner.ValidateWorkflowGraphDocument("engineering-parallel-delivery", template.Graph)
	if !validation.Valid {
		t.Fatalf("expected valid engineering parallel delivery graph, got %#v", validation)
	}
	requiredStages := []string{"decompose", "dispatch", "worker-core", "worker-tests", "worker-docs", "join", "aggregate", "audit", "quality", "resolve", "final-report"}
	for _, stageName := range requiredStages {
		if !workflowGraphHasStage(template.Graph, stageName) {
			t.Fatalf("expected engineering parallel delivery template to include stage %q, got %#v", stageName, template.Graph.Stages)
		}
	}
	foundStrongPlan := false
	foundCheapWorker := false
	foundParallel := false
	foundJoin := false
	foundResolve := false
	foundTokenContext := false
	for _, stage := range template.Graph.Stages {
		if stage.Name == "decompose" && stage.Model.Provider == "primary" && stage.Model.MaxTokens > 0 && strings.Contains(stage.Params["output_contract"], "Work Slices") {
			foundStrongPlan = true
		}
		if strings.HasPrefix(stage.Name, "worker-") && stage.Model.Provider == "backup" && stage.Model.MaxTokens > 0 {
			foundCheapWorker = true
		}
		if stage.Name == "dispatch" && stage.NodeType == "parallel" && len(stage.Next) == 3 {
			foundParallel = true
		}
		if stage.Name == "join" && stage.NodeType == "join" && strings.Contains(stage.Params["wait_for"], "worker-core") {
			foundJoin = true
		}
		if stage.Name == "resolve" && stage.Model.Provider == "primary" && stage.Model.MaxTokens > 0 && strings.Contains(stage.Params["output_contract"], "Resolution Plan") {
			foundResolve = true
		}
		if stage.Context.MaxTokens > 0 && len(stage.Context.Exclude) > 0 {
			foundTokenContext = true
		}
	}
	if !foundStrongPlan || !foundCheapWorker || !foundParallel || !foundJoin || !foundResolve || !foundTokenContext {
		t.Fatalf("expected model routing, parallel/join, and bounded context stages, got %#v", template.Graph.Stages)
	}
	rendered, err := RenderWorkflowTemplateYAML("parallel-delivery-copy", "engineering-parallel-delivery")
	if err != nil {
		t.Fatalf("RenderWorkflowTemplateYAML: %v", err)
	}
	for _, want := range []string{"provider: primary", "provider: backup", "parallel", "join", "Resolution Plan", "Token Discipline"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered engineering parallel delivery template to contain %q, got %q", want, rendered)
		}
	}
}

func TestWorkflowTemplateCatalogIncludesPlanImplementAudit(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	runner := runtimeRef.WorkflowRunner()
	template, ok := runner.WorkflowTemplate("plan-implement-audit")
	if !ok {
		t.Fatal("expected plan-implement-audit template")
	}
	if template.Category != "software" || template.Graph.Name != "plan-implement-audit" || len(template.Graph.Stages) != 11 {
		t.Fatalf("unexpected plan implement audit template: %#v", template)
	}
	validation := runner.ValidateWorkflowGraphDocument("plan-implement-audit", template.Graph)
	if !validation.Valid {
		t.Fatalf("expected valid plan implement audit graph, got %#v", validation)
	}
	for _, stageName := range []string{"clarify", "plan", "implement", "verify", "audit", "quality", "final-report"} {
		if !workflowGraphHasStage(template.Graph, stageName) {
			t.Fatalf("expected plan implement audit template to include stage %q, got %#v", stageName, template.Graph.Stages)
		}
	}
	rendered, err := RenderWorkflowTemplateYAML("plan-implement-copy", "plan-implement-audit")
	if err != nil {
		t.Fatalf("RenderWorkflowTemplateYAML: %v", err)
	}
	for _, want := range []string{"Verification Evidence", "quality_gate", "final-report", "revision-summary"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered plan implement audit template to contain %q, got %q", want, rendered)
		}
	}
}

func TestWorkflowRunnerBuiltInPlanFixAuditCompletesFinalHandoff(t *testing.T) {
	runtimeHome := t.TempDir()
	rendered, err := RenderWorkflowTemplateYAML("plan-fix-audit", "plan-fix-audit")
	if err != nil {
		t.Fatalf("RenderWorkflowTemplateYAML: %v", err)
	}
	writeWorkflowGraphYAML(t, runtimeHome, "plan-fix-audit", rendered)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Goal: ship fix\nAffected Files: README.md\nConstraints: preserve user edits\nAcceptance Checks: go test ./...\nRisks: low\nOpen Questions: none"}},
		{Message: schema.Message{Content: "Implementation Steps: update docs\nTarget Files: README.md\nVerification Commands: go test ./...\nRollback Notes: revert patch\nHandoff: implement exactly this"}},
		{Message: schema.Message{Content: "Final Status: complete\nDelivered Changes: fixed docs\nVerification Evidence: go test ./... passed\nAudit Summary: no findings\nRemaining Risks: none\nNext Steps: release"}},
	}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Change Summary: updated docs\nFiles Changed: README.md\nVerification Notes: go test ./... passed\nResidual Risk: none"}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Verification Result: passed\nCommands Run: go test ./...\nFailures: none\nCoverage: package tests\nResidual Risk: low"}},
		{Message: schema.Message{Content: "Findings: none\nSeverity Order: none\nMissed Requirements: none\nTest Gaps: none\nSafety Notes: safe\nRecommendation: ship"}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "plan-fix-audit", "ship a documented fix", true, nil)
	if err != nil {
		t.Fatalf("Run plan-fix-audit: %v", err)
	}
	if result.Status != "completed" || !workflowStageResultNamesContain(result.CompletedStages, "quality") || !workflowStageResultNamesContain(result.CompletedStages, "final-handoff") {
		t.Fatalf("expected completed workflow through quality and final handoff, got %#v", result.CompletedStages)
	}
	quality := workflowGraphStageResultByName(t, result.CompletedStages, "quality")
	if quality.Output.Variables["route"] != "pass" || quality.Output.Variables["quality_status"] != "passed" {
		t.Fatalf("expected quality gate to pass, got %#v", quality.Output.Variables)
	}
	final := workflowGraphStageResultByName(t, result.CompletedStages, "final-handoff")
	if !strings.Contains(final.Result.Output, "Final Status") || !strings.Contains(final.Result.Output, "Verification Evidence") {
		t.Fatalf("expected final handoff with status and evidence, got %q", final.Result.Output)
	}
}

func TestWorkflowRunnerBuiltInPlanImplementAuditCompletesFinalReport(t *testing.T) {
	runtimeHome := t.TempDir()
	rendered, err := RenderWorkflowTemplateYAML("plan-implement-audit", "plan-implement-audit")
	if err != nil {
		t.Fatalf("RenderWorkflowTemplateYAML: %v", err)
	}
	writeWorkflowGraphYAML(t, runtimeHome, "plan-implement-audit", rendered)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Objective: ship bounded change\nScope: small\nConstraints: preserve user edits\nTarget Areas: docs\nAcceptance Criteria: user can see result\nRisks: low\nAssumptions: tests available"}},
		{Message: schema.Message{Content: "Implementation Plan: update docs\nFiles Or Modules: README.md\nChange Steps: edit file\nVerification Commands: go test ./...\nReview Checklist: acceptance\nRollback Plan: revert patch\nDone Criteria: checks pass"}},
		{Message: schema.Message{Content: "Final Status: complete\nDelivered Work: bounded change\nFiles Changed: README.md\nVerification Evidence: go test ./... passed\nAudit Outcome: accepted\nRemaining Risks: none\nNext Steps: release"}},
	}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Implementation Summary: updated docs\nFiles Changed: README.md\nVerification Attempted: go test ./...\nDeviations: none\nRemaining Work: none\nRisk Notes: low"}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Verification Result: passed\nCommands Run: go test ./...\nCoverage: package tests\nFailures: none\nEvidence: all checks passed\nResidual Risk: low"}},
		{Message: schema.Message{Content: "Audit Decision: accept\nFindings: none\nMissed Requirements: none\nTest Gaps: none\nSafety Notes: safe\nRecommendation: ship\nRelease Readiness: ready"}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "plan-implement-audit", "ship bounded change", true, nil)
	if err != nil {
		t.Fatalf("Run plan-implement-audit: %v", err)
	}
	if result.Status != "completed" || !workflowStageResultNamesContain(result.CompletedStages, "quality") || !workflowStageResultNamesContain(result.CompletedStages, "final-report") {
		t.Fatalf("expected completed workflow through quality and final report, got %#v", result.CompletedStages)
	}
	quality := workflowGraphStageResultByName(t, result.CompletedStages, "quality")
	if quality.Output.Variables["route"] != "pass" || quality.Output.Variables["quality_status"] != "passed" {
		t.Fatalf("expected quality gate to pass, got %#v", quality.Output.Variables)
	}
	final := workflowGraphStageResultByName(t, result.CompletedStages, "final-report")
	if !strings.Contains(final.Result.Output, "Final Status") || !strings.Contains(final.Result.Output, "Verification Evidence") {
		t.Fatalf("expected final report with status and evidence, got %q", final.Result.Output)
	}
}

func TestWorkflowRunnerComplexProjectDeliveryCompletesAfterConfirmationAndFinalQuality(t *testing.T) {
	runtimeHome := t.TempDir()
	rendered, err := RenderWorkflowTemplateYAML("complex-project-delivery", "complex-project-delivery")
	if err != nil {
		t.Fatalf("RenderWorkflowTemplateYAML: %v", err)
	}
	writeWorkflowGraphYAML(t, runtimeHome, "complex-project-delivery", rendered)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "User Goal: deliver project\nProblem Context: repo\nFunctional Requirements: feature A\nNon-Functional Requirements: stable\nConstraints: scoped\nUnknowns: none\nAcceptance Criteria: feature works\nRisks: low\nClarifying Questions: none"}},
		{Message: schema.Message{Content: "Project Plan: deliver in slices\nMilestones: one\nWork Slices: slice A\nDependencies: none\nTarget Files: README.md\nVerification Strategy: go test ./...\nReview Strategy: audit\nStop Conditions: PROJECT_COMPLETE\nUser Checkpoints: confirm plan\nDefinition of Done: requirements delivered"}},
		{Message: schema.Message{Content: "Completion Summary: complete\nRequirements Delivered: feature A\nFiles Changed: README.md\nVerification Evidence: go test ./... passed\nReview Notes: final quality passed\nResidual Risks: none\nUser Follow-Up: none\nArtifact References: completion-report"}},
	}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Iteration Number: 1\nSelected Work Slice: slice A\nImplementation Summary: implemented feature A\nFiles Changed: README.md\nVerification Performed: go test ./... passed\nReview Findings: none\nPlan Update: all work complete\nRemaining Work: none\nCompletion Decision: PROJECT_COMPLETE\nNext Action: final validation"}}}}
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Final Validation Result: passed\nFull Test Pass: yes\nCommands Run: go test ./...\nRequirements Coverage: feature A covered\nFailures: none\nResidual Risks: low\nRelease Recommendation: release"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "complex-project-delivery", "build the project", true, nil)
	if err != nil {
		t.Fatalf("Run complex-project-delivery: %v", err)
	}
	for _, stage := range []string{"confirm-plan", "delivery-loop", "loop-quality", "final-validation", "final-quality", "final-report"} {
		if !workflowStageResultNamesContain(result.CompletedStages, stage) {
			t.Fatalf("expected completed stage %s in %#v", stage, result.CompletedStages)
		}
	}
	loopQuality := workflowGraphStageResultByName(t, result.CompletedStages, "loop-quality")
	if loopQuality.Output.Variables["route"] != "pass" || loopQuality.Output.Variables["quality_status"] != "passed" {
		t.Fatalf("expected loop quality to pass, got %#v", loopQuality.Output.Variables)
	}
	finalQuality := workflowGraphStageResultByName(t, result.CompletedStages, "final-quality")
	if finalQuality.Output.Variables["route"] != "pass" || finalQuality.Output.Variables["quality_status"] != "passed" {
		t.Fatalf("expected final quality to pass, got %#v", finalQuality.Output.Variables)
	}
	final := workflowGraphStageResultByName(t, result.CompletedStages, "final-report")
	if !strings.Contains(final.Result.Output, "Completion Summary") || !strings.Contains(final.Result.Output, "Artifact References") {
		t.Fatalf("expected completion report with artifact references, got %q", final.Result.Output)
	}
}

func TestWorkflowTemplateCatalogIncludesOperationsAndSupportStarters(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	runner := runtimeRef.WorkflowRunner()
	for _, tc := range []struct {
		name       string
		category   string
		wantStages []string
		wantText   []string
	}{
		{
			name:       "operations-runbook",
			category:   "operations",
			wantStages: []string{"plan", "draft", "risk-review", "quality", "operator-check", "handoff"},
			wantText:   []string{"Rollback", "quality_gate", "operator-check", "runbook-risk-review"},
		},
		{
			name:       "customer-support-triage",
			category:   "support",
			wantStages: []string{"triage", "needs-context", "collect-context", "draft-response", "review", "quality"},
			wantText:   []string{"Customer Reply", "input_gate", "customer-response-draft", "support-response-review"},
		},
	} {
		template, ok := runner.WorkflowTemplate(tc.name)
		if !ok {
			t.Fatalf("expected %s template", tc.name)
		}
		if template.Category != tc.category || template.Graph.Name != tc.name {
			t.Fatalf("unexpected %s template summary: %#v", tc.name, template)
		}
		validation := runner.ValidateWorkflowGraphDocument(tc.name, template.Graph)
		if !validation.Valid {
			t.Fatalf("expected valid %s template graph, got %#v", tc.name, validation)
		}
		for _, stageName := range tc.wantStages {
			if !workflowGraphHasStage(template.Graph, stageName) {
				t.Fatalf("expected %s template to include stage %q, got %#v", tc.name, stageName, template.Graph.Stages)
			}
		}
		rendered, err := RenderWorkflowTemplateYAML(tc.name+"-copy", tc.name)
		if err != nil {
			t.Fatalf("RenderWorkflowTemplateYAML(%s): %v", tc.name, err)
		}
		for _, want := range tc.wantText {
			if !strings.Contains(rendered, want) {
				t.Fatalf("expected rendered %s template to contain %q, got %q", tc.name, want, rendered)
			}
		}
	}
}

func workflowGraphHasStage(doc WorkflowGraphDocument, name string) bool {
	for _, stage := range doc.Stages {
		if stage.Name == name {
			return true
		}
	}
	return false
}

func TestWorkflowOptionsMergeCustomWorkflowNodeMetadata(t *testing.T) {
	runtimeHome := t.TempDir()
	metadataDir := filepath.Join(runtimeHome, "metadata", "workflow_nodes")
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		t.Fatalf("mkdir workflow node metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "policy_guard.yaml"), []byte(`
kind: goflow.workflow_node_metadata
version: 1
type: policy_guard
label: Review Gate
description: Custom Studio copy for review-gate policy nodes.
tags: [review, gate]
hints:
  - Use approval_preset when the selected team defines a reusable quorum.
warnings:
  - A deny route should point to a revision or stop path.
fields:
  - name: params.rule
    label: Guard rule
    description: Select a built-in or custom policy rule.
  - name: params.approval_preset
    label: Approval preset
    type: text
    description: Team quorum preset name.
outputs:
  - name: value
    description: Evaluated gate status.
examples:
  - title: Team approval gate
    description: Branch after a reusable team review preset.
    stage:
      name: review_gate
      node_type: policy_guard
      params:
        rule: team_approval_gate
        approval_preset: software-review
`), 0644); err != nil {
		t.Fatalf("write workflow node metadata: %v", err)
	}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	options := runtimeRef.WorkflowRunner().WorkflowOptions()
	var guard *WorkflowNodeTypeOption
	for i := range options.NodeTypes {
		if options.NodeTypes[i].Type == "policy_guard" {
			guard = &options.NodeTypes[i]
			break
		}
	}
	if guard == nil {
		t.Fatalf("expected policy_guard node type in options")
	}
	if guard.Label != "Review Gate" || guard.Source != "built_in+custom_metadata" || !guard.Custom || len(guard.Hints) != 1 || len(guard.Warnings) != 1 || len(guard.Examples) != 1 {
		t.Fatalf("expected custom metadata merged into policy_guard, got %#v", guard)
	}
	foundRuleField := false
	foundPresetField := false
	foundValueOutput := false
	for _, field := range guard.Fields {
		if field.Name == "params.rule" && field.Label == "Guard rule" && strings.Contains(field.Description, "custom policy rule") {
			foundRuleField = true
		}
		if field.Name == "params.approval_preset" && field.Type == "text" {
			foundPresetField = true
		}
	}
	for _, output := range guard.Outputs {
		if output.Name == "value" && strings.Contains(output.Description, "gate status") {
			foundValueOutput = true
		}
	}
	if !foundRuleField || !foundPresetField || !foundValueOutput {
		t.Fatalf("expected merged fields and outputs, fields=%#v outputs=%#v", guard.Fields, guard.Outputs)
	}
}

func TestWorkflowOptionsMergeCustomExpressionFunctionMetadata(t *testing.T) {
	runtimeHome := t.TempDir()
	metadataDir := filepath.Join(runtimeHome, "metadata", "expression_helpers")
	if err := os.MkdirAll(metadataDir, 0755); err != nil {
		t.Fatalf("mkdir expression helper metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(metadataDir, "risk_rank.yaml"), []byte(`
kind: goflow.workflow_expression_function
version: 1
name: risk_rank
label: Severity Rank
description: Custom Studio explanation for ranking severity-like values.
hints:
  - Use numeric comparisons such as risk_rank(ref) >= 4.
warnings:
  - Empty values rank as zero.
args:
  - name: value
    label: Severity value
    description: Text or numeric risk value.
examples:
  - risk_rank(stages.audit.outputs.severity) >= 4
`), 0644); err != nil {
		t.Fatalf("write expression helper metadata: %v", err)
	}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())
	options := runtimeRef.WorkflowRunner().WorkflowOptions()
	var risk *WorkflowExpressionFunctionOption
	for i := range options.ExpressionFunctions {
		if options.ExpressionFunctions[i].Name == "risk_rank" {
			risk = &options.ExpressionFunctions[i]
			break
		}
	}
	if risk == nil {
		t.Fatalf("expected risk_rank expression helper in options")
	}
	if risk.Label != "Severity Rank" || risk.Source != "built_in+custom_metadata" || !risk.Custom || len(risk.Hints) != 1 || len(risk.Warnings) != 1 || len(risk.Examples) != 1 {
		t.Fatalf("expected custom expression helper metadata, got %#v", risk)
	}
	if len(risk.Args) == 0 || risk.Args[0].Name != "value" || risk.Args[0].Label != "Severity value" || !strings.Contains(risk.Args[0].Description, "risk value") {
		t.Fatalf("expected merged risk_rank arg metadata, got %#v", risk.Args)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphForEachRunsBodyForItems(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "foreach-flow", `
name: foreach-flow
stages:
  - name: each
    node_type: for_each
    params:
      stage: process
      items: alpha,beta
    next: [report]
  - name: process
    agent: planner
    skill: execution-plan
    outputs:
      text: result.output
  - name: report
    agent: planner
    skill: execution-plan
    input:
      aggregate: stages.each.outputs.outputs
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Processed alpha."}},
		{Message: schema.Message{Content: "Processed beta."}},
		{Message: schema.Message{Content: "Report complete."}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "foreach-flow", "process items", true, nil)
	if err != nil {
		t.Fatalf("Run foreach-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 4 {
		t.Fatalf("expected two iterations, aggregate, and report, got %#v", result.CompletedStages)
	}
	if result.CompletedStages[0].Stage != "process[1]" || result.CompletedStages[1].Stage != "process[2]" || result.CompletedStages[2].Stage != "each" {
		t.Fatalf("unexpected foreach stage order: %#v", result.CompletedStages)
	}
	each := result.CompletedStages[2]
	if each.NodeType != "for_each" || each.Output.Variables["iteration_count"] != "2" || !strings.Contains(each.Output.Variables["outputs"], "Processed alpha") {
		t.Fatalf("expected foreach aggregate outputs, got %#v", each.Output)
	}
	firstPrompt := plannerLLM.requests[0].Messages[len(plannerLLM.requests[0].Messages)-1].Content
	if !strings.Contains(firstPrompt, "iteration.item: alpha") {
		t.Fatalf("expected iteration item in body prompt, got %q", firstPrompt)
	}
	reportPrompt := plannerLLM.requests[2].Messages[len(plannerLLM.requests[2].Messages)-1].Content
	if !strings.Contains(reportPrompt, "Processed alpha") || !strings.Contains(reportPrompt, "Processed beta") {
		t.Fatalf("expected aggregate outputs in report prompt, got %q", reportPrompt)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphForEachResumesSuspendedBody(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "foreach-approval-flow", `
name: foreach-approval-flow
stages:
  - name: each
    node_type: for_each
    params:
      stage: process
      items: alpha,beta
    next: [report]
  - name: process
    agent: fixer
    skill: code-writing
  - name: report
    agent: planner
    skill: execution-plan
    input:
      aggregate: stages.each.outputs.outputs
`)
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "Need to write alpha."},
		ToolCalls: []schema.ToolCall{{
			ID:        "call-foreach-1",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"alpha.txt","content":"alpha"}`),
		}},
	}, {
		Message: schema.Message{Content: "Processed alpha after approval."},
	}, {
		Message: schema.Message{Content: "Processed beta."},
	}}}
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Report complete."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), mcpClient, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   fixerLLM,
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	first, err := runtimeRef.WorkflowRunner().Run(context.Background(), "foreach-approval-flow", "process with approval", true, nil)
	if err != nil {
		t.Fatalf("Run foreach-approval-flow workflow graph: %v", err)
	}
	if first.Status != "awaiting_tool_approval" || first.NextStage != "process[1]" {
		t.Fatalf("expected suspended first iteration, got %#v", first)
	}
	if mcpClient.calls != 0 {
		t.Fatalf("expected no tool execution before approval, got %d calls", mcpClient.calls)
	}

	resumed, err := runtimeRef.WorkflowRunner().Resume(context.Background(), "foreach-approval-flow", "call-foreach-1", true, nil)
	if err != nil {
		t.Fatalf("Resume foreach-approval-flow workflow graph: %v", err)
	}
	if resumed.Status != "completed" || len(resumed.CompletedStages) != 4 {
		t.Fatalf("expected resumed foreach workflow to complete, got %#v", resumed.CompletedStages)
	}
	if mcpClient.calls != 1 {
		t.Fatalf("expected approved tool to execute once, got %d calls", mcpClient.calls)
	}
	if resumed.CompletedStages[0].Stage != "process[1]" || resumed.CompletedStages[1].Stage != "process[2]" || resumed.CompletedStages[2].Stage != "each" {
		t.Fatalf("unexpected resumed foreach order: %#v", resumed.CompletedStages)
	}
	if !strings.Contains(resumed.CompletedStages[2].Output.Variables["outputs"], "Processed alpha") || !strings.Contains(resumed.CompletedStages[2].Output.Variables["outputs"], "Processed beta") {
		t.Fatalf("expected aggregate outputs after resume, got %#v", resumed.CompletedStages[2].Output)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphLoopStopsWhenUntilPasses(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "loop-flow", `
name: loop-flow
stages:
  - name: repeat
    node_type: loop
    params:
      stage: review
      max_iterations: 3
      until: contains(previous.raw_output, "done")
    next: [report]
  - name: review
    agent: auditor
    skill: code-audit
  - name: report
    agent: planner
    skill: execution-plan
    input:
      loop_outputs: stages.repeat.outputs.outputs
`)
	auditorLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Still checking."}},
		{Message: schema.Message{Content: "done: risk review complete."}},
	}}
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Loop report complete."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "loop-flow", "review until done", true, nil)
	if err != nil {
		t.Fatalf("Run loop-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 4 {
		t.Fatalf("expected two loop iterations, aggregate, and report, got %#v", result.CompletedStages)
	}
	if result.CompletedStages[0].Stage != "review[1]" || result.CompletedStages[1].Stage != "review[2]" || result.CompletedStages[2].Stage != "repeat" {
		t.Fatalf("unexpected loop stage order: %#v", result.CompletedStages)
	}
	loop := result.CompletedStages[2]
	if loop.Output.Variables["iteration_count"] != "2" || loop.Output.Variables["passed"] != "true" || !strings.Contains(loop.Output.Variables["outputs"], "risk review complete") {
		t.Fatalf("expected loop aggregate outputs, got %#v", loop.Output)
	}
	if auditorLLM.calls != 2 {
		t.Fatalf("expected loop to stop after second iteration, got %d calls", auditorLLM.calls)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphSubWorkflowPublishesNestedRun(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "child-flow", `
name: child-flow
stages:
  - name: plan
    agent: planner
    skill: execution-plan
`)
	writeWorkflowGraph(t, runtimeHome, "parent-flow", `
name: parent-flow
stages:
  - name: child
    node_type: sub_workflow
    params:
      workflow: child-flow
      request: workflow.input
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
    input:
      child_summary: stages.child.outputs.summary
      child_run: stages.child.outputs.sub_run_id
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Child workflow complete."}},
		{Message: schema.Message{Content: "Parent report complete."}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "parent-flow", "run child then report", true, nil)
	if err != nil {
		t.Fatalf("Run parent-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 {
		t.Fatalf("expected sub workflow stage plus report, got %#v", result.CompletedStages)
	}
	child := result.CompletedStages[0]
	if child.NodeType != "sub_workflow" || child.Output.Variables["sub_workflow"] != "child-flow" || strings.TrimSpace(child.Output.Variables["sub_run_id"]) == "" {
		t.Fatalf("expected sub workflow metadata, got %#v", child.Output)
	}
	reportPrompt := plannerLLM.requests[1].Messages[len(plannerLLM.requests[1].Messages)-1].Content
	if !strings.Contains(reportPrompt, "Child workflow complete") || !strings.Contains(reportPrompt, "child_run") {
		t.Fatalf("expected nested workflow outputs in parent prompt, got %q", reportPrompt)
	}
	runs := runtimeRef.SessionSnapshot().WorkflowRuns
	if len(runs) < 2 {
		t.Fatalf("expected parent and child workflow runs, got %#v", runs)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphSubWorkflowPausesAndResumesParent(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "child-input-flow", `
name: child-input-flow
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields: target:string:Target:required
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      target: stages.collect.outputs.target
`)
	writeWorkflowGraph(t, runtimeHome, "parent-sub-pause-flow", `
name: parent-sub-pause-flow
stages:
  - name: child
    node_type: sub_workflow
    params:
      workflow: child-input-flow
      request: workflow.input
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
    input:
      child_summary: stages.child.outputs.summary
      child_run: stages.child.outputs.sub_run_id
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "Child plan after input."}},
		{Message: schema.Message{Content: "Parent report after child."}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	parent, err := runtimeRef.WorkflowRunner().Run(context.Background(), "parent-sub-pause-flow", "run child with input", true, nil)
	if err != nil {
		t.Fatalf("Run parent-sub-pause-flow workflow graph: %v", err)
	}
	if parent.Status != "awaiting_sub_workflow" || !parent.PendingSubWorkflow || parent.PendingSubWorkflowRunID == "" || parent.NextStage != "child" {
		t.Fatalf("expected parent to await nested sub-workflow, got %#v", parent)
	}
	parentRunID := parent.RunID
	childRunID := parent.PendingSubWorkflowRunID
	parentRun := workflowGraphRunSnapshotByID(t, runtimeRef, parentRunID)
	if parentRun.PendingSubWorkflowRunID != childRunID || parentRun.PendingSubWorkflowStatus != "awaiting_input" {
		t.Fatalf("expected parent run to persist child run metadata, got %#v", parentRun)
	}
	if _, err := runtimeRef.WorkflowRunner().ResumeSubWorkflow(context.Background(), parentRunID, nil); err == nil || !strings.Contains(err.Error(), "not completed") {
		t.Fatalf("expected parent resume to wait for child completion, got %v", err)
	}

	child, err := runtimeRef.WorkflowRunner().ResumeInput(context.Background(), childRunID, map[string]string{"target": "auth"}, nil)
	if err != nil {
		t.Fatalf("ResumeInput child-input-flow: %v", err)
	}
	if child.Status != "completed" {
		t.Fatalf("expected child workflow to complete, got %#v", child)
	}
	resumed, err := runtimeRef.WorkflowRunner().ResumeSubWorkflow(context.Background(), parentRunID, nil)
	if err != nil {
		t.Fatalf("ResumeSubWorkflow parent-sub-pause-flow: %v", err)
	}
	if resumed.Status != "completed" || resumed.RunID != parentRunID || len(resumed.CompletedStages) != 2 {
		t.Fatalf("expected parent workflow to complete after child resume, got %#v", resumed)
	}
	if resumed.CompletedStages[0].NodeType != "sub_workflow" || resumed.CompletedStages[0].Output.Variables["sub_run_id"] != childRunID {
		t.Fatalf("expected resumed parent to include child run output, got %#v", resumed.CompletedStages[0])
	}
	reportPrompt := plannerLLM.requests[1].Messages[len(plannerLLM.requests[1].Messages)-1].Content
	if !strings.Contains(reportPrompt, "Child plan after input") || !strings.Contains(reportPrompt, childRunID) {
		t.Fatalf("expected parent report prompt to include child output and run id, got %q", reportPrompt)
	}
	parentRun = workflowGraphRunSnapshotByID(t, runtimeRef, parentRunID)
	if parentRun.Status != "completed" || parentRun.PendingSubWorkflowRunID != "" {
		t.Fatalf("expected parent pending child metadata cleared after completion, got %#v", parentRun)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphManualInputGateResumesFromRun(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "manual-input-flow", `
name: manual-input-flow
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields: target:string:Target:required,severity:select:Severity
      field.severity.options: low,medium,high
      field.severity.default: medium
      prompt: Provide target and severity.
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      requested_target: stages.collect.outputs.target
      requested_severity: stages.collect.outputs.severity
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Manual input plan."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	first, err := runtimeRef.WorkflowRunner().Run(context.Background(), "manual-input-flow", "prepare work", true, nil)
	if err != nil {
		t.Fatalf("Run manual-input-flow workflow graph: %v", err)
	}
	if first.Status != "awaiting_input" || !first.PendingInput || first.NextStage != "collect" || first.RunID == "" {
		t.Fatalf("expected input gate pause with run id, got %#v", first)
	}
	if len(first.PendingFields) != 2 || first.PendingFields[0].Name != "target" || !first.PendingFields[0].Required || first.PendingFields[1].Type != "select" || len(first.PendingFields[1].Options) != 3 {
		t.Fatalf("expected manual input field schema, got %#v", first.PendingFields)
	}
	if !strings.Contains(first.ApprovalPrompt, "Provide target") {
		t.Fatalf("expected input prompt, got %q", first.ApprovalPrompt)
	}
	if _, err := runtimeRef.WorkflowRunner().ResumeInput(context.Background(), first.RunID, map[string]string{"severity": "high"}, nil); err == nil || !strings.Contains(err.Error(), "target") {
		t.Fatalf("expected missing required target error, got %v", err)
	}

	resumed, err := runtimeRef.WorkflowRunner().ResumeInput(context.Background(), first.RunID, map[string]string{"target": "auth", "severity": "high"}, nil)
	if err != nil {
		t.Fatalf("ResumeInput manual-input-flow: %v", err)
	}
	if resumed.Status != "completed" || len(resumed.CompletedStages) != 2 {
		t.Fatalf("expected resumed workflow to complete, got %#v", resumed)
	}
	if got := resumed.CompletedStages[0].Output.Variables["target"]; got != "auth" {
		t.Fatalf("expected submitted target output, got %#v", resumed.CompletedStages[0].Output)
	}
	prompt := plannerLLM.requests[0].Messages[len(plannerLLM.requests[0].Messages)-1].Content
	if !strings.Contains(prompt, "requested_target") || !strings.Contains(prompt, "auth") || !strings.Contains(prompt, "requested_severity") || !strings.Contains(prompt, "high") {
		t.Fatalf("expected submitted manual inputs in downstream prompt, got %q", prompt)
	}
	runs := runtimeRef.SessionSnapshot().WorkflowRuns
	if len(runs) == 0 || runs[0].ID != first.RunID || runs[0].Status != "completed" || len(runs[0].CompletedStages) != 2 {
		t.Fatalf("expected original workflow run to be completed after input resume, got %#v", runs)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphManualInputGateSupportsJSONFieldSchema(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "manual-input-json-flow", `
name: manual-input-json-flow
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields_json: |
        {
          "fields": [
            {"name": "target", "type": "url", "label": "Target URL", "placeholder": "https://example.test", "group": "Scope", "required": true},
            {"name": "depth", "type": "integer", "min": 1, "max": 3, "default": 1},
            {"name": "tags", "type": "select", "options": ["api", "auth", "db"], "multiple": true},
            {"name": "payload", "type": "object", "required": true},
            {"name": "findings", "type": "array"},
            {"name": "evidence", "type": "textarea", "rows": 6, "pattern": "(?i)evidence"},
            {"name": "credentials", "label": "Credentials", "children": [
              {"name": "token", "type": "password", "required": true}
            ]}
          ]
        }
      prompt: Provide rich input fields.
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      target: stages.collect.outputs.target
      depth: stages.collect.outputs.depth
      tags: stages.collect.outputs.tags
      payload: stages.collect.outputs.payload
      findings: stages.collect.outputs.findings
      token: stages.collect.outputs.credentials.token
      evidence: stages.collect.outputs.evidence
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Rich input plan."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	first, err := runtimeRef.WorkflowRunner().Run(context.Background(), "manual-input-json-flow", "prepare rich input", true, nil)
	if err != nil {
		t.Fatalf("Run manual-input-json-flow workflow graph: %v", err)
	}
	if first.Status != "awaiting_input" || len(first.PendingFields) != 7 {
		t.Fatalf("expected seven pending input fields, got %#v", first)
	}
	if first.PendingFields[0].Name != "target" || first.PendingFields[0].Type != "url" || first.PendingFields[0].Placeholder == "" || first.PendingFields[0].Group != "Scope" {
		t.Fatalf("expected target URL field metadata, got %#v", first.PendingFields[0])
	}
	if first.PendingFields[1].Name != "depth" || first.PendingFields[1].Type != "integer" || first.PendingFields[1].Min != "1" || first.PendingFields[1].Max != "3" || first.PendingFields[1].Default != "1" {
		t.Fatalf("expected depth range metadata, got %#v", first.PendingFields[1])
	}
	if first.PendingFields[2].Name != "tags" || first.PendingFields[2].Type != "select" || !first.PendingFields[2].Multiple || len(first.PendingFields[2].Options) != 3 {
		t.Fatalf("expected multiple select tags metadata, got %#v", first.PendingFields[2])
	}
	if first.PendingFields[3].Name != "payload" || first.PendingFields[3].Type != "object" || !first.PendingFields[3].Required {
		t.Fatalf("expected required object payload metadata, got %#v", first.PendingFields[3])
	}
	if first.PendingFields[4].Name != "findings" || first.PendingFields[4].Type != "array" {
		t.Fatalf("expected array findings metadata, got %#v", first.PendingFields[4])
	}
	if first.PendingFields[5].Type != "textarea" || first.PendingFields[5].Rows != 6 || first.PendingFields[5].Pattern == "" {
		t.Fatalf("expected textarea metadata, got %#v", first.PendingFields[5])
	}
	if first.PendingFields[6].Name != "credentials.token" || first.PendingFields[6].Group != "Credentials" || !first.PendingFields[6].Required {
		t.Fatalf("expected flattened nested credential field, got %#v", first.PendingFields[6])
	}
	if _, err := runtimeRef.WorkflowRunner().ResumeInput(context.Background(), first.RunID, map[string]string{
		"target":            "https://example.test",
		"depth":             "5",
		"payload":           `{"risk":"low"}`,
		"evidence":          "evidence: checked",
		"credentials.token": "secret",
	}, nil); err == nil || !strings.Contains(err.Error(), "at most 3") {
		t.Fatalf("expected depth range validation error, got %v", err)
	}
	if _, err := runtimeRef.WorkflowRunner().ResumeInput(context.Background(), first.RunID, map[string]string{
		"target":            "https://example.test",
		"depth":             "2",
		"tags":              `["api","unknown"]`,
		"payload":           `{"risk":"low"}`,
		"evidence":          "evidence: checked",
		"credentials.token": "secret",
	}, nil); err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Fatalf("expected multiple select validation error, got %v", err)
	}
	if _, err := runtimeRef.WorkflowRunner().ResumeInput(context.Background(), first.RunID, map[string]string{
		"target":            "https://example.test",
		"depth":             "2",
		"payload":           `["not","object"]`,
		"evidence":          "evidence: checked",
		"credentials.token": "secret",
	}, nil); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("expected object validation error, got %v", err)
	}
	resumed, err := runtimeRef.WorkflowRunner().ResumeInputValues(context.Background(), first.RunID, map[string]any{
		"target":            "https://example.test",
		"depth":             float64(2),
		"tags":              []any{"api", "auth"},
		"payload":           map[string]any{"risk": "low"},
		"findings":          []any{"one", "two"},
		"evidence":          "evidence: checked",
		"credentials.token": "secret",
	}, nil)
	if err != nil {
		t.Fatalf("ResumeInput manual-input-json-flow: %v", err)
	}
	if resumed.Status != "completed" || resumed.CompletedStages[0].Output.Variables["credentials.token"] != "secret" || resumed.CompletedStages[0].Output.Variables["tags"] != `["api","auth"]` {
		t.Fatalf("expected resumed workflow with nested input output, got %#v", resumed)
	}
	if got := resumed.CompletedStages[0].Output.Values["depth"]; got != 2 {
		t.Fatalf("expected typed integer depth output, got %#v", resumed.CompletedStages[0].Output.Values)
	}
	if tags, ok := resumed.CompletedStages[0].Output.Values["tags"].([]any); !ok || len(tags) != 2 || tags[0] != "api" || tags[1] != "auth" {
		t.Fatalf("expected typed tags output, got %#v", resumed.CompletedStages[0].Output.Values["tags"])
	}
	if payload, ok := resumed.CompletedStages[0].Output.Values["payload"].(map[string]any); !ok || payload["risk"] != "low" {
		t.Fatalf("expected typed payload output, got %#v", resumed.CompletedStages[0].Output.Values["payload"])
	}
	if planInputs := resumed.CompletedStages[1].InputValues; planInputs["depth"] != 2 {
		t.Fatalf("expected typed mapped inputs on downstream stage, got %#v", planInputs)
	}
	prompt := plannerLLM.requests[0].Messages[len(plannerLLM.requests[0].Messages)-1].Content
	if !strings.Contains(prompt, "credentials.token") || !strings.Contains(prompt, "https://example.test") || !strings.Contains(prompt, "payload") || !strings.Contains(prompt, "findings") {
		t.Fatalf("expected rich manual inputs in downstream prompt, got %q", prompt)
	}
	run := runtimeRef.SessionSnapshot().WorkflowRuns[0]
	if run.CompletedStages[0].OutputValues["depth"] != 2 || run.CompletedStages[1].InputValues["depth"] != 2 {
		t.Fatalf("expected typed values persisted in workflow run snapshot, got %#v", run.CompletedStages)
	}
}

func TestWorkflowRunnerWorkflowGraphConditionsUseTypedValues(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "typed-condition-flow", `
name: typed-condition-flow
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields_json: |
        {"fields":[
          {"name":"depth","type":"integer","required":true},
          {"name":"tags","type":"select","options":["api","auth"],"multiple":true},
          {"name":"payload","type":"object","required":true}
        ]}
    next: [depth-ok]
  - name: depth-ok
    node_type: condition
    condition: stages.collect.outputs.depth >= 2
    routes:
      true: tags-ok
      false: fail
  - name: tags-ok
    node_type: condition
    condition: contains(stages.collect.outputs.tags, "api")
    routes:
      true: api-tag-member
      false: fail
  - name: api-tag-member
    node_type: condition
    condition: in("api", stages.collect.outputs.tags)
    routes:
      true: risk-pattern
      false: fail
  - name: risk-pattern
    node_type: condition
    condition: matches(stages.collect.outputs.payload.risk, "hi.*")
    routes:
      true: risk-rank
      false: fail
  - name: risk-rank
    node_type: policy_guard
    policy: risk_rank(stages.collect.outputs.payload.risk) >= 4
    routes:
      allow: any-auth
      deny: fail
  - name: any-auth
    node_type: condition
    condition: any(stages.collect.outputs.tags, "auth")
    routes:
      true: all-tags
      false: fail
  - name: all-tags
    node_type: condition
    condition: all(stages.collect.outputs.tags)
    routes:
      true: enough-tags
      false: fail
  - name: enough-tags
    node_type: policy_guard
    params:
      rule: min_count
      ref: stages.collect.outputs.tags
      minimum: 2
    routes:
      allow: risk-ok
      deny: fail
  - name: risk-ok
    node_type: policy_guard
    policy: stages.collect.outputs.payload.risk == "high"
    routes:
      allow: depth-switch
      deny: fail
  - name: depth-switch
    node_type: switch
    switch_on: stages.collect.outputs.depth
    cases:
      "2": plan
      default: fail
  - name: plan
    agent: planner
    skill: execution-plan
    input:
      risk: stages.collect.outputs.payload.risk
      first_tag: stages.collect.outputs.tags.0
  - name: fail
    agent: chat
    skill: execution-plan
`)
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "typed condition plan"}}}}
	chatLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fail path"}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    chatLLM,
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	first, err := runtimeRef.WorkflowRunner().Run(context.Background(), "typed-condition-flow", "check typed flow", true, nil)
	if err != nil {
		t.Fatalf("Run typed-condition-flow workflow graph: %v", err)
	}
	if first.Status != "awaiting_input" || first.RunID == "" {
		t.Fatalf("expected typed condition workflow to pause for input, got %#v", first)
	}
	resumed, err := runtimeRef.WorkflowRunner().ResumeInputValues(context.Background(), first.RunID, map[string]any{
		"depth":   float64(2),
		"tags":    []any{"api", "auth"},
		"payload": map[string]any{"risk": "high"},
	}, nil)
	if err != nil {
		t.Fatalf("ResumeInputValues typed-condition-flow: %v", err)
	}
	if resumed.Status != "completed" {
		t.Fatalf("expected typed condition workflow completion, got %#v", resumed)
	}
	if len(plannerLLM.requests) != 1 || len(chatLLM.requests) != 0 {
		t.Fatalf("expected plan path only, planner=%d chat=%d", len(plannerLLM.requests), len(chatLLM.requests))
	}
	if !workflowStageResultNamesContain(resumed.CompletedStages, "depth-ok") ||
		!workflowStageResultNamesContain(resumed.CompletedStages, "tags-ok") ||
		!workflowStageResultNamesContain(resumed.CompletedStages, "api-tag-member") ||
		!workflowStageResultNamesContain(resumed.CompletedStages, "risk-pattern") ||
		!workflowStageResultNamesContain(resumed.CompletedStages, "risk-rank") ||
		!workflowStageResultNamesContain(resumed.CompletedStages, "any-auth") ||
		!workflowStageResultNamesContain(resumed.CompletedStages, "all-tags") ||
		!workflowStageResultNamesContain(resumed.CompletedStages, "enough-tags") ||
		!workflowStageResultNamesContain(resumed.CompletedStages, "risk-ok") ||
		!workflowStageResultNamesContain(resumed.CompletedStages, "depth-switch") ||
		!workflowStageResultNamesContain(resumed.CompletedStages, "plan") {
		t.Fatalf("expected typed control stages and plan completion, got %#v", resumed.CompletedStages)
	}
	prompt := plannerLLM.requests[0].Messages[len(plannerLLM.requests[0].Messages)-1].Content
	if !strings.Contains(prompt, "risk") || !strings.Contains(prompt, "high") || !strings.Contains(prompt, "first_tag") || !strings.Contains(prompt, "api") {
		t.Fatalf("expected nested typed inputs in plan prompt, got %q", prompt)
	}
}

func TestWorkflowRunnerValidateWorkflowExpressionProvidesReferenceHints(t *testing.T) {
	doc := WorkflowGraphDocument{
		Name: "typed-expression-flow",
		Stages: []WorkflowGraphStageDocument{
			{
				Name:     "collect",
				NodeType: "input_gate",
				Params: map[string]string{
					"manual": "true",
					"fields_json": `{"fields":[
						{"name":"depth","type":"integer","required":true},
						{"name":"tags","type":"select","options":["api","auth"],"multiple":true},
						{"name":"payload","type":"object","required":true}
					]}`,
				},
				Next: []string{"guard"},
			},
			{
				Name:      "guard",
				NodeType:  "condition",
				Condition: `stages.collect.outputs.payload.risk == "high"`,
				Routes:    map[string]string{"true": "plan", "false": "plan"},
			},
			{Name: "plan", NodeType: "agent", Agent: "planner", Skill: "execution-plan"},
		},
	}
	runner := &WorkflowRunner{}
	result := runner.ValidateWorkflowExpression("typed-expression-flow", doc, `len(stages.collect.outputs.tags) >= 2`, "condition")
	if !result.Valid || result.ValueType != "boolean" || len(result.References) != 1 || result.References[0].Type != "array" {
		t.Fatalf("expected valid typed array expression, got %#v", result)
	}
	var sawPayloadSuggestion bool
	for _, suggestion := range result.Suggestions {
		if suggestion.Reference == "stages.collect.outputs.payload" && suggestion.Type == "object" {
			sawPayloadSuggestion = true
			break
		}
	}
	if !sawPayloadSuggestion {
		t.Fatalf("expected payload object suggestion, got %#v", result.Suggestions)
	}
	nested := runner.ValidateWorkflowExpression("typed-expression-flow", doc, `stages.collect.outputs.payload.risk == "high"`, "condition")
	if !nested.Valid || len(nested.References) != 1 || nested.References[0].Path != "risk" {
		t.Fatalf("expected valid nested object reference, got %#v", nested)
	}
	membership := runner.ValidateWorkflowExpression("typed-expression-flow", doc, `in("api", stages.collect.outputs.tags)`, "condition")
	if !membership.Valid || len(membership.References) != 1 || membership.References[0].Expression != "stages.collect.outputs.tags" {
		t.Fatalf("expected valid membership expression, got %#v", membership)
	}
	regex := runner.ValidateWorkflowExpression("typed-expression-flow", doc, `matches(stages.collect.outputs.payload.risk, "high|critical")`, "condition")
	if !regex.Valid || len(regex.References) != 1 || regex.References[0].Path != "risk" {
		t.Fatalf("expected valid regex expression, got %#v", regex)
	}
	rank := runner.ValidateWorkflowExpression("typed-expression-flow", doc, `risk_rank(stages.collect.outputs.payload.risk) >= 4`, "condition")
	if !rank.Valid || len(rank.References) != 1 || rank.References[0].Path != "risk" {
		t.Fatalf("expected valid risk_rank expression, got %#v", rank)
	}
	anyMatch := runner.ValidateWorkflowExpression("typed-expression-flow", doc, `any(stages.collect.outputs.tags, "auth")`, "condition")
	if !anyMatch.Valid || len(anyMatch.References) != 1 || anyMatch.References[0].Type != "array" {
		t.Fatalf("expected valid any() expression, got %#v", anyMatch)
	}
	invalidFunctionArgs := runner.ValidateWorkflowExpression("typed-expression-flow", doc, `matches(stages.collect.outputs.payload.risk)`, "condition")
	if invalidFunctionArgs.Valid || len(invalidFunctionArgs.Issues) == 0 || !strings.Contains(invalidFunctionArgs.Issues[0].Message, "matches() expects two arguments") {
		t.Fatalf("expected matches arg count diagnostic, got %#v", invalidFunctionArgs)
	}
	unsupportedFunction := runner.ValidateWorkflowExpression("typed-expression-flow", doc, `foo(stages.collect.outputs.tags)`, "condition")
	if unsupportedFunction.Valid || len(unsupportedFunction.Issues) == 0 || !strings.Contains(unsupportedFunction.Issues[0].Message, "unsupported workflow expression function foo") {
		t.Fatalf("expected unsupported function diagnostic, got %#v", unsupportedFunction)
	}
	run := session.WorkflowRunSnapshot{
		ID:   "run-1",
		Name: "typed-expression-flow",
		CompletedStages: []session.WorkflowRunStageSnapshot{{
			Stage: "collect",
			OutputValues: map[string]any{
				"payload": map[string]any{"risk": "high", "score": 9},
				"tags":    []any{"api", "auth"},
			},
		}},
	}
	withRun := runner.ValidateWorkflowExpressionWithRun("typed-expression-flow", doc, run, `stages.collect.outputs.payload.risk == "high"`, "condition")
	if !withRun.Valid || len(withRun.References) != 1 || withRun.References[0].Type != "string" || withRun.References[0].Path != "risk" {
		t.Fatalf("expected run snapshot to infer nested string reference, got %#v", withRun)
	}
	var sawRiskSuggestion bool
	for _, suggestion := range withRun.Suggestions {
		if suggestion.Reference == "stages.collect.outputs.payload.risk" && suggestion.Type == "string" && suggestion.Source == "run_output" {
			sawRiskSuggestion = true
			break
		}
	}
	if !sawRiskSuggestion {
		t.Fatalf("expected observed nested risk suggestion, got %#v", withRun.Suggestions)
	}
	missingNested := runner.ValidateWorkflowExpressionWithRun("typed-expression-flow", doc, run, `stages.collect.outputs.payload.missing == "x"`, "condition")
	if missingNested.Valid || len(missingNested.Issues) == 0 || !strings.Contains(missingNested.Issues[len(missingNested.Issues)-1].Message, "missing object key") {
		t.Fatalf("expected missing observed object key diagnostic, got %#v", missingNested)
	}
	invalid := runner.ValidateWorkflowExpression("typed-expression-flow", doc, `stages.collect.outputs.tags.name == "api"`, "condition")
	if invalid.Valid || len(invalid.Issues) == 0 || !strings.Contains(invalid.Issues[len(invalid.Issues)-1].Message, "non-numeric") {
		t.Fatalf("expected invalid array path diagnostic, got %#v", invalid)
	}
	missing := runner.ValidateWorkflowExpression("typed-expression-flow", doc, `stages.collect.outputs.missing == "x"`, "condition")
	if missing.Valid || len(missing.Issues) == 0 || !strings.Contains(missing.Issues[len(missing.Issues)-1].Message, "unknown output") {
		t.Fatalf("expected missing output diagnostic, got %#v", missing)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphCheckpointPausesWhenNotApproved(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "checkpoint-flow", `
name: checkpoint-flow
stages:
  - name: checkpoint
    node_type: checkpoint
    params:
      prompt: review collected evidence before continuing
    next: [plan]
  - name: plan
    agent: planner
    skill: execution-plan
`)
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())

	first, err := runtimeRef.WorkflowRunner().Run(context.Background(), "checkpoint-flow", "continue carefully", false, nil)
	if err != nil {
		t.Fatalf("Run checkpoint-flow workflow graph: %v", err)
	}
	if first.Status != "awaiting_approval" || first.NextStage != "checkpoint" || !strings.Contains(first.ApprovalPrompt, "review collected evidence") {
		t.Fatalf("expected checkpoint approval pause, got %#v", first)
	}

	second, err := runtimeRef.WorkflowRunner().Run(context.Background(), "checkpoint-flow", "continue carefully", true, nil)
	if err != nil {
		t.Fatalf("Run approved checkpoint-flow workflow graph: %v", err)
	}
	if second.Status != "completed" || len(second.CompletedStages) != 2 || second.CompletedStages[0].Stage != "checkpoint" || second.CompletedStages[1].Stage != "plan" {
		t.Fatalf("expected checkpoint to continue when approved, got %#v", second)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphRetriesStage(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "retry-flow", `
name: retry-flow
stages:
  - name: unstable
    agent: planner
    skill: execution-plan
    retry:
      max_attempts: 2
`)
	plannerLLM := &workflowFlakyLLMClient{}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "retry-flow", "retry once", true, nil)
	if err != nil {
		t.Fatalf("Run retry-flow workflow graph: %v", err)
	}
	if plannerLLM.calls != 2 {
		t.Fatalf("expected one retry, got %d calls", plannerLLM.calls)
	}
	if len(result.CompletedStages) != 1 || result.CompletedStages[0].Result.Output != "Recovered after retry." {
		t.Fatalf("expected recovered stage output, got %#v", result.CompletedStages)
	}
	if result.CompletedStages[0].Attempts != 2 {
		t.Fatalf("expected stage attempt count to be persisted, got %#v", result.CompletedStages[0])
	}
}

func TestWorkflowRunnerCustomWorkflowGraphRoutesOnErrorFallback(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "fallback-flow", `
name: fallback-flow
stages:
  - name: verify
    agent: auditor
    skill: code-audit
    retry:
      max_attempts: 1
    on_error: [fallback]
  - name: fallback
    agent: planner
    skill: execution-plan
`)
	auditorLLM := &workflowFailingLLMClient{err: errors.New("verifier failed")}
	plannerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Fallback plan."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": plannerLLM,
		"fixer":   &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor": auditorLLM,
	})

	result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "fallback-flow", "verify then fallback", true, nil)
	if err != nil {
		t.Fatalf("Run fallback-flow workflow graph: %v", err)
	}
	if result.Status != "completed" || len(result.CompletedStages) != 2 {
		t.Fatalf("expected failed verifier stage plus fallback stage, got %#v", result)
	}
	if result.CompletedStages[0].Stage != "verify" || result.CompletedStages[0].Status != "failed" || !strings.Contains(result.CompletedStages[0].Result.Output, "verifier failed") {
		t.Fatalf("expected failed verifier evidence, got %#v", result.CompletedStages[0])
	}
	if result.CompletedStages[1].Stage != "fallback" || result.CompletedStages[1].Result.Output != "Fallback plan." || len(plannerLLM.requests) != 1 {
		t.Fatalf("expected fallback stage output, got stages=%#v plannerRequests=%d", result.CompletedStages, len(plannerLLM.requests))
	}
}

func TestWorkflowRunnerCustomWorkflowGraphResumesSuspendedStage(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "release-check", `
name: release-check
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    next: [implement]
  - name: implement
    agent: fixer
    skill: code-writing
    next: [audit]
  - name: audit
    agent: auditor
    skill: code-audit
`)
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), mcpClient, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Plan ready."}}}},
		"fixer": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{
			Message: schema.Message{Content: "Need to write."},
			ToolCalls: []schema.ToolCall{{
				ID:        "call-1",
				Name:      "write_file",
				Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
			}},
		}, {
			Message: schema.Message{Content: "Implementation done."},
		}}},
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Audit done."}}}},
	})

	first, err := runtimeRef.WorkflowRunner().Run(context.Background(), "release-check", "add the feature", true, nil)
	if err != nil {
		t.Fatalf("Run custom workflow graph: %v", err)
	}
	if first.Status != "awaiting_tool_approval" || first.NextStage != "implement" {
		t.Fatalf("expected suspended implement stage, got %#v", first)
	}
	if mcpClient.calls != 0 {
		t.Fatalf("expected no tool execution before approval, got %d calls", mcpClient.calls)
	}

	resumed, err := runtimeRef.WorkflowRunner().Resume(context.Background(), "release-check", "call-1", true, nil)
	if err != nil {
		t.Fatalf("Resume custom workflow graph: %v", err)
	}
	if resumed.Status != "completed" || len(resumed.CompletedStages) != 3 {
		t.Fatalf("expected resumed workflow to complete, got %#v", resumed)
	}
	if mcpClient.calls != 1 {
		t.Fatalf("expected approved tool to execute once, got %d calls", mcpClient.calls)
	}
	if resumed.CompletedStages[1].Result.Output != "Implementation done." || resumed.CompletedStages[2].Result.Output != "Audit done." {
		t.Fatalf("unexpected resumed stage outputs: %#v", resumed.CompletedStages)
	}
	if runtimeRef.ActiveAgent() != "chat" {
		t.Fatalf("expected default agent restored after resume, got %q", runtimeRef.ActiveAgent())
	}
}

func TestWorkflowRunnerCustomWorkflowGraphApproveToolsAutoApprovesLaterMatchingStageCall(t *testing.T) {
	runtimeHome := t.TempDir()
	writeWorkflowGraph(t, runtimeHome, "release-check", `
name: release-check
stages:
  - name: implement
    agent: fixer
    skill: code-writing
    next: [audit]
  - name: audit
    agent: auditor
    skill: code-audit
`)
	writeTool := schema.Tool{Name: "write_file", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)}
	mcpClient := &stubRuntimeMCP{tools: []schema.Tool{writeTool}}
	fixerLLM := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{
		Message: schema.Message{Content: "Need first write."},
		ToolCalls: []schema.ToolCall{{
			ID:        "call-1",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"a.txt","content":"hello"}`),
		}},
	}, {
		Message: schema.Message{Content: "Need second write."},
		ToolCalls: []schema.ToolCall{{
			ID:        "call-2",
			Name:      "write_file",
			Arguments: json.RawMessage(`{"path":"b.txt","content":"world"}`),
		}},
	}, {
		Message: schema.Message{Content: "Implementation done."},
	}}}
	runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), mcpClient, map[string]interfaces.LLMClient{
		"chat":    &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":   fixerLLM,
		"auditor": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Audit done."}}}},
	})

	first, err := runtimeRef.WorkflowRunner().Run(context.Background(), "release-check", "add the feature", true, nil)
	if err != nil {
		t.Fatalf("Run custom workflow graph: %v", err)
	}
	if first.Status != "awaiting_tool_approval" || first.NextStage != "implement" {
		t.Fatalf("expected suspended implement stage, got %#v", first)
	}

	if err := runtimeRef.RememberPendingWorkflowToolApproval("call-1"); err != nil {
		t.Fatalf("RememberPendingWorkflowToolApproval: %v", err)
	}
	resumed, err := runtimeRef.WorkflowRunner().Resume(context.Background(), "release-check", "call-1", true, nil)
	if err != nil {
		t.Fatalf("Resume custom workflow graph: %v", err)
	}
	if resumed.Status != "completed" {
		t.Fatalf("expected workflow to complete after scoped approve-tools, got %#v", resumed)
	}
	if mcpClient.calls != 2 {
		t.Fatalf("expected current and later matching write calls to execute once each, got %d", mcpClient.calls)
	}
	if len(runtimeRef.SessionSnapshot().PendingApprovals) != 0 {
		t.Fatalf("expected no pending approvals after auto-approval, got %#v", runtimeRef.SessionSnapshot().PendingApprovals)
	}
}

func TestWorkflowRunnerCustomWorkflowGraphReportsInvalidFile(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantError string
	}{
		{
			name: "missing-skill",
			yaml: `
name: missing-skill
stages:
  - name: triage
    agent: planner
`,
			wantError: "stage triage missing skill",
		},
		{
			name: "missing-agent",
			yaml: `
name: missing-agent
stages:
  - name: triage
    skill: execution-plan
`,
			wantError: "stage triage missing agent",
		},
		{
			name: "unknown-team",
			yaml: `
name: unknown-team
stages:
  - name: team
    node_type: team
    params:
      team: missing-team
`,
			wantError: "unknown team template",
		},
		{
			name: "unknown-next",
			yaml: `
name: unknown-next
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    next: [implement]
`,
			wantError: `references unknown next stage "implement"`,
		},
		{
			name: "invalid-artifact",
			yaml: `
name: invalid-artifact
stages:
  - name: plan
    agent: planner
    skill: execution-plan
    artifacts:
      - name: report
        kind: report
`,
			wantError: `artifact report must declare ref or content`,
		},
		{
			name: "duplicate-stage",
			yaml: `
name: duplicate-stage
stages:
  - name: triage
    agent: planner
    skill: execution-plan
  - name: triage
    agent: auditor
    skill: code-audit
`,
			wantError: `duplicate stage "triage"`,
		},
		{
			name: "name-mismatch",
			yaml: `
name: other-name
stages:
  - name: triage
    agent: planner
    skill: execution-plan
`,
			wantError: `name "other-name" does not match "name-mismatch"`,
		},
		{
			name: "empty-stages",
			yaml: `
name: empty-stages
stages: []
`,
			wantError: "has no stages",
		},
		{
			name: "missing-name",
			yaml: `
description: Missing workflow name.
stages:
  - name: triage
    agent: planner
    skill: execution-plan
`,
			wantError: "missing name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeHome := t.TempDir()
			writeWorkflowGraph(t, runtimeHome, tt.name, tt.yaml)
			runtimeRef := newWorkflowGraphRuntime(t, runtimeHome, workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())

			_, err := runtimeRef.WorkflowRunner().Run(context.Background(), tt.name, "run it", true, nil)
			if err == nil {
				t.Fatal("expected invalid custom workflow graph error")
			}
			if strings.Contains(err.Error(), "unknown workflow") || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected validation error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestWorkflowRunnerCustomWorkflowGraphReportsInvalidInvocationName(t *testing.T) {
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, workflowGraphTestClients())

	_, err := runtimeRef.WorkflowRunner().Run(context.Background(), "../release-check", "run it", true, nil)
	if err == nil {
		t.Fatal("expected invalid workflow name error")
	}
	if strings.Contains(err.Error(), "unknown workflow") || !strings.Contains(err.Error(), "invalid workflow name") {
		t.Fatalf("expected invalid workflow name error, got %v", err)
	}
}

type workflowFlakyLLMClient struct {
	requests []schema.ChatRequest
	calls    int
}

func (s *workflowFlakyLLMClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	panic("unused")
}

func (s *workflowFlakyLLMClient) StreamChat(_ context.Context, req schema.ChatRequest, _ interfaces.StreamHandler) (schema.ChatResponse, error) {
	s.requests = append(s.requests, req)
	s.calls++
	if s.calls == 1 {
		return schema.ChatResponse{}, errors.New("temporary model failure")
	}
	return schema.ChatResponse{Message: schema.Message{Content: "Recovered after retry."}}, nil
}

func (s *workflowFlakyLLMClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

type workflowFailingLLMClient struct {
	requests []schema.ChatRequest
	err      error
}

func (s *workflowFailingLLMClient) Chat(context.Context, schema.ChatRequest) (schema.ChatResponse, error) {
	panic("unused")
}

func (s *workflowFailingLLMClient) StreamChat(_ context.Context, req schema.ChatRequest, _ interfaces.StreamHandler) (schema.ChatResponse, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return schema.ChatResponse{}, s.err
	}
	return schema.ChatResponse{}, errors.New("workflow stage failed")
}

func (s *workflowFailingLLMClient) Capabilities() []string {
	return []string{"chat", "stream"}
}

func writeWorkflowGraph(t *testing.T, runtimeHome, name, content string) {
	t.Helper()
	dir := filepath.Join(runtimeHome, "workflows", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll workflow dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile workflow graph: %v", err)
	}
}

func writeWorkflowGraphYAML(t *testing.T, runtimeHome, name, content string) {
	t.Helper()
	dir := filepath.Join(runtimeHome, "workflows", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll workflow dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "workflow.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile workflow graph: %v", err)
	}
}

func writeWorkflowPolicyRule(t *testing.T, runtimeHome, name, content string) {
	t.Helper()
	dir := filepath.Join(runtimeHome, "policies", "workflow_rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll workflow policy rule dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile workflow policy rule: %v", err)
	}
}

func workflowStringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func workflowStageResultNamesContain(stages []WorkflowStageResult, want string) bool {
	for _, stage := range stages {
		if string(stage.Stage) == want {
			return true
		}
	}
	return false
}

func workflowGraphStageResultByName(t *testing.T, stages []WorkflowStageResult, want string) WorkflowStageResult {
	t.Helper()
	for _, stage := range stages {
		if string(stage.Stage) == want {
			return stage
		}
	}
	t.Fatalf("stage %s not found in %#v", want, stages)
	return WorkflowStageResult{}
}

func workflowGraphRunSnapshotByID(t *testing.T, runtimeRef *Runtime, runID string) session.WorkflowRunSnapshot {
	t.Helper()
	for _, run := range runtimeRef.SessionSnapshot().WorkflowRuns {
		if run.ID == runID {
			return run
		}
	}
	t.Fatalf("workflow run %s not found in %#v", runID, runtimeRef.SessionSnapshot().WorkflowRuns)
	return session.WorkflowRunSnapshot{}
}

func workflowGraphTestSkills() []schema.Skill {
	return []schema.Skill{
		{Name: "execution-plan", Description: "Plan the work", Mode: "plan", PreferredAgent: "planner", Activation: schema.Activation{Keywords: []string{"plan"}}, Instructions: "Plan."},
		{Name: "code-writing", Description: "Implement code", Mode: "fix", PreferredAgent: "fixer", Activation: schema.Activation{Keywords: []string{"implement", "code"}}, Instructions: "Implement."},
		{Name: "code-audit", Description: "Audit code", Mode: "audit", PreferredAgent: "auditor", Activation: schema.Activation{Keywords: []string{"audit", "security"}}, Instructions: "Audit."},
		{Name: "vulnerability-research", Description: "Research vulnerabilities", Mode: "audit", PreferredAgent: "security-researcher", Activation: schema.Activation{Keywords: []string{"security", "vulnerability"}}, Instructions: "Research vulnerabilities defensively."},
		{Name: "web-vulnerability-research", Description: "Research web vulnerabilities", Mode: "audit", PreferredAgent: "web-security-researcher", Activation: schema.Activation{Keywords: []string{"web", "browser"}}, Instructions: "Research authorized web evidence."},
		{Name: "reverse-engineering", Description: "Reverse engineering", Mode: "audit", PreferredAgent: "binary-analyst", Activation: schema.Activation{Keywords: []string{"binary", "reverse"}}, Instructions: "Analyze binaries statically."},
	}
}

func newWorkflowGraphRuntime(t *testing.T, runtimeHome string, skills []schema.Skill, mcpClient interfaces.MCPClient, clients map[string]interfaces.LLMClient) *Runtime {
	t.Helper()
	cfg := &config.Config{
		Audit:        config.AuditConfig{},
		Session:      config.SessionConfig{MaxHistory: 8},
		RuntimeHome:  runtimeHome,
		DefaultAgent: "chat",
		Providers: map[string]config.LLMConfig{
			"chat":                          {Model: "test-model"},
			"planner":                       {Model: "test-model"},
			"fixer":                         {Model: "test-model"},
			"auditor":                       {Model: "test-model"},
			"primary":                       {Model: "test-model"},
			"backup":                        {Model: "test-model"},
			"software-engineer":             {Model: "test-model"},
			"security-researcher":           {Model: "test-model"},
			"web-security-researcher":       {Model: "test-model"},
			"binary-analyst":                {Model: "test-model"},
			"documentation-specialist":      {Model: "test-model"},
			"operations-specialist":         {Model: "test-model"},
			"support-specialist":            {Model: "test-model"},
			"framework-extension-architect": {Model: "test-model"},
		},
		Agents: map[string]config.AgentProfile{
			"chat":                          {Name: "Chat", Provider: "chat", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"planner":                       {Name: "Planner", Provider: "planner", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"fixer":                         {Name: "Fixer", Provider: "fixer", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"auditor":                       {Name: "Auditor", Provider: "auditor", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"software-engineer":             {Name: "Software Engineer", Provider: "software-engineer", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"security-researcher":           {Name: "Security Researcher", Provider: "security-researcher", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"web-security-researcher":       {Name: "Web Security Researcher", Provider: "web-security-researcher", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"binary-analyst":                {Name: "Binary Analyst", Provider: "binary-analyst", Mode: "audit", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"documentation-specialist":      {Name: "Documentation Specialist", Provider: "documentation-specialist", Mode: "fix", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
			"operations-specialist":         {Name: "Operations Specialist", Provider: "operations-specialist", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"support-specialist":            {Name: "Support Specialist", Provider: "support-specialist", Mode: "chat", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
			"framework-extension-architect": {Name: "Framework Extension Architect", Provider: "framework-extension-architect", Mode: "plan", Model: "test-model", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite}, ToolPolicy: config.ToolPolicyConfirm},
		},
	}
	mergedClients := make(map[string]interfaces.LLMClient, len(cfg.Providers))
	for name, client := range clients {
		mergedClients[name] = client
	}
	for name := range cfg.Providers {
		if _, ok := mergedClients[name]; !ok {
			mergedClients[name] = &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: name}}}}
		}
	}
	runtimeRef, err := NewRuntime(cfg, mergedClients, workflowSkillManager{skills: skills}, mcpClient, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return runtimeRef
}

func workflowGraphTestClients() map[string]interfaces.LLMClient {
	return map[string]interfaces.LLMClient{
		"chat":                          &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "chat"}}}},
		"planner":                       &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "plan"}}}},
		"fixer":                         &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "fix"}}}},
		"auditor":                       &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "audit"}}}},
		"primary":                       &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "primary"}}}},
		"backup":                        &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "backup"}}}},
		"software-engineer":             &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "software"}}}},
		"security-researcher":           &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "security"}}}},
		"web-security-researcher":       &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "web security"}}}},
		"binary-analyst":                &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "binary"}}}},
		"documentation-specialist":      &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "docs"}}}},
		"operations-specialist":         &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "operations"}}}},
		"support-specialist":            &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "support"}}}},
		"framework-extension-architect": &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "platform"}}}},
	}
}
