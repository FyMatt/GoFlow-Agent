package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestChangeSliceWriteGateAllowsFollowUpPatchOnImplementedSlice(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "repair incomplete write")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "cache", Title: "Cache", ExecutionMode: "write", TargetFiles: []string{"internal/cache/cache.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "cache", Status: "implemented"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	writeTool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)}
	execCtx := ExecutionContext{
		Profile:                config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:                  []schema.Tool{writeTool},
		Session:                state,
		WorkflowRunID:          runID,
		WorkflowName:           "complex-project-delivery",
		WorkflowStage:          "implement",
		ChangeSliceEnforcement: true,
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"internal/cache/cache.go","content":"package cache"}`)}); err != nil {
		t.Fatalf("implemented-but-unverified slice must still accept a follow-up patch: %v", err)
	}
}

func TestChangeSlicePatchMismatchRemainsRetryable(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "retry exact patch")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "config", Title: "Config", ExecutionMode: "write", TargetFiles: []string{"internal/config/config.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "config", Status: "running"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	tool := schema.Tool{Name: "file_tools/apply_patch", Server: "file_tools", Kind: "write"}
	call := schema.ToolCall{Name: tool.Name, Arguments: []byte(`{"path":"internal/config/config.go"}`)}
	execCtx := ExecutionContext{Session: state, WorkflowRunID: runID, WorkflowName: "complex-project-delivery", WorkflowStage: "implement", ChangeSliceEnforcement: true}
	execCtx.recordChangeSliceToolFailure(call, tool, schema.ToolResult{ToolName: tool.Name, IsError: true, Content: "edit 1 expected 1 occurrence(s) but found 0; no changes were written"})
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) != 1 || run.ChangeSlices[0].Status != "running" {
		t.Fatalf("exact patch mismatch must remain retryable: %#v", run.ChangeSlices)
	}
	last := run.Events[len(run.Events)-1]
	if last.Reason != "change_slice_retryable_write_failure" || last.WorkflowStatus != "running" {
		t.Fatalf("retryable failure event mismatch: %#v", last)
	}
}

func TestChangeSliceStaleWriteHashRemainsRetryable(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "refresh a changed report")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "report", Title: "Report", ExecutionMode: "write", TargetFiles: []string{"docs/unified-delivery.md"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "report", Status: "running"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	tool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write"}
	call := schema.ToolCall{Name: tool.Name, Arguments: []byte(`{"path":"docs/unified-delivery.md"}`)}
	execCtx := ExecutionContext{Session: state, WorkflowRunID: runID, WorkflowName: "complex-project-delivery", WorkflowStage: "implement", ChangeSliceEnforcement: true}
	execCtx.recordChangeSliceToolFailure(call, tool, schema.ToolResult{ToolName: tool.Name, IsError: true, Content: "file changed since it was read: expected_sha256 does not match"})
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) != 1 || run.ChangeSlices[0].Status != "running" {
		t.Fatalf("stale write hash must remain retryable: %#v", run.ChangeSlices)
	}
	last := run.Events[len(run.Events)-1]
	if last.Reason != "change_slice_retryable_write_failure" || last.WorkflowStatus != "running" {
		t.Fatalf("retryable stale write event mismatch: %#v", last)
	}
}

func TestChangeSlicePermissionFailureStillBlocks(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "block unsafe write")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "config", Title: "Config", ExecutionMode: "write", TargetFiles: []string{"internal/config/config.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "config", Status: "running"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	tool := schema.Tool{Name: "file_tools/apply_patch", Server: "file_tools", Kind: "write"}
	call := schema.ToolCall{Name: tool.Name, Arguments: []byte(`{"path":"internal/config/config.go"}`)}
	execCtx := ExecutionContext{Session: state, WorkflowRunID: runID, WorkflowName: "complex-project-delivery", WorkflowStage: "implement", ChangeSliceEnforcement: true}
	execCtx.recordChangeSliceToolFailure(call, tool, schema.ToolResult{ToolName: tool.Name, IsError: true, Content: "apply_patch permission denied: internal/config/config.go"})
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) != 1 || run.ChangeSlices[0].Status != "blocked" {
		t.Fatalf("permission failure must block the slice: %#v", run.ChangeSlices)
	}
}

// A usage-level write failure (missing expected_sha256) must stay retryable:
// blocking the slice made the stage end with a blocked slice and the run stop
// before any repair could happen.
func TestChangeSliceUsageFailureKeepsSliceRetryable(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "retry usage error")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "cache", Title: "Cache", ExecutionMode: "write", TargetFiles: []string{"internal/cache/cache.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "cache", Status: "running"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	tool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write"}
	call := schema.ToolCall{Name: tool.Name, Arguments: []byte(`{"path":"internal/cache/cache.go"}`)}
	execCtx := ExecutionContext{Session: state, WorkflowRunID: runID, WorkflowName: "complex-project-delivery", WorkflowStage: "implement", ChangeSliceEnforcement: true}
	execCtx.recordChangeSliceToolFailure(call, tool, schema.ToolResult{ToolName: tool.Name, IsError: true, Content: "expected_sha256 is required when replacing an existing file"})
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) != 1 || run.ChangeSlices[0].Status != "running" {
		t.Fatalf("a usage failure must keep the slice retryable: %#v", run.ChangeSlices)
	}
	last := run.Events[len(run.Events)-1]
	if last.Reason != "change_slice_retryable_write_failure" || !strings.Contains(last.Content, "expected_sha256") {
		t.Fatalf("the failing write text must be preserved in the run event: %#v", last)
	}
}

// Retries are bounded: after three usage failures in the same stage the slice
// is blocked so the run cannot loop forever on the same failing write.
func TestChangeSliceRepeatedUsageFailuresEventuallyBlock(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "bound retries")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "cache", Title: "Cache", ExecutionMode: "write", TargetFiles: []string{"internal/cache/cache.go"}, ContractHash: "cache", Status: "running"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	tool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write"}
	call := schema.ToolCall{Name: tool.Name, Arguments: []byte(`{"path":"internal/cache/cache.go"}`)}
	execCtx := ExecutionContext{Session: state, WorkflowRunID: runID, WorkflowName: "complex-project-delivery", WorkflowStage: "implement", ChangeSliceEnforcement: true}
	failure := schema.ToolResult{ToolName: tool.Name, IsError: true, Content: "expected_sha256 is required when replacing an existing file"}
	for attempt := 0; attempt < 4; attempt++ {
		execCtx.recordChangeSliceToolFailure(call, tool, failure)
	}
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) != 1 {
		t.Fatalf("slice state missing: %#v", run)
	}
	if run.ChangeSlices[0].Status != "blocked" || !strings.Contains(run.ChangeSlices[0].BlockedReason, "kept failing") {
		t.Fatalf("repeated failures must eventually block the slice: %#v", run.ChangeSlices[0])
	}
}

// One stuck slice must not absorb the whole run: after its model-call budget it
// is blocked with the consumed count, so repair targets that slice.
func TestChangeSliceModelCallBudgetBlocksStuckSlice(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "bound one slice")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "api", Title: "API", ExecutionMode: "write", TargetFiles: []string{"internal/api/status.go"}, ContractHash: "api", Status: "running"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	execCtx := ExecutionContext{Session: state, WorkflowRunID: runID, WorkflowName: "complex-project-delivery", WorkflowStage: "implement", ChangeSliceEnforcement: true}
	execCtx.enforceChangeSliceCallBudget()
	run, _ := state.WorkflowRun(runID)
	for index := 0; index < maxModelCallsPerChangeSlice+2; index++ {
		state.AppendWorkflowRunEvent(runID, session.WorkflowRunEventSnapshot{Type: "token_usage", Stage: "implement"})
	}
	execCtx.enforceChangeSliceCallBudget()
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) != 1 {
		t.Fatalf("slice state missing: %#v", run)
	}
	if run.ChangeSlices[0].Status != "blocked" || !strings.Contains(run.ChangeSlices[0].BlockedReason, "budget") {
		t.Fatalf("stuck slice must be blocked with its budget reason: %#v", run.ChangeSlices[0])
	}
}

// The budget baseline must survive event truncation: the session keeps a
// bounded event window, so an event-based marker was dropped and the budget
// reset on every turn, letting one slice consume hundreds of model calls.
func TestChangeSliceModelCallBudgetSurvivesEventTruncation(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "survive truncation")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "api", Title: "API", ExecutionMode: "write", TargetFiles: []string{"internal/api/status.go"}, ContractHash: "api", Status: "running"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	execCtx := ExecutionContext{Session: state, WorkflowRunID: runID, WorkflowName: "complex-project-delivery", WorkflowStage: "implement", ChangeSliceEnforcement: true}
	execCtx.enforceChangeSliceCallBudget()
	for index := 0; index < 260; index++ {
		state.AppendWorkflowRunEvent(runID, session.WorkflowRunEventSnapshot{Type: "token_usage", Stage: "implement"})
	}
	execCtx.enforceChangeSliceCallBudget()
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) != 1 {
		t.Fatalf("slice state missing: %#v", run)
	}
	if run.ChangeSlices[0].Status != "blocked" {
		t.Fatalf("a truncated event window must not reset the slice budget: %#v", run.ChangeSlices[0])
	}
}

// Once every write slice is blocked the stage must stop: continuing only burns
// model calls (a real run reached 141 calls after its slices were blocked).
func TestChangeSliceBudgetStopsStageWhenAllSlicesBlocked(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "stop when all blocked")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "done", Title: "Done", ExecutionMode: "write", TargetFiles: []string{"a.go"}, ContractHash: "a", Status: "blocked"},
		{ID: "api", Title: "API", ExecutionMode: "write", TargetFiles: []string{"internal/api/status.go"}, ContractHash: "api", Status: "running"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	execCtx := ExecutionContext{Session: state, WorkflowRunID: runID, WorkflowName: "complex-project-delivery", WorkflowStage: "implement", ChangeSliceEnforcement: true}
	for index := 0; index < maxModelCallsPerChangeSlice+2; index++ {
		state.AppendWorkflowRunEvent(runID, session.WorkflowRunEventSnapshot{Type: "token_usage", Stage: "implement"})
	}
	if !execCtx.enforceChangeSliceCallBudget() {
		t.Fatalf("expected the stage to stop once no write slice can progress")
	}
	run, _ := state.WorkflowRun(runID)
	for _, slice := range run.ChangeSlices {
		if slice.Status == "running" {
			t.Fatalf("running slice must be blocked before stopping: %#v", run.ChangeSlices)
		}
	}
}

func TestChangeSliceWriteGateAllowsDeliverableArtifacts(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("security-audit-evidence-gate", "write report")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "auth", Title: "auth", ExecutionMode: "write", TargetFiles: []string{"internal/auth/auth.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "auth", Status: "planned"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	writeTool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)}
	mkdirTool := schema.Tool{Name: "file_tools/mkdir", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}
	execCtx := ExecutionContext{
		Profile:                config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:                  []schema.Tool{writeTool, mkdirTool},
		Session:                state,
		WorkflowRunID:          runID,
		WorkflowName:           "security-audit-evidence-gate",
		WorkflowStage:          "remediate",
		ChangeSliceEnforcement: true,
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: mkdirTool.Name, Arguments: []byte(`{"path":"deliverables"}`)}); err != nil {
		t.Fatalf("mkdir deliverables must be allowed: %v", err)
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"deliverables/security-report.md","content":"# report"}`)}); err != nil {
		t.Fatalf("deliverable write must be allowed: %v", err)
	}
}

