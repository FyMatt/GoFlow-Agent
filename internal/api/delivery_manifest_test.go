package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestPreviewDeliveryDoesNotRequireFileDiff(t *testing.T) {
	checks := deliveryChecksFromVerifications("", []schema.Verification{{Kind: "verifier", Status: "failed", Detail: "Status: pass\nRisks: none"}})
	if len(checks) != 1 || checks[0].Status != "failed" {
		t.Fatal("an explicit saved failure must not be downgraded")
	}
	state := session.New(8)
	id := state.StartAgentRunWithOptions("启动服务供人工预览，不改文件", session.AgentRunStartOptions{AgentID: "software-engineer"})
	result := schema.AgentResult{Output: "预览服务已启动，未修改任何项目文件。http://127.0.0.1:5003/", Changes: []schema.Change{{Files: []string{"//127.0.0.1", "`/static/style.css`"}}}, ToolResults: []schema.ToolResult{
		{CallID: "start", ToolName: "start_process", Content: `{"process_id":"preview","running":true,"stdout":"http://127.0.0.1:5003/"}`},
		{CallID: "browser", ToolName: "browser_snapshot", Content: `{"url":"http://127.0.0.1:5003/","dom_bytes":3000,"dom_preview":"<main>actual page</main>"}`},
	}, Verification: []schema.Verification{{Kind: "verifier", Status: "warning", Detail: "Status: pass\nChecked: actual browser\nRisks: none\nNext: none"}}}
	state.CompleteAgentRun(id, "completed", result)
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	run, _ := state.AgentRun(id)
	manifest := server.agentRunDeliveryManifest(run)
	if manifest.Conclusion != "success" || len(manifest.Changes) != 0 {
		t.Fatalf("operational task was misgraded: %#v", manifest)
	}
}

func TestAgentDeliveryManifestDoesNotTrustModelCompletionClaim(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRunWithOptions("fix the boundary condition", session.AgentRunStartOptions{AgentID: "software-engineer"})
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:       "Implemented the fix and all tests pass.",
		AgentID:      "software-engineer",
		Changes:      []schema.Change{{Summary: "fixed clamp", Files: []string{"clamp.go"}}},
		Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "all tests pass"}},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "unverified" && manifest.Conclusion != "partial" {
		t.Fatalf("expected an evidence-free completion claim to be unverified or partial, got %#v", manifest)
	}
	if manifest.Quality.StrongEvidence != 0 || manifest.Quality.Score >= 85 {
		t.Fatalf("expected no strong evidence and a failing quality score, got %#v", manifest.Quality)
	}
	assertDeliveryGate(t, manifest, "changes_have_diff", false)
	assertDeliveryGate(t, manifest, "changes_verified", false)
	assertDeliveryGate(t, manifest, "strong_evidence", false)
}

func TestAgentDeliveryManifestPassesWithDiffAndCommandEvidence(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRunWithOptions("fix the boundary condition", session.AgentRunStartOptions{AgentID: "software-engineer"})
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:  "Implemented and verified the fix.",
		AgentID: "software-engineer",
		ToolResults: []schema.ToolResult{
			{
				CallID:   "write-1",
				ToolName: "write_file",
				Content:  `{"relative_path":"clamp.go","status":"modified","line_summary":"1 line changed","added_lines":1,"deleted_lines":1,"diff_preview":"- return value\n+ return max"}`,
			},
			{
				CallID:   "test-1",
				ToolName: "run_command",
				Content:  `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`,
			},
		},
		Changes:      []schema.Change{{Summary: "fixed clamp", Files: []string{"clamp.go"}}},
		Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "success" || manifest.Quality.Score < 85 {
		t.Fatalf("expected a successful evidence-backed delivery, got %#v", manifest)
	}
	if len(manifest.Changes) != 1 || manifest.Metrics.ToolCalls != 2 {
		t.Fatalf("expected one persisted diff and two traceable tool calls, got %#v", manifest)
	}
	assertDeliveryGate(t, manifest, "changes_have_diff", true)
	assertDeliveryGate(t, manifest, "changes_verified", true)
	assertDeliveryGate(t, manifest, "strong_evidence", true)
}

