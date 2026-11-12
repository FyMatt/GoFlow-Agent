package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

func TestWorkflowStageCapabilitiesAreDeclarative(t *testing.T) {
	for _, name := range []string{"unknown-domain", "complex-project-delivery", "plan-implement-audit", "docs-review-publish"} {
		graph := workflowGraph{Name: name}
		stage := workflowGraphStage{Name: "custom-step"}
		if workflowGraphShouldEnforceWorkspaceMutation(graph, stage) ||
			(&WorkflowRunner{}).workflowGraphStageRequiresChangeSlices(graph, stage) ||
			workflowGraphShouldSeedPlanIntake(graph, stage) || workflowGraphStageUsesChangeSliceScheduler(graph, stage) {
			t.Fatalf("%s enables undeclared capabilities", name)
		}
		stage.Approval = true
		stage.Params = map[string]string{
			"require_workspace_mutation": "true", "change_slice_policy": "required",
			"seed_plan_intake": "true", "use_change_slice_scheduler": "true", "approved_plan_scope": "true",
		}
		if !workflowGraphShouldEnforceWorkspaceMutation(graph, stage) ||
			!(&WorkflowRunner{}).workflowGraphStageRequiresChangeSlices(graph, stage) ||
			!workflowGraphShouldSeedPlanIntake(graph, stage) || !workflowGraphStageUsesChangeSliceScheduler(graph, stage) {
			t.Fatalf("%s ignores declared capabilities", name)
		}
	}
}

func TestWorkflowStageCapabilitiesRejectInvalidDeclarations(t *testing.T) {
	for _, params := range []map[string]string{
		{"require_workspace_mutation": "yes"}, {"seed_plan_intake": "1"},
		{"use_change_slice_scheduler": "invalid"}, {"change_slice_policy": "sometimes"},
		{"approved_plan_scope": "true"}, {"use_change_slice_scheduler": "true"},
		{"verification_mode": "unknown"}, {"verification_mode": "tool"},
		{"verification_any_tools": "file_tools/read_file"},
		{"verification_mode": "command", "verification_any_tools": "file_tools/read_file"},
		{"verification_mode": "tool", "verification_any_tools": " , ; "},
		{"verification_mode": "tool", "verification_any_tools": "read_file"},
		{"verification_mode": "tool", "verification_any_tools": "file_tools/"},
	} {
		graph := workflowGraph{Name: "unknown-domain", Stages: []workflowGraphStage{{Name: "work", Agent: "fixer", Skill: "code-writing", Params: params}}}
		if err := validateWorkflowGraph(graph.Name, graph); err == nil {
			t.Fatalf("accepted invalid capabilities: %#v", params)
		}
	}
}

