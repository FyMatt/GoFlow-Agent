package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestQualityGateCountsPythonCompilationAndRecoveredExactPatch(t *testing.T) {
	for _, target := range []string{"static/style.css", "static/other.css"} {
		t.Run(target, func(t *testing.T) {
			stage := workflowGraphStage{Name: "quality", Params: map[string]string{"require_verification": "true", "verification_mode": "command"}}
			completed := []WorkflowStageResult{{Stage: "implement", Status: "completed", Result: schema.AgentResult{
				ToolResults: []schema.ToolResult{
					{CallID: "patch-failed", ToolName: "apply_patch", IsError: true, Content: `{"relative_path":"static/style.css","error":"edit 1 expected 1 occurrence(s) but found 0; no changes were written"}`},
					{CallID: "patch-passed", ToolName: "write_file", Content: `{"relative_path":"` + target + `","new_sha256":"changed"}`},
					{CallID: "compile", ToolName: "run_command", Content: `{"program":"python","args":["-m","py_compile","app.py"],"exit_code":0}`},
				},
				Verification: []schema.Verification{{Kind: "tool", Status: "failed", Detail: "apply_patch: edit 1 expected 1 occurrence(s) but found 0; no changes were written"}, {Kind: "command", Status: "passed", Detail: "python -m py_compile app.py"}},
			}}}
			got := evaluateWorkflowGraphQualityGate(stage, completed)
			if target == "static/style.css" {
				if !got.Passed || got.ToolErrors != 0 || got.VerificationFailed != 0 || got.RecoveredToolErrors != 1 {
					t.Fatalf("recovered patch and compile should pass: %#v", got)
				}
			} else if got.Passed || got.ToolErrors != 1 {
				t.Fatalf("writing another file must not recover a failed patch: %#v", got)
			}
		})
	}
}

func TestWorkflowQualityGateRejectsTextOnlyReportEvidence(t *testing.T) {
	stage := workflowGraphStage{
		Name: "quality",
		Params: map[string]string{
			"require_acceptance":   "true",
			"require_verification": "true", "verification_mode": "command",
			"require_evidence": "true",
			"min_score":        "85",
		},
	}
	completed := []WorkflowStageResult{{
		Stage:  "implement",
		Status: "completed",
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
			Name: "heading contract", Status: "passed", Actual: "# Verification",
		}},
		Result: schema.AgentResult{
			Output:       "# Verification\nAll tests pass.",
			Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "tests pass"}},
		},
		Output: WorkflowStageOutput{Artifacts: []session.WorkflowRunArtifact{{
			Kind: "report", Title: "Verification report", Content: "# Verification\nAll tests pass.",
		}}},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if evaluation.Passed || evaluation.Status != "failed" {
		t.Fatalf("expected text-only evidence to fail, got %#v", evaluation)
	}
	if evaluation.EvidenceArtifacts != 0 || evaluation.Score > 70 {
		t.Fatalf("expected no strong evidence and a capped score, got %#v", evaluation)
	}
	if !strings.Contains(evaluation.Reason, "tool-backed") {
		t.Fatalf("expected strong-evidence failure reason, got %q", evaluation.Reason)
	}
}