func TestAgentDeliveryManifestPassesWithContentAddressedOfficeArtifact(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRun("create and validate a workbook")
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output: "workbook created",
		ToolResults: []schema.ToolResult{{
			CallID: "create-1", ToolName: "create_xlsx",
			Content:      `{"path":"deliverables/report.xlsx","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bytes":2048}`,
			ArtifactRef:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ArtifactHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}, {
			CallID: "validate-1", ToolName: "validate_office_document",
			Content: `{"path":"deliverables/report.xlsx","valid":true}`,
		}},
		Changes:      []schema.Change{{Summary: "created workbook", Files: []string{"deliverables/report.xlsx"}}},
		Verification: []schema.Verification{{Kind: "validation", Status: "passed", Detail: "workbook structure validated"}},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "success" || manifest.Quality.Score < 85 {
		t.Fatalf("content-addressed Office artifact must satisfy the change evidence gate, got %#v", manifest)
	}
	assertDeliveryGate(t, manifest, "changes_have_diff", true)
	assertDeliveryGate(t, manifest, "changes_verified", true)
}

func TestAgentDeliveryManifestPassesReportWithTraceableDiagnosticEvidence(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRun("diagnose the host and write a report")
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output: "report created",
		ToolResults: []schema.ToolResult{{
			CallID: "diagnose-1", ToolName: "host_diagnostic_snapshot",
			Content: `{"hostname":"test-host","disk_free_bytes":1024}`,
		}, {
			CallID: "write-1", ToolName: "write_file",
			Content:      `{"relative_path":"deliverables/host-diagnosis.md","status":"created","sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
			ArtifactRef:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ArtifactHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		}},
		Changes:      []schema.Change{{Summary: "created diagnosis report", Files: []string{"deliverables/host-diagnosis.md"}}},
		Verification: []schema.Verification{{Kind: "report_review", Status: "", Detail: "summary did not provide a status"}},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "success" || manifest.Quality.Score < 85 {
		t.Fatalf("traceable diagnostic report must satisfy delivery quality, got %#v", manifest)
	}
	assertDeliveryGate(t, manifest, "changes_verified", true)
}

func TestWorkflowDeliveryManifestRejectsHeadingOnlyEvidence(t *testing.T) {
	state := session.New(8)
	runID := state.StartWorkflowRun("quality-flow", "implement and verify the change")
	state.CompleteWorkflowRun(runID, "completed", "delivery complete", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:  "report",
		Status: "completed",
		Result: schema.AgentResult{
			Output:       "# Verification\n\nAll requirements are satisfied.",
			Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "tests passed"}},
		},
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{
			Name: "verification heading", Ref: "result.output", Expected: "contains Verification", Actual: "# Verification", Status: "passed",
		}},
	}})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "unverified" || manifest.Quality.StrongEvidence != 0 {
		t.Fatalf("expected heading-only workflow evidence to remain unverified, got %#v", manifest)
	}
	assertDeliveryGate(t, manifest, "strong_evidence", false)
}

func TestWorkflowDeliveryManifestPassesWithRealToolEvidence(t *testing.T) {
	state := session.New(8)
	runID := state.StartWorkflowRun("engineering-delivery", "implement and verify the change")
	state.CompleteWorkflowRun(runID, "completed", "delivery complete", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:  "implement",
		Status: "completed",
		Result: schema.AgentResult{
			Output: "implemented",
			ToolResults: []schema.ToolResult{
				{CallID: "write-1", ToolName: "apply_patch", Content: `{"relative_path":"cache.go","status":"modified","line_summary":"2 lines changed","added_lines":1,"deleted_lines":1,"diff_preview":"- old\n+ new"}`},
				{CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
			},
			Changes:      []schema.Change{{Summary: "updated cache", Files: []string{"cache.go"}}},
			Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
		},
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{Name: "implementation", Expected: "completed", Actual: "completed", Status: "passed"}},
	}})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "success" || manifest.Quality.Score < 85 || len(manifest.Changes) != 1 {
		t.Fatalf("expected a successful workflow delivery, got %#v", manifest)
	}
	assertDeliveryGate(t, manifest, "acceptance_passed", true)
	assertDeliveryGate(t, manifest, "verification_passed", true)
	assertDeliveryGate(t, manifest, "changes_verified", true)
}

func TestWorkflowDeliveryManifestRecoversIntermediateToolFailure(t *testing.T) {
	state := session.New(8)
	runID := state.StartWorkflowRun("engineering-delivery", "repair and verify")
	state.CompleteWorkflowRun(runID, "completed", "delivery complete", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage: "implement", Status: "completed", Result: schema.AgentResult{
			ToolResults:  []schema.ToolResult{{CallID: "patch-1", ToolName: "apply_patch", Content: `{"relative_path":"cache.go","status":"modified","diff_preview":"- old\n+ new"}`}, {CallID: "failed-1", ToolName: "run_command", IsError: true, Content: `{"program":"go","args":["test","-race","./..."],"exit_code":1}`}, {CallID: "passed-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`}},
			Changes:      []schema.Change{{Summary: "updated cache", Files: []string{"cache.go"}}},
			Verification: []schema.Verification{{Kind: "tests", Status: "failed", Detail: "race unavailable"}, {Kind: "tests", Status: "passed", Detail: "go test ./..."}},
		}, Acceptance: []session.WorkflowRunAcceptanceSnapshot{{Name: "implementation", Status: "passed"}},
	}})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "success" || manifest.Quality.ToolErrors != 0 || manifest.Quality.RecoveredToolErrors == 0 {
		t.Fatalf("expected recovered intermediate failure to pass delivery, got %#v", manifest)
	}
}