func TestChangeSliceWriteGateRequiresDependenciesAndObservedTargets(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "apply slices")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "first", Title: "First", ExecutionMode: "write", TargetFiles: []string{"a.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "first", Status: "planned"},
		{ID: "second", Title: "Second", ExecutionMode: "write", DependsOn: []string{"first"}, TargetFiles: []string{"b.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "second", Status: "planned"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	writeTool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)}
	execCtx := ExecutionContext{
		Profile:                config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:                  []schema.Tool{writeTool},
		Session:                state,
		WorkflowRunID:          runID,
		WorkflowName:           "complex-project-delivery",
		WorkflowStage:          "implement",
		ChangeSliceEnforcement: true,
	}
	dependent := schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"b.go","content":"b"}`)}
	if _, err := execCtx.validateToolCall(dependent); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("dependent write should be rejected before its dependency: %v", err)
	}
	first := schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"a.go","content":"a"}`)}
	if _, err := execCtx.validateToolCall(first); err != nil {
		t.Fatalf("first write should start its ready slice: %v", err)
	}
	run, ok := state.WorkflowRun(runID)
	if !ok || run.ChangeSlices[0].Status != "running" || run.ChangeSlices[0].Attempt != 1 {
		t.Fatalf("first slice transition was not durable: %#v", run.ChangeSlices)
	}
	if err := state.MarkWorkflowRunChangeSliceFilesDurable(runID, "implement", "first", []string{"a.go"}); err != nil {
		t.Fatalf("record first target: %v", err)
	}
	if _, err := execCtx.validateToolCall(dependent); err != nil {
		t.Fatalf("dependent write should be allowed after first target evidence: %v", err)
	}
}