func TestWorkflowQualityGateAcceptsTraceableToolEvidence(t *testing.T) {
	stage := workflowGraphStage{
		Name: "quality",
		Params: map[string]string{
			"require_acceptance":   "true",
			"require_verification": "true", "verification_mode": "command",
			"require_evidence": "true",
			"min_score":        "85",
		},
	}
	completed := []WorkflowStageResult{{
		Stage:  "implement",
		Status: "completed",
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
			Name: "behavior", Status: "passed", Actual: "verified",
		}},
		Result: schema.AgentResult{
			Output:       "implemented",
			Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
		},
		Output: WorkflowStageOutput{Artifacts: []session.WorkflowRunArtifact{{
			Kind: "tool_result", ToolName: "run_command", ToolCallID: "test-1", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`,
		}}},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.Status != "passed" || evaluation.EvidenceArtifacts != 1 || evaluation.Score < 85 {
		t.Fatalf("expected traceable tool evidence to pass, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateAcceptsSuccessfulResultToolEvidence(t *testing.T) {
	stage := workflowGraphStage{
		Name: "quality",
		Params: map[string]string{
			"require_acceptance":   "true",
			"require_verification": "true", "verification_mode": "command",
			"require_evidence": "true",
			"min_score":        "85",
		},
	}
	completed := []WorkflowStageResult{{
		Stage:  "test",
		Status: "completed",
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
			Name: "tests", Status: "passed", Actual: "verified",
		}},
		Result: schema.AgentResult{
			Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
			ToolResults:  []schema.ToolResult{{CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`}},
		},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.EvidenceArtifacts != 1 || evaluation.ToolErrors != 0 || evaluation.Score < 85 {
		t.Fatalf("expected successful command result to satisfy strong evidence, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateRequiresNamedSuccessfulToolResults(t *testing.T) {
	stage := workflowGraphStage{
		Name: "quality",
		Params: map[string]string{
			"required_tools":   "office_tools/inspect_office_document,validate_office_document",
			"require_evidence": "true",
			"min_score":        "70",
		},
	}
	completed := []WorkflowStageResult{{
		Stage:  "validate",
		Status: "completed",
		Result: schema.AgentResult{ToolResults: []schema.ToolResult{{
			CallID: "inspect-1", ToolName: "office_tools/inspect_office_document", Content: `{"valid":true}`,
		}}},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if evaluation.Passed || !strings.Contains(evaluation.Reason, "validate_office_document") {
		t.Fatalf("expected missing named validation tool to fail, got %#v", evaluation)
	}

	completed[0].Result.ToolResults = append(completed[0].Result.ToolResults, schema.ToolResult{
		CallID: "validate-1", ToolName: "validate_office_document", Content: `{"valid":true}`,
	})
	evaluation = evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.Score < 70 {
		t.Fatalf("expected qualified and unqualified named tool evidence to pass, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateRejectsExplicitFailureOutputMarker(t *testing.T) {
	stage := workflowGraphStage{
		Name: "quality",
		Params: map[string]string{
			"failure_output_markers": "GOFLOW_QUALITY=FAIL",
			"require_evidence":       "true",
		},
	}
	completed := []WorkflowStageResult{{
		Stage:  "validate",
		Status: "completed",
		Result: schema.AgentResult{
			Output:      "GOFLOW_QUALITY=FAIL\n发现输入源之外的事实。",
			ToolResults: []schema.ToolResult{{CallID: "validate-1", ToolName: "validate_office_document", Content: `{"valid":true}`}},
		},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if evaluation.Passed || !strings.Contains(evaluation.Reason, "quality failure marker") {
		t.Fatalf("expected explicit failure marker to block quality gate, got %#v", evaluation)
	}

	completed[0].Result.Output = "GOFLOW_QUALITY=PASS\n输入事实和产物一致。"
	evaluation = evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed {
		t.Fatalf("expected pass marker not to block quality gate, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateRejectsFailedCommandResultEvidence(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{"require_evidence": "true", "min_score": "85"}}
	completed := []WorkflowStageResult{{
		Stage:  "test",
		Status: "completed",
		Result: schema.AgentResult{ToolResults: []schema.ToolResult{{
			CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":1}`,
		}}},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if evaluation.Passed || evaluation.ToolErrors != 1 || evaluation.EvidenceArtifacts != 0 {
		t.Fatalf("expected failed command result to block quality, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateAllowsUnavailableGoRaceWithSuccessfulFallback(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"require_acceptance":   "true",
		"require_verification": "true", "verification_mode": "command",
		"require_evidence":                "true",
		"min_score":                       "85",
		"allowed_infrastructure_failures": "go_race_unavailable",
	}}
	completed := []WorkflowStageResult{{
		Stage:  "verify",
		Status: "completed",
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
			Name: "verification-report", Status: "passed", Actual: "fallback recorded",
		}},
		Result: schema.AgentResult{
			Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "ordinary tests passed; race deferred to CI"}},
			ToolResults: []schema.ToolResult{
				{CallID: "race-1", ToolName: "run_command", IsError: true, Content: `{"program":"go","args":["test","-race","./..."],"exit_code":2,"stderr":"go: -race requires cgo; enable cgo by setting CGO_ENABLED=1","timed_out":false,"cancelled":false}`},
				{CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
			},
		},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.Status != "passed" || evaluation.ToolErrors != 0 || evaluation.InfrastructureLimits != 1 {
		t.Fatalf("expected explicit race infrastructure limit with successful fallback to pass, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateRejectsUnavailableGoRaceWithoutSuccessfulFallback(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"require_evidence":                "true",
		"allowed_infrastructure_failures": "go_race_unavailable",
	}}
	completed := []WorkflowStageResult{{
		Stage:  "verify",
		Status: "completed",
		Result: schema.AgentResult{ToolResults: []schema.ToolResult{{
			CallID: "race-1", ToolName: "run_command", Content: `{"program":"go","args":["test","-race","./..."],"exit_code":2,"stderr":"go: -race requires cgo; enable cgo by setting CGO_ENABLED=1"}`,
		}}},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if evaluation.Passed || evaluation.ToolErrors != 1 || evaluation.InfrastructureLimits != 0 {
		t.Fatalf("expected missing ordinary-test fallback to keep race failure blocking, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateDoesNotWaiveGenericFailedGoTest(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"allowed_infrastructure_failures": "go_race_unavailable",
	}}
	completed := []WorkflowStageResult{{
		Stage:  "verify",
		Status: "completed",
		Result: schema.AgentResult{ToolResults: []schema.ToolResult{
			{CallID: "race-1", ToolName: "run_command", Content: `{"program":"go","args":["test","-race","./..."],"exit_code":1,"stderr":"cache test failed"}`},
			{CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
		}},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if evaluation.Passed || evaluation.ToolErrors != 1 || evaluation.InfrastructureLimits != 0 {
		t.Fatalf("expected a real race-test failure to remain blocking, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateAcceptsExactCommandFailureRecoveredByLaterSuccess(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"require_acceptance":   "true",
		"require_verification": "true", "verification_mode": "command",
		"require_evidence": "true",
		"min_score":        "85",
	}}
	completed := []WorkflowStageResult{
		{
			Stage:  "implement",
			Status: "completed",
			Result: schema.AgentResult{ToolResults: []schema.ToolResult{
				{CallID: "test-failed", ToolName: "command_tools/run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":1,"stderr":"bad import"}`},
			}},
		},
		{
			Stage:  "verify",
			Status: "completed",
			Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
				Name: "regression", Status: "passed", Actual: "fixed import verified",
			}},
			Result: schema.AgentResult{
				Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
				ToolResults: []schema.ToolResult{
					{CallID: "test-passed", ToolName: "command_tools/run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
				},
			},
		},
	}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.ToolErrors != 0 || evaluation.RecoveredToolErrors != 1 {
		t.Fatalf("expected exact later command success to recover the historical failure, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateDoesNotRecoverFailureWithDifferentCommand(t *testing.T) {
	stage := workflowGraphStage{Name: "quality"}
	completed := []WorkflowStageResult{{
		Stage:  "verify",
		Status: "completed",
		Result: schema.AgentResult{ToolResults: []schema.ToolResult{
			{CallID: "focused-failed", ToolName: "run_command", Content: `{"program":"go","args":["test","./cache"],"exit_code":1}`},
			{CallID: "other-passed", ToolName: "run_command", Content: `{"program":"go","args":["test","./other"],"exit_code":0}`},
		}},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if evaluation.Passed || evaluation.ToolErrors != 1 || evaluation.RecoveredToolErrors != 0 {
		t.Fatalf("expected a different passing command not to recover the failed check, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateAcceptsReadPathFailureRecoveredBySameFile(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"require_acceptance":   "true",
		"require_verification": "true", "verification_mode": "command",
		"require_evidence": "true",
		"min_score":        "85",
	}}
	completed := []WorkflowStageResult{{
		Stage:  "audit",
		Status: "completed",
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
			Name: "audit", Status: "passed", Actual: "repository root file inspected",
		}},
		Result: schema.AgentResult{
			Verification: []schema.Verification{
				{Kind: "tool", Status: "failed", Detail: "read_file path not found"},
				{Kind: "tests", Status: "passed", Detail: "go test ./..."},
			},
			ToolResults: []schema.ToolResult{
				{CallID: "wrong-path", ToolName: "read_file", Content: `read_file path not found: C:\repo\pkg\go.mod`, IsError: true},
				{CallID: "root-file", ToolName: "read_file", Content: `{"path":"C:\\repo\\go.mod","relative_path":"go.mod","content":"module example"}`},
				{CallID: "test", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
			},
		},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.ToolErrors != 0 || evaluation.RecoveredToolErrors != 1 || evaluation.VerificationFailed != 0 {
		t.Fatalf("expected recovered read path error not to block quality, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateRecordsPolicyInterventionWithoutProductFailure(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"require_acceptance":   "true",
		"require_verification": "true", "verification_mode": "command",
		"require_evidence": "true",
		"min_score":        "85",
	}}
	completed := []WorkflowStageResult{{
		Stage:  "verify",
		Status: "completed",
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
			Name: "verification", Status: "passed", Actual: "bounded checks complete",
		}},
		Result: schema.AgentResult{
			Verification: []schema.Verification{
				{Kind: "tests", Status: "passed", Detail: "go test ./..."},
				{Kind: "tool", Status: "failed", Detail: "run_command: workflow stage command limit reached (4); stop calling commands and produce the stage report"},
			},
			ToolResults: []schema.ToolResult{
				{CallID: "test-passed", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
				{CallID: "extra-blocked", ToolName: "run_command", Content: "workflow stage command limit reached (4); stop calling commands and produce the stage report", IsError: true, Denied: true},
			},
		},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.ToolErrors != 0 || evaluation.PolicyInterventions != 1 || evaluation.VerificationFailed != 0 || evaluation.VerificationTotal != 1 {
		t.Fatalf("expected an unexecuted redundant call to be an efficiency intervention, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateRecordsTestRewritePolicyWithoutProductFailure(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"require_acceptance":   "true",
		"require_verification": "true", "verification_mode": "command",
		"require_evidence": "true",
		"min_score":        "85",
	}}
	completed := []WorkflowStageResult{{
		Stage:  "implement",
		Status: "completed",
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
			Name: "implementation", Status: "passed", Actual: "files changed",
		}},
		Result: schema.AgentResult{
			Verification: []schema.Verification{
				{Kind: "tests", Status: "passed", Detail: "go test ./..."},
				{Kind: "tool", Status: "failed", Detail: "existing test web/app.test.js encodes acceptance; change the implementation instead of rewriting the test"},
			},
			ToolResults: []schema.ToolResult{
				{CallID: "test-passed", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
				{CallID: "rewrite-blocked", ToolName: "apply_patch", Content: "tool call apply_patch was not executed: existing test web/app.test.js encodes acceptance; change the implementation instead of rewriting the test", IsError: true},
			},
		},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.ToolErrors != 0 || evaluation.PolicyInterventions != 1 || evaluation.VerificationFailed != 0 {
		t.Fatalf("test-rewrite protection must be a policy intervention, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateRecordsChangeSliceDependencyInterventionWithoutProductFailure(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"require_acceptance":   "true",
		"require_verification": "true", "verification_mode": "command",
		"require_evidence": "true",
		"min_score":        "85",
	}}
	completed := []WorkflowStageResult{{
		Stage:  "implement",
		Status: "completed",
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
			Name: "implementation", Status: "passed", Actual: "ready slice changed",
		}},
		Result: schema.AgentResult{
			Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
			ToolResults: []schema.ToolResult{
				{CallID: "write", ToolName: "apply_patch", Content: `{"relative_path":"a.go","status":"modified"}`},
				{CallID: "dependent", ToolName: "read_file_range", Content: `tool call read_file_range was not executed: change slice slice-docs is not ready: dependencies for slice "slice-docs" are not complete`, IsError: true},
				{CallID: "test", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
			},
		},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.ToolErrors != 0 || evaluation.PolicyInterventions != 1 {
		t.Fatalf("change-slice dependency gate must be a control intervention, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateFailsImplementWithoutMutations(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"stages":               "implement,verify,audit",
		"require_acceptance":   "true",
		"require_verification": "true", "verification_mode": "command",
		"require_evidence":       "true",
		"min_score":              "85",
		"failure_output_markers": "未通过||不可验收",
	}}
	completed := []WorkflowStageResult{
		{
			Stage:  "implement",
			Status: "completed",
			Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
				Name: "has-change-summary", Status: "passed", Actual: "plan only",
			}},
			Result: schema.AgentResult{
				Output: `{"summary":"waiting for approval","changed_files":[]}`,
				ToolResults: []schema.ToolResult{{
					CallID: "list-1", ToolName: "file_tools/list_tree", Content: `{"entries":[{"relative_path":"internal/portal/portal.go"}]}`,
				}},
			},
		},
		{
			Stage:  "verify",
			Status: "completed",
			Result: schema.AgentResult{
				Verification: []schema.Verification{{Kind: "response", Status: "passed", Detail: "policy blocked"}},
				ToolResults: []schema.ToolResult{{
					CallID: "blocked", ToolName: "run_command", Content: "workflow stage command policy blocks class unknown; allowed classes: test", IsError: true, Denied: true,
				}},
			},
		},
		{
			Stage:  "audit",
			Status: "completed",
			Result: schema.AgentResult{Output: "## Audit Decision\n**未通过 / 不可验收。**\n"},
		},
	}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if evaluation.Passed {
		t.Fatalf("expected empty implement plus blocked verify to fail quality, got %#v", evaluation)
	}
	joined := strings.Join(evaluation.Failures, "; ")
	if !strings.Contains(joined, "executed test") && !strings.Contains(joined, "未通过") && !strings.Contains(joined, "failure marker") {
		t.Fatalf("expected executed-check or audit-failure marker, got %q", joined)
	}
}

func TestWorkflowQualityGatePolicyInterventionCannotSatisfyEvidence(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{"require_evidence": "true"}}
	completed := []WorkflowStageResult{{
		Stage:  "verify",
		Status: "completed",
		Result: schema.AgentResult{ToolResults: []schema.ToolResult{{
			CallID: "blocked", ToolName: "run_command", Content: "workflow stage command policy blocks class unknown; allowed classes: test", IsError: true, Denied: true,
		}}},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if evaluation.Passed || evaluation.ToolErrors != 0 || evaluation.PolicyInterventions != 1 || evaluation.EvidenceArtifacts != 0 {
		t.Fatalf("expected policy intervention to remain non-evidence and fail the evidence requirement, got %#v", evaluation)
	}
}

func TestWorkflowQualityGateWaivesStructuredRaceFailureWithVerifiedFallback(t *testing.T) {
	stage := workflowGraphStage{Name: "quality", Params: map[string]string{
		"require_acceptance":   "true",
		"require_verification": "true", "verification_mode": "command",
		"require_evidence":                "true",
		"min_score":                       "85",
		"allowed_infrastructure_failures": "go_race_unavailable",
	}}
	completed := []WorkflowStageResult{{
		Stage:  "recovery-verify",
		Status: "completed",
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
			Name: "recovery", Status: "passed", Actual: "ordinary test fallback passed",
		}},
		Result: schema.AgentResult{
			Verification: []schema.Verification{
				{Kind: "race test", Status: "failed", Detail: "go test -race ./...: -race requires cgo"},
				{Kind: "tests", Status: "passed", Detail: "go test ./..."},
			},
			ToolResults: []schema.ToolResult{
				{CallID: "race", ToolName: "run_command", IsError: true, Content: `{"program":"go","args":["test","-race","./..."],"exit_code":2,"stderr":"go: -race requires cgo"}`},
				{CallID: "fallback", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
			},
		},
	}}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.VerificationFailed != 0 || evaluation.VerificationTotal != 1 || evaluation.InfrastructureLimits != 1 {
		t.Fatalf("expected the structured race failure to be paired with the verified fallback, got %#v", evaluation)
	}
}

func TestWorkflowFinalQualityGatePassesRecoveredDeliveryWithInfrastructureAndPolicySignals(t *testing.T) {
	stage := workflowGraphStage{Name: "final-quality", Params: map[string]string{
		"stages":               "repair,recovery-verify,recovery-audit",
		"require_acceptance":   "true",
		"require_verification": "true", "verification_mode": "command",
		"require_evidence":                "true",
		"min_score":                       "85",
		"allow_unknown":                   "false",
		"allowed_infrastructure_failures": "go_race_unavailable",
	}}
	completed := []WorkflowStageResult{
		{
			Stage:  "repair",
			Status: "completed",
			Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
				Name: "defect-coverage", Status: "passed", Actual: "TOCTOU regression added",
			}},
			Result: schema.AgentResult{ToolResults: []schema.ToolResult{{
				CallID: "repair-test", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`,
			}}},
		},
		{
			Stage:  "recovery-verify",
			Status: "completed",
			Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
				Name: "recovery-verification", Status: "passed", Actual: "regression and full test pass",
			}},
			Result: schema.AgentResult{
				Verification: []schema.Verification{
					{Kind: "tests", Status: "passed", Detail: "go test ./..."},
					{Kind: "race test", Status: "failed", Detail: "go test -race ./...: -race requires cgo"},
					{Kind: "tool", Status: "failed", Detail: "read_file: workflow stage read limit reached (1); use the supplied stage evidence and produce the stage report"},
				},
				ToolResults: []schema.ToolResult{
					{CallID: "fallback", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
					{CallID: "race", ToolName: "run_command", Content: `{"program":"go","args":["test","-race","./..."],"exit_code":2,"stderr":"go: -race requires cgo"}`},
					{CallID: "blocked-read", ToolName: "read_file", Content: "workflow stage read limit reached (1); use the supplied stage evidence and produce the stage report", IsError: true, Denied: true},
				},
			},
		},
		{
			Stage:  "recovery-audit",
			Status: "completed",
			Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
				Name: "finding-resolution", Status: "passed", Actual: "no blockers remain",
			}},
		},
	}

	evaluation := evaluateWorkflowGraphQualityGate(stage, completed)
	if !evaluation.Passed || evaluation.Status != "passed" || evaluation.ToolErrors != 0 || evaluation.VerificationFailed != 0 || evaluation.InfrastructureLimits != 1 || evaluation.PolicyInterventions != 1 {
		t.Fatalf("expected recovered delivery to pass the final quality gate, got %#v", evaluation)
	}
}

func TestWorkflowStageEvidenceRequiresTraceableArtifact(t *testing.T) {
	weak := WorkflowStageResult{Output: WorkflowStageOutput{Artifacts: []session.WorkflowRunArtifact{{Kind: "plan", Content: "implementation plan"}}}}
	if workflowGraphStageHasEvidence(weak) {
		t.Fatal("plain plan text must not satisfy required evidence")
	}
	strong := WorkflowStageResult{Output: WorkflowStageOutput{Artifacts: []session.WorkflowRunArtifact{{Kind: "diff", Content: "@@ -1 +1 @@", Hash: "abc123"}}}}
	if !workflowGraphStageHasEvidence(strong) {
		t.Fatal("content-addressed diff should satisfy required evidence")
	}
}

func TestVerificationStatusIgnoresWorkflowPolicyInterventions(t *testing.T) {
	policyResult := schema.ToolResult{
		ToolName: "run_command",
		Content:  "workflow stage command policy blocks class lint; allowed classes: test",
		IsError:  true,
		Denied:   true,
	}
	if got := verificationStatus([]schema.ToolResult{policyResult}); got != "passed" {
		t.Fatalf("expected stage policy intervention not to block response verification, got %q", got)
	}

	_, _, verification := buildStructuredArtifacts("implementation completed", []schema.ToolResult{policyResult}, "fix")
	if len(verification) == 0 || verification[0].Kind != "response" || verification[0].Status != "passed" {
		t.Fatalf("expected passed response verification for policy-only intervention, got %#v", verification)
	}
}

func TestVerificationStatusStillReportsRealDenialsAndErrors(t *testing.T) {
	denied := schema.ToolResult{ToolName: "write_file", Content: "operator denied write", IsError: true, Denied: true}
	if got := verificationStatus([]schema.ToolResult{denied}); got != "blocked" {
		t.Fatalf("expected ordinary denial to block response verification, got %q", got)
	}

	failed := schema.ToolResult{ToolName: "run_command", Content: "test failed", IsError: true}
	if got := verificationStatus([]schema.ToolResult{failed}); got != "failed" {
		t.Fatalf("expected ordinary tool error to fail response verification, got %q", got)
	}
}