func TestWorkflowDeliveryManifestRecoversImplementFailuresAfterLaterVerification(t *testing.T) {
	state := session.New(8)
	runID := state.StartWorkflowRun("engineering-delivery", "implement and verify the change")
	state.CompleteWorkflowRun(runID, "completed", "delivery complete", "", "", nil, []session.WorkflowRunStageSnapshot{
		{
			Stage: "implement", Status: "completed",
			Result: schema.AgentResult{
				Output: "partial implementation",
				ToolResults: []schema.ToolResult{
					{CallID: "write-1", ToolName: "apply_patch", Content: `{"relative_path":"cache.go","status":"modified","diff_preview":"- old\n+ new"}`},
					{CallID: "rewrite-denied", ToolName: "apply_patch", IsError: true, Content: "tool call apply_patch was not executed: existing test web/app.test.js encodes acceptance; change the implementation instead of rewriting the test"},
					{CallID: "policy-denied", ToolName: "run_command", IsError: true, Denied: true, Content: "workflow stage command policy blocks class unknown; allowed classes: build,format,lint,test"},
					{CallID: "failed-1", ToolName: "run_command", IsError: true, Content: `{"program":"go","args":["test","./..."],"exit_code":1,"stdout":"FAIL"}`},
				},
				Changes: []schema.Change{{Summary: "updated cache", Files: []string{"cache.go"}}},
				Verification: []schema.Verification{
					{Kind: "command", Status: "failed", Detail: "go test ./...\nFAIL"},
					{Kind: "tool", Status: "failed", Detail: "existing test web/app.test.js encodes acceptance; change the implementation instead of rewriting the test"},
				},
			},
			Acceptance: []session.WorkflowRunAcceptanceSnapshot{
				{Name: "workspace-discovered-tests", Status: "failed", Reason: "go test ./... failed"},
				{Name: "has-files-changed", Status: "passed"},
			},
		},
		{
			Stage: "verify", Status: "completed",
			Result: schema.AgentResult{
				Output: "Verification Result\nCommands Run\nFailure Evidence",
				ToolResults: []schema.ToolResult{
					{CallID: "test-2", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
				},
				Verification: []schema.Verification{{Kind: "command", Status: "passed", Detail: "go test ./..."}},
			},
			Acceptance: []session.WorkflowRunAcceptanceSnapshot{{Name: "has-verification-result", Status: "passed"}},
		},
		{
			Stage: "audit", Status: "completed",
			Result: schema.AgentResult{
				Output:       "Audit Decision\n**Blocked**\nRequirements Coverage\nRecovery Status",
				Verification: []schema.Verification{{Kind: "audit", Status: "failed", Detail: "historical implement failures"}},
			},
			Acceptance: []session.WorkflowRunAcceptanceSnapshot{{Name: "has-audit-decision", Status: "passed"}},
		},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "success" || manifest.Quality.ToolErrors != 0 || manifest.Quality.AcceptanceFailed != 0 || manifest.Quality.VerificationFailed != 0 {
		t.Fatalf("later passing verification must recover implement-stage failures, got %#v", manifest)
	}
}

func TestWorkflowDeliveryManifestDoesNotSucceedWithoutPersistedChanges(t *testing.T) {
	state := session.New(8)
	runID := state.StartWorkflowRun("engineering-delivery", "implement and verify the change")
	state.CompleteWorkflowRun(runID, "completed", "delivery complete", "", "", nil, []session.WorkflowRunStageSnapshot{
		{
			Stage: "implement", Status: "completed",
			Result: schema.AgentResult{Output: "no files were changed"},
			Acceptance: []session.WorkflowRunAcceptanceSnapshot{
				{Name: "has-files-changed", Status: "failed", Reason: "expected exists=true for result.changed_files"},
			},
		},
		{
			Stage: "verify", Status: "completed",
			Result: schema.AgentResult{
				ToolResults:  []schema.ToolResult{{CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`}},
				Verification: []schema.Verification{{Kind: "command", Status: "passed", Detail: "go test ./..."}},
			},
			Acceptance: []session.WorkflowRunAcceptanceSnapshot{{Name: "has-verification-result", Status: "passed"}},
		},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion == "success" || manifest.Quality.AcceptanceFailed == 0 {
		t.Fatalf("a delivery with no persisted diffs must not recover to success, got %#v", manifest)
	}
}

func TestDeliveryManifestPolicyInterventionDoesNotMaskRealFailures(t *testing.T) {
	tests := []struct {
		name   string
		item   runArtifactItem
		blocks bool
	}{
		{
			name:   "workflow policy intervention is recoverable",
			item:   runArtifactItem{IsError: true, Content: "workflow stage read limit reached"},
			blocks: false,
		},
		{
			name:   "denied workflow stage policy is a control intervention",
			item:   runArtifactItem{IsError: true, Denied: true, Content: "workflow stage read limit reached"},
			blocks: false,
		},
		{
			name:   "change slice dependency gate is a control intervention",
			item:   runArtifactItem{IsError: true, Content: `tool call read_file_range was not executed: change slice slice-docs is not ready: dependencies for slice "slice-docs" are not complete`},
			blocks: false,
		},
		{
			name:   "operator denial remains blocking",
			item:   runArtifactItem{IsError: true, Denied: true, Content: "operator denied apply_patch"},
			blocks: true,
		},
		{
			name:   "ordinary tool failure remains blocking",
			item:   runArtifactItem{IsError: true, Content: `{"error":"permission denied"}`},
			blocks: true,
		},
		{
			name:   "timeout remains blocking",
			item:   runArtifactItem{IsError: true, Content: `{"timed_out":true,"error":"command timed out"}`},
			blocks: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runDeliveryArtifactBlocks(tt.item); got != tt.blocks {
				t.Fatalf("runDeliveryArtifactBlocks() = %v, want %v for %#v", got, tt.blocks, tt.item)
			}
		})
	}
}

func TestDeliveryManifestPolicyInterventionVerificationIsNotFailure(t *testing.T) {
	state := session.New(8)
	runID := state.StartWorkflowRun("engineering-delivery", "implement and verify")
	state.CompleteWorkflowRun(runID, "completed", "delivery complete", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage: "verify", Status: "completed", Result: schema.AgentResult{
			ToolResults: []schema.ToolResult{
				{CallID: "write-1", ToolName: "apply_patch", Content: `{"relative_path":"cache.go","status":"modified","diff_preview":"- old\n+ new"}`},
				{CallID: "test-1", ToolName: "run_command", IsError: true, Content: "workflow stage command limit reached (2); stop calling commands and produce the stage report"},
				{CallID: "test-2", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
			},
			Changes:      []schema.Change{{Summary: "updated cache", Files: []string{"cache.go"}}},
			Verification: []schema.Verification{{Kind: "tool", Status: "failed", Detail: "workflow stage command limit reached (2)"}, {Kind: "tests", Status: "passed", Detail: "go test ./..."}},
		}, Acceptance: []session.WorkflowRunAcceptanceSnapshot{{Name: "implementation", Status: "passed"}},
	}})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Quality.VerificationFailed != 0 || manifest.Quality.ToolErrors != 0 || manifest.Conclusion != "success" {
		t.Fatalf("policy intervention must not fail delivery, got %#v", manifest)
	}
}

func TestWorkflowRunPolicyInterventionPreservesDeniedArtifact(t *testing.T) {
	if workflowRunArtifactIsBlockingError(session.WorkflowRunArtifact{
		IsError: true,
		Content: "workflow stage command limit reached (2)",
	}) {
		t.Fatal("plain policy intervention must not be blocking")
	}
	if workflowRunArtifactIsBlockingError(session.WorkflowRunArtifact{
		IsError:  true,
		Content:  "workflow stage command limit reached (2)",
		Metadata: map[string]string{"denied": "true"},
	}) {
		t.Fatal("denied workflow stage policy intervention must not be blocking")
	}
	if !workflowRunArtifactIsBlockingError(session.WorkflowRunArtifact{
		IsError:  true,
		Content:  "operator denied apply_patch",
		Metadata: map[string]string{"denied": "true"},
	}) {
		t.Fatal("operator denial must remain blocking")
	}
}

func TestWorkflowDeliveryRaceFallbackUsesStructuredChangeEvidence(t *testing.T) {
	run := session.WorkflowRunSnapshot{
		Status: "completed",
		CompletedStages: []session.WorkflowRunStageSnapshot{{
			Result: schema.AgentResult{Changes: []schema.Change{{Files: []string{".github/workflows/race.yml"}}}},
		}},
	}
	artifacts := []runArtifactItem{
		{ToolCallID: "race", ToolName: "run_command", IsError: true, Content: `{"program":"go","args":["test","-race","./..."],"exit_code":2,"stderr":"go: -race requires cgo; enable cgo by setting CGO_ENABLED=1"}`},
		{ToolCallID: "fallback", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0}`},
	}
	if !workflowRunDeliveryHasAllowedRaceFallback(run, artifacts) {
		t.Fatal("expected race infrastructure failure plus fallback and structured CI change to be recoverable")
	}
	run.CompletedStages[0].Result.Changes[0].Files = nil
	if workflowRunDeliveryHasAllowedRaceFallback(run, artifacts) {
		t.Fatal("expected race fallback without a CI gate change to remain blocking")
	}
	run.Request = "implement config migration"
	if !workflowRunDeliveryHasAllowedRaceFallback(run, artifacts) {
		t.Fatal("expected an optional race probe to recover with a passing ordinary test")
	}
	run.Request = "run go test -race and provide the CI gate"
	if workflowRunDeliveryHasAllowedRaceFallback(run, artifacts) {
		t.Fatal("an explicitly required race check still needs a CI gate")
	}
}

func TestWorkflowDeliveryManifestDoesNotTreatProposedPlanAsUnknownVerification(t *testing.T) {
	state := session.New(8)
	runID := state.StartWorkflowRun("engineering-delivery", "implement and verify the change")
	state.CompleteWorkflowRun(runID, "completed", "delivery complete", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:  "implement",
		Status: "completed",
		Result: schema.AgentResult{
			Output: "implemented",
			ToolResults: []schema.ToolResult{
				{CallID: "write-1", ToolName: "apply_patch", Content: `{"relative_path":"cache.go","status":"modified","added_lines":1,"deleted_lines":1,"diff_preview":"- old\n+ new"}`},
				{CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
			},
			Changes: []schema.Change{{Summary: "updated cache", Files: []string{"cache.go"}}},
			Verification: []schema.Verification{
				{Kind: "plan", Status: "proposed", Detail: "implementation plan captured for review"},
				{Kind: "tests", Status: "passed", Detail: "go test ./..."},
			},
		},
	}})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "success" || manifest.Quality.Score != 100 || manifest.Quality.VerificationUnknown != 0 {
		t.Fatalf("expected proposed plan metadata to remain outside verification scoring, got %#v", manifest)
	}
	for _, check := range manifest.Verification {
		if check.Kind == "plan" {
			t.Fatalf("expected proposed plan entry to be excluded from delivery verification, got %#v", manifest.Verification)
		}
	}
}

func TestDeliveryManifestTreatsNonZeroCommandAsFailure(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRun("run the test suite")
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output: "tests passed",
		ToolResults: []schema.ToolResult{{
			CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":1,"stderr":"FAIL"}`,
		}},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "failed" || manifest.Quality.ToolErrors == 0 {
		t.Fatalf("expected non-zero command evidence to fail delivery, got %#v", manifest)
	}
	assertDeliveryGate(t, manifest, "no_tool_errors", false)
}

func TestDeliveryManifestIgnoresExploratoryMissingRead(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRun("inspect then implement")
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output: "implemented and verified",
		ToolResults: []schema.ToolResult{
			{CallID: "read-missing", ToolName: "file_tools/read_file", Content: "read_file path not found: README.md", IsError: true},
			{CallID: "write-1", ToolName: "apply_patch", Content: `{"relative_path":"main.go","status":"modified","added_lines":1,"deleted_lines":1,"diff_preview":"- old\n+ new"}`},
			{CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
		},
		Changes:      []schema.Change{{Summary: "updated main", Files: []string{"main.go"}}},
		Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Quality.ToolErrors != 0 {
		t.Fatalf("missing-path reads are discovery misses, not delivery failures: %#v", manifest.Quality)
	}
	assertDeliveryGate(t, manifest, "no_tool_errors", true)
}

func TestDeliveryManifestIgnoresWindowsExploratoryListMiss(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRun("inspect then implement")
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output: "implemented and verified",
		ToolResults: []schema.ToolResult{
			{CallID: "list-missing", ToolName: "list_tree", Content: "GetFileAttributesEx deliverables: The system cannot find the file specified.", IsError: true},
			{CallID: "write-1", ToolName: "apply_patch", Content: `{"relative_path":"main.go","status":"modified","added_lines":1,"deleted_lines":1,"diff_preview":"- old\n+ new"}`},
			{CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
		},
		Changes:      []schema.Change{{Summary: "updated main", Files: []string{"main.go"}}},
		Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Quality.ToolErrors != 0 {
		t.Fatalf("windows missing-directory list_tree is discovery, not a delivery failure: %#v", manifest.Quality)
	}
	assertDeliveryGate(t, manifest, "no_tool_errors", true)
}

func TestDeliveryManifestTreatsBlockedSliceReplayAsPolicyIntervention(t *testing.T) {
	if !runDeliveryPolicyInterventionText("change slice slice-internal-filesafe is blocked: stage remediate failed before this slice was verified") {
		t.Fatal("blocked slice after a failed stage is a control intervention")
	}
	if !runDeliveryPolicyInterventionText("change slice slice-internal-auth is already complete; refusing to replay write tool write_file") {
		t.Fatal("refusing completed slice replay is a control intervention")
	}
	if !runDeliveryPolicyInterventionText(`cwd "D:\\Projects\\Golang\\goflow_agent\\task-staging\\wf-security-audit-evidence-gate-178742372" escapes workspace root`) {
		t.Fatal("truncated cwd escape is an infrastructure intervention")
	}
	if runDeliveryToolResultFailed(runArtifactItem{
		ToolName: "run_command",
		IsError:  true,
		Content:  `cwd "D:\\task-staging\\wf-security-audit-evidence-gate-178742372" escapes workspace root`,
	}) {
		t.Fatal("truncated cwd escape must not count as a failed command result")
	}
}

func TestAgentDeliveryManifestIgnoresSuspendedApprovalResult(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRun("apply and verify the approved change")
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output: "change applied and verified",
		ToolResults: []schema.ToolResult{
			// Some historical providers marked the approval placeholder as an
			// error even though it was only waiting for operator approval.
			{CallID: "approval-1", ToolName: "apply_patch", Content: "requires approval", IsError: true, Suspended: true},
			{CallID: "write-1", ToolName: "apply_patch", Content: `{"relative_path":"clamp.go","status":"modified","added_lines":1,"deleted_lines":1,"diff_preview":"- old\n+ new"}`},
			{CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
		},
		Changes:      []schema.Change{{Summary: "updated clamp", Files: []string{"clamp.go"}}},
		Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Quality.ToolErrors != 0 {
		t.Fatalf("suspended approval result must not count as a tool error, got %#v", manifest.Quality)
	}
	assertDeliveryGate(t, manifest, "no_tool_errors", true)
}

func TestAgentDeliveryManifestIgnoresRecoveredApprovalContextEvent(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRun("apply and verify the approved change")
	state.AppendAgentRunEvent(runID, session.AgentRunEventSnapshot{
		Type:    string(schema.StreamEventError),
		Content: agentRunToolApprovalActionReason(false),
		IsError: true,
	})
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output: "change applied and verified",
		ToolResults: []schema.ToolResult{
			{CallID: "write-1", ToolName: "apply_patch", Content: `{"relative_path":"clamp.go","status":"modified","added_lines":1,"deleted_lines":1,"diff_preview":"- old\n+ new"}`},
			{CallID: "test-1", ToolName: "run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok"}`},
		},
		Changes:      []schema.Change{{Summary: "updated clamp", Files: []string{"clamp.go"}}},
		Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "success" || manifest.Quality.ToolErrors != 0 || manifest.Quality.Score < 85 {
		t.Fatalf("recovered approval context event must not block delivery, got %#v", manifest)
	}
}

func TestAgentRunEventIsRecoverableApprovalContextError(t *testing.T) {
	if !agentRunEventIsRecoverableApprovalContextError(agentRunToolApprovalActionReason(false)) {
		t.Fatal("expected persisted approval context race to be recoverable")
	}
	if agentRunEventIsRecoverableApprovalContextError("file_tools/apply_patch failed: permission denied") {
		t.Fatal("real tool failure must remain blocking")
	}
}

func TestAgentDeliveryManifestKeepsRealExecutionFailuresBlocking(t *testing.T) {
	tests := []struct {
		name   string
		result schema.ToolResult
	}{
		{name: "denied", result: schema.ToolResult{CallID: "deny-1", ToolName: "apply_patch", Content: "operator denied", Denied: true, IsError: true}},
		{name: "tool error", result: schema.ToolResult{CallID: "error-1", ToolName: "apply_patch", Content: "permission denied", IsError: true}},
		{name: "timeout", result: schema.ToolResult{CallID: "timeout-1", ToolName: "run_command", Content: `{"timed_out":true,"cancelled":false,"exit_code":-1}`, IsError: true}},
		{name: "cancelled", result: schema.ToolResult{CallID: "cancel-1", ToolName: "run_command", Content: `{"timed_out":false,"cancelled":true,"exit_code":-1}`, IsError: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := session.New(8)
			runID := state.StartAgentRun("execute a guarded operation")
			state.CompleteAgentRun(runID, "completed", schema.AgentResult{Output: "done", ToolResults: []schema.ToolResult{tt.result}})
			server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

			manifest := getRunDeliveryManifest(t, server, runID)
			if manifest.Conclusion != "failed" || manifest.Quality.ToolErrors == 0 {
				t.Fatalf("real %s must block delivery, got %#v", tt.name, manifest)
			}
		})
	}
}

func TestAgentDeliveryManifestAllowsRecoveredIntermediateToolFailure(t *testing.T) {
	state := session.New(8)
	runID := state.StartAgentRun("repair and verify")
	state.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:       "repaired",
		Verification: []schema.Verification{{Kind: "tests", Status: "failed", Detail: "an earlier test failed before the repair"}},
		ToolResults: []schema.ToolResult{{
			CallID: "test-failed", ToolName: "run_command",
			Content: `{"program":"go","args":["test","./..."],"exit_code":1}`,
			IsError: true,
		}, {
			CallID: "test-passed", ToolName: "run_command",
			Content: `{"program":"go","args":["test","./..."],"exit_code":0}`,
		}},
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
	manifest := getRunDeliveryManifest(t, server, runID)
	if manifest.Conclusion != "success" || manifest.Quality.ToolErrors != 0 || manifest.Quality.RecoveredToolErrors != 1 || manifest.Quality.Score < 85 {
		t.Fatalf("recovered intermediate failure must not block verified delivery, got %#v", manifest)
	}
}

func TestDeliveryManifestDistinguishesRunningAndWaitingInput(t *testing.T) {
	state := session.New(8)
	runningID := state.StartAgentRun("inspect the workspace")
	waitingID := state.StartAgentRun("apply the approved change")
	state.CompleteAgentRun(waitingID, "awaiting_tool_approval", schema.AgentResult{
		Output:           "waiting for tool approval",
		Incomplete:       true,
		IncompleteReason: "tool approval required",
	})
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	running := getRunDeliveryManifest(t, server, runningID)
	if running.Conclusion != "running" {
		t.Fatalf("expected running conclusion, got %#v", running)
	}
	waiting := getRunDeliveryManifest(t, server, waitingID)
	if waiting.Conclusion != "waiting_input" {
		t.Fatalf("expected waiting_input conclusion, got %#v", waiting)
	}
}

func TestRunningWorkflowDoesNotWaitForResolvedHistoricalApproval(t *testing.T) {
	state := session.New(8)
	id := state.StartWorkflowRun("complex-project-delivery", "implement the approved plan")
	state.CompleteWorkflowRun(id, "running", "", "implement", "", nil, []session.WorkflowRunStageSnapshot{{Stage: "approve-plan", Status: "completed"}})
	run, _ := state.WorkflowRun(id)
	run.Events = append(run.Events, session.WorkflowRunEventSnapshot{NeedsAction: true, PendingApproval: true})
	if workflowRunCollectionRunNeedsAction(run) {
		t.Fatal("resolved approval history must not make a running task require input")
	}
	facts := runDeliveryFactsForWorkflow(run, runDeliveryManifest{}, nil)
	if conclusion := runDeliveryConclusion(facts, runDeliveryManifest{}); conclusion != "running" {
		t.Fatalf("running workflow was shown as %s", conclusion)
	}
	for _, status := range []string{"awaiting_approval", "awaiting_input", "paused_need_more_budget"} {
		run.Status = status
		if !workflowRunCollectionRunNeedsAction(run) {
			t.Fatalf("current %s state must still request user action", status)
		}
	}
}

func TestRunCollectionExposesUnifiedDeliveryPath(t *testing.T) {
	state := session.New(8)
	agentID := state.StartAgentRun("agent task")
	state.CompleteAgentRun(agentID, "completed", schema.AgentResult{Output: "done"})
	workflowID := state.StartWorkflowRun("flow", "workflow task")
	state.CompleteWorkflowRun(workflowID, "completed", "done", "", "", nil, nil)
	server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))

	request := httptest.NewRequest(http.MethodGet, "/api/runs", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected runs 200, got %d body=%s", response.Code, response.Body.String())
	}
	var collection runCollectionResponse
	if err := json.NewDecoder(response.Body).Decode(&collection); err != nil {
		t.Fatalf("decode run collection: %v", err)
	}
	if len(collection.Runs) != 2 {
		t.Fatalf("expected two runs, got %#v", collection.Runs)
	}
	for _, item := range collection.Runs {
		if item.DeliveryPath != "/api/runs/"+item.ID+"/delivery" {
			t.Fatalf("expected unified delivery path, got %#v", item)
		}
	}
}

func getRunDeliveryManifest(t *testing.T, server *Server, runID string) runDeliveryManifest {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/runs/"+runID+"/delivery", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected delivery manifest 200, got %d body=%s", response.Code, response.Body.String())
	}
	var manifest runDeliveryManifest
	if err := json.NewDecoder(response.Body).Decode(&manifest); err != nil {
		t.Fatalf("decode delivery manifest: %v", err)
	}
	if manifest.SchemaVersion != runDeliverySchemaVersion || manifest.RunID != runID {
		t.Fatalf("unexpected delivery manifest identity: %#v", manifest)
	}
	return manifest
}

func assertDeliveryGate(t *testing.T, manifest runDeliveryManifest, name string, passed bool) {
	t.Helper()
	for _, gate := range manifest.HardGates {
		if gate.Name != name {
			continue
		}
		if gate.Passed != passed {
			t.Fatalf("expected gate %s passed=%t, got %#v", name, passed, gate)
		}
		return
	}
	t.Fatalf("delivery gate %s not found in %#v", name, manifest.HardGates)
}