func TestChangeSliceWriteGateSelectsDependentOverlappingTarget(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "apply overlapping slices")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "first", Title: "First", ExecutionMode: "write", TargetFiles: []string{"shared.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "first", Status: "implemented"},
		{ID: "second", Title: "Second", ExecutionMode: "write", DependsOn: []string{"first"}, TargetFiles: []string{"shared.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "second", Status: "planned"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	writeTool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)}
	execCtx := ExecutionContext{
		Profile:                config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:                  []schema.Tool{writeTool},
		Session:                state,
		WorkflowRunID:          runID,
		WorkflowName:           "complex-project-delivery",
		WorkflowStage:          "implement",
		ChangeSliceEnforcement: true,
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"shared.go","content":"new"}`)}); err != nil {
		t.Fatalf("dependent overlapping slice should be selected after predecessor: %v", err)
	}
	run, ok := state.WorkflowRun(runID)
	if !ok || run.ChangeSlices[1].Status != "running" {
		t.Fatalf("dependent overlapping slice was not started: %#v", run.ChangeSlices)
	}
}

func TestChangeSliceWriteGateRelativizesAbsoluteWorkspacePaths(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "write absolute path")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "config", Title: "Config", ExecutionMode: "write", TargetFiles: []string{"internal/config"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "config", Status: "planned"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	root := t.TempDir()
	writeTool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)}
	execCtx := ExecutionContext{
		Profile:                config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:                  []schema.Tool{writeTool},
		Session:                state,
		WorkflowRunID:          runID,
		WorkflowName:           "complex-project-delivery",
		WorkflowStage:          "implement",
		ChangeSliceEnforcement: true,
		WorkspaceRoot:          root,
		ToolWorkspaceRoot:      root,
	}
	abs := filepath.Join(root, "internal", "config", "config.go")
	call := execCtx.prepareToolCall(schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":` + jsonQuote(abs) + `,"content":"package config"}`)})
	if _, err := execCtx.validateToolCall(call); err != nil {
		t.Fatalf("absolute path inside the task workspace should be accepted: %v", err)
	}
}