func TestToolVerificationAcceptsOneDeclaredAlternative(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", NodeType: "quality_gate", Params: map[string]string{
		"require_verification": "true", "verification_mode": "tool", "min_score": "85",
		"verification_any_tools": "file_tools/read_file,web_tools/browser_snapshot,web_tools/research_snapshot,ssh_tools/ssh_run_command",
	}}
	if err := validateWorkflowGraphStageCapabilities("unknown-domain", stage); err != nil {
		t.Fatal(err)
	}
	if len(workflowGraphStageAllowedTools(stage)) != 0 {
		t.Fatal("declared evidence alternatives must not grant tools")
	}
	for _, name := range workflowGraphQualityToolList(stage.Params["verification_any_tools"]) {
		completed := []WorkflowStageResult{{Stage: "collect", Status: "completed", Result: schema.AgentResult{
			ToolResults: []schema.ToolResult{{CallID: "source-1", ToolName: name, Content: "collected evidence"}},
		}}}
		if got := evaluateWorkflowGraphQualityGate(stage, completed); !got.Passed || got.VerificationPassed == 0 || got.EvidenceArtifacts == 0 {
			t.Fatalf("one actual alternative must suffice without software commands (%s): %#v", name, got)
		}
		stage.Params["verification_tools"] = "botany/check_specimen"
		if got := evaluateWorkflowGraphQualityGate(stage, completed); got.Passed || !strings.Contains(got.Reason, "botany/check_specimen") {
			t.Fatalf("alternatives must not weaken all-required validators: %#v", got)
		}
		completed[0].Result.ToolResults = append(completed[0].Result.ToolResults, schema.ToolResult{
			CallID: "validator", ToolName: "botany/check_specimen", Content: "validated evidence",
		})
		if got := evaluateWorkflowGraphQualityGate(stage, completed); !got.Passed {
			t.Fatalf("all-required plus one alternative must pass: %#v", got)
		}
		delete(stage.Params, "verification_tools")
	}
	for name, result := range map[string]schema.AgentResult{
		"none":            {},
		"fake-report":     {Output: "file_tools/read_file completed successfully.", Verification: []schema.Verification{{Kind: "tool", Status: "passed"}}},
		"missing-call-id": {ToolResults: []schema.ToolResult{{ToolName: "file_tools/read_file", Content: "evidence"}}},
		"empty":           {ToolResults: []schema.ToolResult{{CallID: "empty", ToolName: "file_tools/read_file"}}},
		"failed":          {ToolResults: []schema.ToolResult{{CallID: "failed", ToolName: "file_tools/read_file", Content: "missing input", IsError: true}}},
		"denied":          {ToolResults: []schema.ToolResult{{CallID: "denied", ToolName: "web_tools/research_snapshot", Content: "denied", Denied: true}}},
		"suspended":       {ToolResults: []schema.ToolResult{{CallID: "pending", ToolName: "ssh_tools/ssh_run_command", Content: "pending", Suspended: true}}},
		"wrong-server":    {ToolResults: []schema.ToolResult{{CallID: "other", ToolName: "untrusted/read_file", Content: "evidence"}}},
		"unqualified":     {ToolResults: []schema.ToolResult{{CallID: "ambiguous", ToolName: "read_file", Content: "evidence"}}},
	} {
		if got := evaluateWorkflowGraphQualityGate(stage, []WorkflowStageResult{{Stage: "collect", Status: "completed", Result: result}}); got.Passed || !strings.Contains(got.Reason, "at least one successful tool result") {
			t.Fatalf("%s must fail alternatives check: %#v", name, got)
		}
	}
}

func TestToolVerificationRetainsResolvedServerIdentity(t *testing.T) {
	for _, name := range []string{"check_specimen", "botany/check_specimen"} {
		for _, returnedName := range []string{"check_specimen", "untrusted/check_specimen"} {
			tool := schema.Tool{Name: name, Server: "botany", Kind: "read"}
			mcp := &stubRuntimeMCP{result: schema.ToolResult{ToolName: returnedName, Content: "observed specimen"}}
			result, _, err := executeToolCallWithIdempotency(context.Background(), mcp, ExecutionContext{}, schema.ToolCall{ID: "observed-1", Name: name, Arguments: json.RawMessage(`{}`)}, tool)
			if err != nil || result.ToolName != "botany/check_specimen" || result.CallID != "observed-1" {
				t.Fatalf("evidence must identify the resolved tool (%s, %s): %#v, %v", name, returnedName, result, err)
			}
		}
	}
}