func TestWriteGateRejectsRewritingExistingAcceptanceTests(t *testing.T) {
	root := t.TempDir()
	testPath := filepath.Join(root, "web", "app.test.js")
	if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testPath, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)}
	execCtx := ExecutionContext{
		Profile:           config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:             []schema.Tool{writeTool},
		WorkspaceRoot:     root,
		ToolWorkspaceRoot: root,
	}
	_, err := execCtx.validateToolCall(schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"web/app.test.js","content":"weakened"}`)})
	if err == nil || !strings.Contains(err.Error(), "encodes acceptance") {
		t.Fatalf("existing public tests must not be rewritten: %v", err)
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"web/extra.test.js","content":"new"}`)}); err != nil {
		t.Fatalf("new test files should still be allowed: %v", err)
	}
}

func TestChangeSliceReadOfNotReadyWriteTargetIsAllowedWithoutStartingSlice(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "inspect a dependent write target")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "first", Title: "First", ExecutionMode: "write", TargetFiles: []string{"a.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "first", Status: "planned"},
		{ID: "second", Title: "Second", ExecutionMode: "write", DependsOn: []string{"first"}, TargetFiles: []string{"b.go"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "second", Status: "planned"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	readTool := schema.Tool{Name: "file_tools/read_file", Server: "file_tools", Kind: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}
	execCtx := ExecutionContext{
		Profile:                config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		Tools:                  []schema.Tool{readTool},
		Session:                state,
		WorkflowRunID:          runID,
		WorkflowName:           "complex-project-delivery",
		WorkflowStage:          "implement",
		ChangeSliceEnforcement: true,
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: readTool.Name, Arguments: []byte(`{"path":"b.go"}`)}); err != nil {
		t.Fatalf("reading a not-ready write target must be allowed as preparation: %v", err)
	}
	run, ok := state.WorkflowRun(runID)
	if !ok || len(run.ChangeSlices) != 2 {
		t.Fatalf("slice state missing: %#v", run)
	}
	if run.ChangeSlices[1].Status != "planned" || run.ChangeSlices[1].Attempt != 0 {
		t.Fatalf("a read must not start the dependent write slice: %#v", run.ChangeSlices[1])
	}
}

// A blocked read-only slice must not block the evidence a repair stage needs.
// In real runs an entire repair stage produced no changes because every read
// hit "change slice audit-read-001 is blocked".
func TestChangeSliceReadOfBlockedSliceIsAllowed(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("security-audit-evidence-gate", "read evidence for a blocked slice")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "audit-read-001", ExecutionMode: "read", TargetFiles: []string{"docs/security-report.md"}, Status: "blocked", BlockedReason: "stage remediate failed before this slice was verified"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	readTool := schema.Tool{Name: "file_tools/read_file", Server: "file_tools", Kind: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}
	execCtx := ExecutionContext{
		Profile:                config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		Tools:                  []schema.Tool{readTool},
		Session:                state,
		WorkflowRunID:          runID,
		WorkflowName:           "security-audit-evidence-gate",
		WorkflowStage:          "repair",
		ChangeSliceEnforcement: true,
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: readTool.Name, Arguments: []byte(`{"path":"docs/security-report.md"}`)}); err != nil {
		t.Fatalf("a blocked read slice must not block reads: %v", err)
	}
}

func jsonQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestChangeSliceWriteGateAllowsFilesCoveredByDirectoryTargets(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "apply directory slices")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "config", Title: "Config", ExecutionMode: "write", TargetFiles: []string{"internal/config"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "config", Status: "planned"},
		{ID: "cache", Title: "Cache", ExecutionMode: "write", DependsOn: []string{"config"}, TargetFiles: []string{"internal/cache"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "cache", Status: "planned"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	writeTool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)}
	execCtx := ExecutionContext{
		Profile:                config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:                  []schema.Tool{writeTool},
		Session:                state,
		WorkflowRunID:          runID,
		WorkflowName:           "complex-project-delivery",
		WorkflowStage:          "implement",
		ChangeSliceEnforcement: true,
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"internal/cache/cache.go","content":"x"}`)}); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("dependent directory write should wait for its slice: %v", err)
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"internal/config/config.go","content":"x"}`)}); err != nil {
		t.Fatalf("directory target should cover package files: %v", err)
	}
	if err := state.MarkWorkflowRunChangeSliceFilesDurable(runID, "implement", "config", []string{"internal/config/config.go"}); err != nil {
		t.Fatalf("record directory write: %v", err)
	}
	run, ok := state.WorkflowRun(runID)
	if !ok || run.ChangeSlices[0].Status != "implemented" {
		t.Fatalf("writing a file under a directory target should complete the slice: %#v", run.ChangeSlices)
	}
}

func TestChangeSliceWriteGateFallsBackToReadySliceForUnlistedWorkspaceFile(t *testing.T) {
	state := session.New(4)
	runID := state.StartWorkflowRun("complex-project-delivery", "apply extra file")
	slices := []session.WorkflowChangeSliceSnapshot{
		{ID: "config", Title: "Config", ExecutionMode: "write", TargetFiles: []string{"internal/config"}, VerificationCommands: []string{"go test ./..."}, ContractHash: "config", Status: "planned"},
	}
	if err := state.UpdateWorkflowRunChangeSlicesDurable(runID, slices); err != nil {
		t.Fatalf("persist slices: %v", err)
	}
	writeTool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)}
	execCtx := ExecutionContext{
		Profile:                config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:                  []schema.Tool{writeTool},
		Session:                state,
		WorkflowRunID:          runID,
		WorkflowName:           "complex-project-delivery",
		WorkflowStage:          "implement",
		ChangeSliceEnforcement: true,
	}
	if _, err := execCtx.validateToolCall(schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"cmd/platform/main.go","content":"package main"}`)}); err != nil {
		t.Fatalf("ready write slice should accept an unlisted workspace file: %v", err)
	}
	execCtx.recordChangeSliceToolSuccess(schema.ToolCall{Name: writeTool.Name, Arguments: []byte(`{"path":"cmd/platform/main.go","content":"package main"}`)}, writeTool, schema.ToolResult{Content: `{"relative_path":"cmd/platform/main.go"}`})
	run, _ := state.WorkflowRun(runID)
	if len(run.ChangeSlices[0].ChangedFiles) != 1 || run.ChangeSlices[0].ChangedFiles[0] != "cmd/platform/main.go" {
		t.Fatalf("an allowed fallback write must remain attached to its ready slice: %#v", run.ChangeSlices)
	}
}