func TestLegacyVerificationDefaultsToExecutedCommands(t *testing.T) {
	for _, key := range []string{"require_verification", "verification_required"} {
		stage := workflowGraphStage{Name: "quality", NodeType: "quality_gate", Params: map[string]string{key: "true"}}
		if err := validateWorkflowGraphStageCapabilities("legacy-workflow", stage); err != nil {
			t.Fatal(err)
		}
		completed := []WorkflowStageResult{{Stage: "verify", Status: "completed", Result: schema.AgentResult{
			ToolResults:  []schema.ToolResult{{CallID: "read", ToolName: "read_file", Content: "package main"}},
			Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "Tests passed."}},
		}}}
		if got := evaluateWorkflowGraphQualityGate(stage, completed); got.Passed || !strings.Contains(got.Reason, "executed test") {
			t.Fatalf("legacy declaration must require executed checks: %#v", got)
		}
		completed[0].Result.ToolResults = append(completed[0].Result.ToolResults, schema.ToolResult{
			CallID: "tests", ToolName: "command_tools/run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`,
		})
		if got := evaluateWorkflowGraphQualityGate(stage, completed); !got.Passed {
			t.Fatalf("legacy declaration must accept executed checks: %#v", got)
		}
	}
}

func TestResourcePlanHelperPreservesToolPermissionsAndInternet(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfigured", true: "declared"}[enabled], func(t *testing.T) {
			client := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
				{ToolCalls: []schema.ToolCall{
					{ID: "forbidden-write", Name: "file_tools/write_file", Arguments: json.RawMessage(`{"path":"report.md","content":"fabricated"}`)},
					{ID: "search", Name: "web_tools/web_search", Arguments: json.RawMessage(`{"query":"current documentation"}`)},
					{ID: "fetch", Name: "web_tools/fetch_url", Arguments: json.RawMessage(`{"url":"https://docs.example.test"}`)},
					{ID: "snapshot", Name: "web_tools/research_snapshot", Arguments: json.RawMessage(`{"urls":["https://docs.example.test"]}`)},
				}},
				{Message: schema.Message{Content: "Plan based on fetched documentation."}},
			}}
			mcp := &stubRuntimeMCP{tools: []schema.Tool{
				{Name: "write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object"}`)},
				{Name: "web_search", Server: "web_tools", Kind: "network", InputSchema: json.RawMessage(`{"type":"object"}`)},
				{Name: "fetch_url", Server: "web_tools", Kind: "network", InputSchema: json.RawMessage(`{"type":"object"}`)},
				{Name: "research_snapshot", Server: "web_tools", Kind: "network", InputSchema: json.RawMessage(`{"type":"object"}`)},
			}, result: schema.ToolResult{Content: `{"source":"documentation"}`}}
			runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), mcp, map[string]interfaces.LLMClient{"planner": client})
			runtimeRef.runners["planner"].profile.AllowedToolKinds = []config.ToolKind{config.ToolKindRead, config.ToolKindNetwork}
			runtimeRef.runners["planner"].profile.AllowedTools = []string{"web_tools/web_search", "web_tools/fetch_url", "web_tools/research_snapshot"}
			runner := runtimeRef.WorkflowRunner()
			runner.runID = runtimeRef.session.StartWorkflowRun("unknown-domain", "Plan a task using current documentation.")
			stage := workflowGraphStage{Name: "custom-plan", Agent: "planner", Skill: "execution-plan", Params: map[string]string{
				"tools": "file_tools/write_file,web_tools/web_search,web_tools/fetch_url,web_tools/research_snapshot", "max_iterations": "4",
			}}
			if enabled {
				stage.Params["seed_plan_intake"] = "true"
			}
			var events []schema.StreamEvent
			result, _, err := runner.runWorkflowGraphExecutableStage(context.Background(), workflowGraph{Name: "unknown-domain"}, stage, "Plan using current documentation.", schema.Skill{Name: "execution-plan"}, func(event schema.StreamEvent) error {
				events = append(events, event)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if mcp.calls != 3 || len(result.ToolResults) != 4 || !result.ToolResults[0].Denied {
				t.Fatalf("helper or stage declaration bypassed permissions: calls=%d, results=%#v", mcp.calls, result.ToolResults)
			}
			for _, request := range client.requests {
				if len(request.Tools) != 3 {
					t.Fatalf("expected public documentation tools only, got %#v", request.Tools)
				}
			}
			visibleHelper := false
			for _, event := range events {
				visibleHelper = visibleHelper || event.Reason == "resource_helper" && event.SourceRef == "params.seed_plan_intake"
			}
			if visibleHelper != enabled || strings.Contains(result.Output, "交付档位") != enabled {
				t.Fatalf("helper must run visibly only when declared: events=%#v, output=%q", events, result.Output)
			}
			persistedHelper := false
			for _, section := range result.Structured {
				persistedHelper = persistedHelper || section.Kind == "resource_helper" && strings.Contains(section.Summary, "not model or tool evidence")
			}
			if persistedHelper != enabled {
				t.Fatalf("helper provenance must survive persistence: %#v", result.Structured)
			}
		})
	}
}

func TestWorkflowTemplatesRetainPublicDocumentationTools(t *testing.T) {
	for name, stages := range map[string][]string{
		"operations-runbook":          {"prepare", "verify"},
		"complex-project-delivery":    {"plan", "verify", "recovery-verify"},
		"plan-implement-audit":        {"clarify", "plan", "verify", "recovery-verify"},
		"plan-fix-audit":              {"plan", "verify"},
		"repository-readiness-review": {"inventory", "build-review", "test-review", "configuration-review"},
		"software-quality-gate":       {"plan", "verify"},
	} {
		template, ok := (&WorkflowRunner{}).WorkflowTemplate(name)
		if !ok {
			t.Fatalf("missing template %s", name)
		}
		graph := template.Graph.toInternalGraph()
		for _, stageName := range stages {
			found := false
			for _, stage := range graph.Stages {
				if stage.Name != stageName {
					continue
				}
				found = true
				allowed := workflowGraphStageAllowedTools(stage)
				if len(allowed) > 0 {
					for _, required := range []string{"web_tools/web_search", "web_tools/fetch_url", "web_tools/research_snapshot"} {
						if !containsString(allowed, required) {
							t.Fatalf("%s/%s excludes %s", name, stageName, required)
						}
					}
				}
				for _, required := range []string{"source URL", "product/version", "fetched_at", "local-only", "Never upload private logs", "do not authorize target scanning"} {
					if !strings.Contains(stage.Params["docs_policy"], required) {
						t.Fatalf("%s/%s missing documentation boundary %q", name, stageName, required)
					}
				}
				prompt := buildWorkflowGraphStagePrompt(graph, stage, "Use local-only evidence.", nil)
				if !strings.Contains(prompt, "local-only") || !strings.Contains(prompt, "Never upload private logs") {
					t.Fatalf("%s/%s lost documentation policy in prompt", name, stageName)
				}
			}
			if !found {
				t.Fatalf("%s missing %s", name, stageName)
			}
		}
	}
}

func TestWorkspaceMutationRequiresStageEvidence(t *testing.T) {
	if workflowGraphResultHasWorkspaceMutation(schema.AgentResult{Changes: []schema.Change{{Files: []string{"claimed.go"}}}}) {
		t.Fatal("reported changes are not executed mutations")
	}
	client := &workflowRecordingLLMClient{responses: []schema.ChatResponse{
		{Message: schema.Message{Content: "No files changed."}},
	}}
	runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"fixer": client})
	runner := runtimeRef.WorkflowRunner()
	runner.snapshotWorkspaceMutationBaseline()
	if err := os.WriteFile(filepath.Join(runtimeRef.cfg.WorkspaceRoot, "previous-stage.txt"), []byte("Earlier stage output."), 0o644); err != nil {
		t.Fatal(err)
	}
	stage := workflowGraphStage{Name: "custom-write", Agent: "fixer", Params: map[string]string{"require_workspace_mutation": "true"}}
	_, _, err := runner.runWorkflowGraphExecutableStage(context.Background(), workflowGraph{Name: "unknown-domain"}, stage, "Write the requested file.", schema.Skill{Name: "code-writing"}, nil)
	if err == nil || !strings.Contains(err.Error(), "produced no workspace file mutations") {
		t.Fatalf("earlier stage writes must not satisfy this stage: %v", err)
	}
}

func TestUnknownDomainReadOnlyValidatorSatisfiesToolVerification(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", NodeType: "quality_gate", Params: map[string]string{
		"require_verification": "true", "verification_mode": "tool", "verification_tools": "botany/check_specimen", "min_score": "85",
	}}
	graph := workflowGraph{Name: "botanical-observation", Stages: []workflowGraphStage{stage}}
	if err := validateWorkflowGraph(graph.Name, graph); err != nil {
		t.Fatal(err)
	}
	completed := []WorkflowStageResult{{Stage: "observe", Status: "completed", Result: schema.AgentResult{
		ToolResults: []schema.ToolResult{{CallID: "observation-1", ToolName: "botany/check_specimen", Content: `{"observed":true,"samples":3}`}},
	}}}
	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.VerificationPassed == 0 || evaluation.EvidenceArtifacts == 0 {
		t.Fatalf("declared read-only validator requires no software command: %#v", evaluation)
	}
	stage.Params["verification_mode"] = "command"
	evaluation = evaluateWorkflowGraphQualityGate(stage, completed)
	if evaluation.Passed || !strings.Contains(evaluation.Reason, "executed test") {
		t.Fatalf("command verification must retain its executed-check prerequisite: %#v", evaluation)
	}
}

func TestToolVerificationRejectsFakeAndUnsuccessfulEvidence(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"require_verification": "true", "verification_mode": "tool", "verification_tools": "botany/check_specimen",
	}}
	for _, result := range []schema.AgentResult{
		{Output: "botany/check_specimen ran and passed", Verification: []schema.Verification{{Kind: "tool", Status: "passed"}}},
		{ToolResults: []schema.ToolResult{{ToolName: "botany/check_specimen", Content: "passed"}}},
		{ToolResults: []schema.ToolResult{{CallID: "empty", ToolName: "botany/check_specimen"}}},
		{ToolResults: []schema.ToolResult{{CallID: "failed", ToolName: "botany/check_specimen", Content: "failed", IsError: true}}},
		{ToolResults: []schema.ToolResult{{CallID: "denied", ToolName: "botany/check_specimen", Content: "denied", Denied: true}}},
		{ToolResults: []schema.ToolResult{{CallID: "pending", ToolName: "botany/check_specimen", Content: "pending", Suspended: true}}},
		{ToolResults: []schema.ToolResult{{CallID: "other-server", ToolName: "untrusted/check_specimen", Content: "passed"}}},
	} {
		evaluation := evaluateWorkflowGraphQualityGate(stage, []WorkflowStageResult{{Stage: "observe", Status: "completed", Result: result}})
		if evaluation.Passed {
			t.Fatalf("fake or unsuccessful validator evidence passed: %#v", result)
		}
	}
	stage.Params["required_tools"] = "botany/check_habitat"
	completed := []WorkflowStageResult{{Stage: "observe", Status: "completed", Result: schema.AgentResult{ToolResults: []schema.ToolResult{
		{CallID: "specimen", ToolName: "botany/check_specimen", Content: "observed"},
	}}}}
	if evaluation := evaluateWorkflowGraphQualityGate(stage, completed); evaluation.Passed || !strings.Contains(evaluation.Reason, "check_habitat") {
		t.Fatalf("verification_tools and required_tools must both be required: %#v", evaluation)
	}
	completed[0].Result.ToolResults = append(completed[0].Result.ToolResults, schema.ToolResult{CallID: "habitat", ToolName: "botany/check_habitat", Content: "observed"})
	if evaluation := evaluateWorkflowGraphQualityGate(stage, completed); !evaluation.Passed {
		t.Fatalf("all declared validators should pass: %#v", evaluation)
	}
}

func TestUnknownReadOnlyWorkflowRunsDeclaredValidator(t *testing.T) {
	for _, verificationParam := range []string{"verification_tools", "verification_any_tools"} {
		t.Run(verificationParam, func(t *testing.T) {
			for _, callValidator := range []bool{true, false} {
				t.Run(map[bool]string{true: "actual-tool", false: "fake-report"}[callValidator], func(t *testing.T) {
					home := t.TempDir()
					writeWorkflowGraphYAML(t, home, "botanical-observation", `name: botanical-observation
stages:
  - name: observe
    agent: auditor
    skill: code-audit
    params:
      tools: botany/check_specimen
      require_workspace_mutation: "false"
    next: [quality]
  - name: quality
    node_type: quality_gate
    params:
      stages: observe
      require_verification: "true"
      verification_mode: tool
      `+verificationParam+`: botany/check_specimen
      min_score: "85"
    routes: {pass: end, fail: end, warning: end}
  - name: end
    node_type: end
`)
					responses := []schema.ChatResponse{{Message: schema.Message{Content: "The specimen validator passed."}}}
					if callValidator {
						responses = append([]schema.ChatResponse{{ToolCalls: []schema.ToolCall{{ID: "specimen-1", Name: "botany/check_specimen", Arguments: json.RawMessage(`{}`)}}}}, responses...)
					}
					client := &workflowRecordingLLMClient{responses: responses}
					mcp := &stubRuntimeMCP{tools: []schema.Tool{{Name: "check_specimen", Server: "botany", Kind: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}}, result: schema.ToolResult{Content: `{"observed":true,"samples":3}`}}
					runtimeRef := newWorkflowGraphRuntime(t, home, workflowGraphTestSkills(), mcp, map[string]interfaces.LLMClient{"auditor": client})
					result, err := runtimeRef.WorkflowRunner().Run(context.Background(), "botanical-observation", "Observe without modifying the workspace.", false, nil)
					if err != nil {
						t.Fatal(err)
					}
					quality := workflowGraphStageResultByName(t, result.CompletedStages, "quality")
					if callValidator {
						if quality.Output.Variables["route"] != "pass" || mcp.calls != 1 {
							t.Fatalf("read-only validator failed: %#v, calls=%d", quality, mcp.calls)
						}
					} else if quality.Output.Variables["route"] != "fail" || mcp.calls != 0 {
						t.Fatalf("report-only verification passed: %#v, calls=%d", quality, mcp.calls)
					}
				})
			}
		})
	}
}

func TestUnknownWorkflowPlanScopeWaitsForUserApproval(t *testing.T) {
	home := t.TempDir()
	writeWorkflowGraphYAML(t, home, "unknown-domain", `name: unknown-domain
stages:
  - name: custom-step
    agent: fixer
    skill: code-writing
    approval: true
    params:
      approved_plan_scope: "true"
`)
	client := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Approved work complete."}}}}
	runtimeRef := newWorkflowGraphRuntime(t, home, workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"fixer": client})
	runner := runtimeRef.WorkflowRunner()
	result, err := runner.Run(context.Background(), "unknown-domain", "Perform bounded local work.", false, nil)
	if err != nil || result.Status != "awaiting_approval" || len(client.requests) != 0 {
		t.Fatalf("resource granted authority before approval: %#v, requests=%d, err=%v", result, len(client.requests), err)
	}
	result, err = runner.ResumeApproval(context.Background(), result.RunID, nil)
	if err != nil || result.Status != "completed" || len(client.requests) != 1 {
		t.Fatalf("approved custom workflow did not execute: %#v, requests=%d, err=%v", result, len(client.requests), err)
	}
}

func TestExistingWorkflowYAMLCopiesDeclareStageCapabilities(t *testing.T) {
	paths, err := filepath.Glob("templates/workflows/*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"../../templates/workflows/*.yaml", "../../workflows/*/workflow.yaml"} {
		copies, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, copies...)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var resource struct {
			Graph workflowGraph `yaml:"graph"`
		}
		if err := yaml.Unmarshal(data, &resource); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		graph := resource.Graph
		if graph.Name == "" {
			if err := yaml.Unmarshal(data, &graph); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}
		switch graph.Name {
		case "agent-framework-extension", "authorized-red-team-validation", "binary-triage", "complex-project-delivery", "customer-support-triage", "docs-review-publish", "multi-domain-intake-router", "operations-runbook", "parallel-research-review", "plan-fix-audit", "plan-implement-audit", "repository-readiness-review", "security-audit-evidence-gate", "software-quality-gate", "software-team-review-gate", "task-decomposition-plan", "web-research-risk", "web-research-risk-copy", "skill-chain":
		default:
			continue
		}
		if err := validateWorkflowGraph(graph.Name, graph); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, stage := range graph.Stages {
			if stage.Agent == "" || stage.Skill == "" || isControlWorkflowNode(stage) || isVisualOnlyWorkflowNode(stage) {
				continue
			}
			if _, declared := stage.Params["require_workspace_mutation"]; !declared {
				t.Fatalf("%s stage %s has no explicit mutation contract", path, stage.Name)
			}
		}
	}
}

func TestWorkflowGraphDoesNotWriteImplicitDomainArtifacts(t *testing.T) {
	for _, name := range []string{"unknown-domain", "complex-project-delivery", "docs-review-publish", "parallel-research-review"} {
		t.Run(name, func(t *testing.T) {
			client := &workflowRecordingLLMClient{responses: []schema.ChatResponse{{Message: schema.Message{Content: "Summary only; no tool was called."}}}}
			runtimeRef := newWorkflowGraphRuntime(t, t.TempDir(), workflowGraphTestSkills(), &stubRuntimeMCP{}, map[string]interfaces.LLMClient{"fixer": client})
			root := runtimeRef.cfg.WorkspaceRoot
			for _, rel := range []string{"input/source.txt", "inputs/request.json", "source-catalog.json", "internal/cache/cache.go", "web/app.js"} {
				path := filepath.Join(root, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("preserve original\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, item := range officeTemplateDeliverables {
				path := filepath.Join(root, filepath.FromSlash(item.template))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				data, err := zipWithXML("word/document.xml", `<w:t>{{CLIENT_NAME}} {{REPORT_TITLE}} {{REPORT_DATE}}</w:t>`)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			runner := runtimeRef.WorkflowRunner()
			before := runner.workspaceMutationFingerprint()
			runner.runID = runtimeRef.session.StartWorkflowRun(name, "Create deliverables/report.md, deliverables/report.docx and deliverables/report.pdf from templates, repair public tests and web styling.")
			stage := workflowGraphStage{Name: "report", Agent: "fixer", Skill: "code-writing"}
			if name == "docs-review-publish" {
				stage.Name = "produce"
			}
			for _, params := range []map[string]string{nil, {"require_workspace_mutation": "true"}} {
				stage.Params = params
				_, _, err := runner.runWorkflowGraphExecutableStage(context.Background(), workflowGraph{Name: name}, stage, "Produce the requested files.", schema.Skill{Name: "code-writing"}, nil)
				if params == nil && err != nil {
					t.Fatalf("unconfigured stage: %v", err)
				}
				if params != nil && (err == nil || !strings.Contains(err.Error(), "produced no workspace file mutations")) {
					t.Fatalf("declared mutation must fail without actual changes: %v", err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "deliverables")); !os.IsNotExist(err) {
				t.Fatalf("implicit deliverables created: %v", err)
			}
			for _, rel := range []string{"internal/cache/cache.go", "web/app.js"} {
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
				if err != nil || strings.TrimSpace(string(data)) != "preserve original" {
					t.Fatalf("implicit repair of %s: %s (%v)", rel, data, err)
				}
			}
			if after := runner.workspaceMutationFingerprint(); !reflect.DeepEqual(before, after) {
				t.Fatalf("workflow changed workspace without an explicit tool action: before=%#v, after=%#v", before, after)
			}
		})
	}
}
