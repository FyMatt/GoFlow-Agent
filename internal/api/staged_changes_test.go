package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestAgentRunApplyStagedChangesRequiresAndUsesDeliveryQualityGate(t *testing.T) {
	state := session.New(8)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	workspace := runtimeRef.WorkspaceRoot()
	if err := os.WriteFile(filepath.Join(workspace, "demo.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runID := runtimeRef.StartAgentRun("update demo")
	task, err := runtimeRef.PrepareTaskStagingForRun(runID)
	if err != nil {
		t.Fatalf("PrepareTaskStagingForRun: %v", err)
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	if err := os.WriteFile(filepath.Join(task.Workspace, "demo.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writePayload := map[string]any{
		"relative_path": "demo.txt",
		"status":        "modified",
		"added_lines":   1,
		"deleted_lines": 1,
		"bytes_written": 6,
		"diff_preview":  "@@ -1,1 +1,1 @@\n-before\n+after",
	}
	writeJSONPayload, _ := json.Marshal(writePayload)
	commandJSONPayload := `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok","duration_ms":10}`
	runtimeRef.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:  "implemented and verified",
		AgentID: "chat",
		Mode:    "fix",
		ToolResults: []schema.ToolResult{
			{CallID: "write-1", ToolName: "file_tools/write_file", Content: string(writeJSONPayload)},
			{CallID: "test-1", ToolName: "command_tools/run_command", Content: commandJSONPayload},
		},
		Verification: []schema.Verification{{Kind: "test", Status: "passed", Detail: "go test ./... passed"}},
	})
	run, ok := runtimeRef.AgentRun(runID)
	if !ok {
		t.Fatal("completed agent run not found")
	}
	server := NewServer(runtimeRef)
	action, ok := server.agentRunStagedChangesAction(run)
	if !ok || !action.Available || action.Name != "apply_staged_changes" {
		t.Fatalf("expected available staged apply action, ok=%v action=%#v", ok, action)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+runID+"/apply-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("apply staged changes status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(workspace, "demo.txt"))
	if err != nil || string(data) != "after\n" {
		t.Fatalf("staged changes were not applied: %q err=%v", data, err)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+runID+"/apply-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "already applied") {
		t.Fatalf("expected duplicate apply conflict, status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	run, _ = runtimeRef.AgentRun(runID)
	restoreAction, ok := server.agentRunStagedChangesAction(run)
	if !ok || !restoreAction.Available || restoreAction.Name != "restore_staged_changes" || restoreAction.RecoveryID == "" || restoreAction.ChangeCount != 1 {
		t.Fatalf("expected staged restore action, ok=%v action=%#v", ok, restoreAction)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+runID+"/restore-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restore staged changes status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data, err = os.ReadFile(filepath.Join(workspace, "demo.txt"))
	if err != nil || string(data) != "before\n" {
		t.Fatalf("staged changes were not restored: %q err=%v", data, err)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+runID+"/restore-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "not in an applied state") {
		t.Fatalf("expected duplicate restore conflict, status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	run, _ = runtimeRef.AgentRun(runID)
	discardAction, ok := server.agentRunDiscardStagedChangesAction(run)
	if ok || discardAction.Name != "" {
		t.Fatalf("restored live version must not expose a second discard action, ok=%v action=%#v", ok, discardAction)
	}
}

func TestAgentRunApplyStagedChangesRejectsUnverifiedDelivery(t *testing.T) {
	state := session.New(8)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	workspace := runtimeRef.WorkspaceRoot()
	if err := os.WriteFile(filepath.Join(workspace, "demo.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runID := runtimeRef.StartAgentRun("update demo")
	task, err := runtimeRef.PrepareTaskStagingForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	if err := os.WriteFile(filepath.Join(task.Workspace, "demo.txt"), []byte("unverified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtimeRef.CompleteAgentRun(runID, "completed", schema.AgentResult{Output: "model says done", AgentID: "chat", Mode: "fix"})
	run, _ := runtimeRef.AgentRun(runID)
	server := NewServer(runtimeRef)
	action, ok := server.agentRunStagedChangesAction(run)
	if !ok || action.Available || !strings.Contains(action.Reason, "质量门") {
		t.Fatalf("expected disabled quality-gated action, ok=%v action=%#v", ok, action)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+runID+"/apply-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "质量门") {
		t.Fatalf("expected quality gate conflict, status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(workspace, "demo.txt"))
	if err != nil || string(data) != "unverified\n" {
		t.Fatalf("unverified delivery must remain visible in the live workspace: %q err=%v", data, err)
	}
	discard, ok := server.agentRunDiscardStagedChangesAction(run)
	if !ok || !discard.Available || discard.WorkspaceMode != "live" {
		t.Fatalf("unverified live changes must offer rollback: ok=%v action=%#v", ok, discard)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+runID+"/discard-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("discard live changes status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data, err = os.ReadFile(filepath.Join(workspace, "demo.txt"))
	if err != nil || string(data) != "before\n" {
		t.Fatalf("discard must restore the pre-run workspace: %q err=%v", data, err)
	}
}

func TestStagedChangesDeliveryAllowedWhenConclusionFailedButChangesExist(t *testing.T) {
	allowed, _ := stagedChangesDeliveryAllowed(runDeliveryManifest{Conclusion: "failed", Changes: []runDiffItem{{Path: "cache.go"}}})
	if !allowed {
		t.Fatal("failed conclusion with staged diffs must still allow writeback")
	}
	allowed, _ = stagedChangesDeliveryAllowed(runDeliveryManifest{Conclusion: "failed"})
	if allowed {
		t.Fatal("failed conclusion without diffs must not allow writeback")
	}
	allowed, _ = stagedChangesDeliveryAllowed(runDeliveryManifest{Conclusion: "running", Changes: []runDiffItem{{Path: "cache.go"}}})
	if allowed {
		t.Fatal("running tasks must not write back")
	}
}

func TestWorkflowRunApplyStagedChangesRequiresAndUsesDeliveryQualityGate(t *testing.T) {
	state := session.New(8)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	workspace := runtimeRef.WorkspaceRoot()
	if err := os.WriteFile(filepath.Join(workspace, "workflow.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runID := state.StartWorkflowRun("engineering-delivery", "update and verify workflow.txt")
	task, err := runtimeRef.PrepareTaskStagingForRun(runID)
	if err != nil {
		t.Fatalf("PrepareTaskStagingForRun: %v", err)
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	if err := os.WriteFile(filepath.Join(task.Workspace, "workflow.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state.CompleteWorkflowRun(runID, "completed", "delivery complete", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:  "implement",
		Status: "completed",
		Result: schema.AgentResult{
			Output: "implemented and verified",
			ToolResults: []schema.ToolResult{
				{CallID: "write-1", ToolName: "file_tools/write_file", Content: `{"relative_path":"workflow.txt","status":"modified","added_lines":1,"deleted_lines":1,"bytes_written":6,"diff_preview":"@@ -1,1 +1,1 @@\n-before\n+after"}`},
				{CallID: "test-1", ToolName: "command_tools/run_command", Content: `{"program":"go","args":["test","./..."],"exit_code":0,"stdout":"ok","duration_ms":10}`},
			},
			Verification: []schema.Verification{{Kind: "test", Status: "passed", Detail: "go test ./... passed"}},
		},
		Acceptance: []session.WorkflowRunAcceptanceSnapshot{{Name: "implementation", Expected: "completed", Actual: "completed", Status: "passed"}},
	}})
	run, ok := state.WorkflowRun(runID)
	if !ok {
		t.Fatal("completed workflow run not found")
	}
	server := NewServer(runtimeRef)
	action, ok := server.workflowRunStagedChangesAction(run)
	if !ok || !action.Available || action.Name != "apply_staged_changes" {
		t.Fatalf("expected available staged apply action, ok=%v action=%#v", ok, action)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+runID+"/apply-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("apply staged workflow changes status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(workspace, "workflow.txt"))
	if err != nil || string(data) != "after\n" {
		t.Fatalf("staged workflow changes were not applied: %q err=%v", data, err)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+runID+"/apply-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "already applied") {
		t.Fatalf("expected duplicate workflow apply conflict, status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	run, _ = state.WorkflowRun(runID)
	restoreAction, ok := server.workflowRunStagedChangesAction(run)
	if !ok || !restoreAction.Available || restoreAction.Name != "restore_staged_changes" || restoreAction.RecoveryID == "" || restoreAction.ChangeCount != 1 {
		t.Fatalf("expected workflow staged restore action, ok=%v action=%#v", ok, restoreAction)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+runID+"/restore-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restore staged workflow changes status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data, err = os.ReadFile(filepath.Join(workspace, "workflow.txt"))
	if err != nil || string(data) != "before\n" {
		t.Fatalf("staged workflow changes were not restored: %q err=%v", data, err)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+runID+"/restore-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "not in an applied state") {
		t.Fatalf("expected duplicate workflow restore conflict, status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	run, _ = state.WorkflowRun(runID)
	discardAction, ok := server.workflowRunDiscardStagedChangesAction(run)
	if ok || discardAction.Name != "" {
		t.Fatalf("restored live workflow version must not expose discard, ok=%v action=%#v", ok, discardAction)
	}
}

func TestWorkflowRunApplyStagedChangesRejectsUnverifiedDelivery(t *testing.T) {
	state := session.New(8)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	workspace := runtimeRef.WorkspaceRoot()
	if err := os.WriteFile(filepath.Join(workspace, "workflow.txt"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runID := state.StartWorkflowRun("engineering-delivery", "update workflow.txt")
	task, err := runtimeRef.PrepareTaskStagingForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeRef.ReleaseTaskStaging(runID) })
	if err := os.WriteFile(filepath.Join(task.Workspace, "workflow.txt"), []byte("unverified\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state.CompleteWorkflowRun(runID, "completed", "model says done", "", "", nil, []session.WorkflowRunStageSnapshot{{
		Stage:  "deliver",
		Status: "completed",
		Result: schema.AgentResult{Output: "all changes completed"},
	}})
	run, _ := state.WorkflowRun(runID)
	server := NewServer(runtimeRef)
	action, ok := server.workflowRunStagedChangesAction(run)
	if !ok || action.Available || !strings.Contains(action.Reason, "质量门") {
		t.Fatalf("expected disabled workflow quality-gated action, ok=%v action=%#v", ok, action)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+runID+"/apply-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "质量门") {
		t.Fatalf("expected workflow quality gate conflict, status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(workspace, "workflow.txt"))
	if err != nil || string(data) != "unverified\n" {
		t.Fatalf("unverified workflow delivery must remain in the live workspace: %q err=%v", data, err)
	}
	discard, ok := server.workflowRunDiscardStagedChangesAction(run)
	if !ok || !discard.Available || discard.WorkspaceMode != "live" {
		t.Fatalf("unverified workflow changes must offer rollback: ok=%v action=%#v", ok, discard)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/workflow-runs/"+runID+"/discard-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("discard live workflow changes status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data, err = os.ReadFile(filepath.Join(workspace, "workflow.txt"))
	if err != nil || string(data) != "before\n" {
		t.Fatalf("workflow discard must restore the pre-run workspace: %q err=%v", data, err)
	}
}

func TestWorkspaceVersionsEndpointListsAndRestoresLatestVersion(t *testing.T) {
	state := session.New(8)
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, state, nil)
	workspace := runtimeRef.WorkspaceRoot()
	path := filepath.Join(workspace, "version.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runID := runtimeRef.StartAgentRun("update version")
	task, err := runtimeRef.PrepareTaskStagingForRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.Workspace, "version.txt"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runtimeRef.CompleteAgentRun(runID, "completed", schema.AgentResult{
		Output:       "implemented and verified",
		AgentID:      "chat",
		Mode:         "fix",
		ToolResults:  []schema.ToolResult{{CallID: "write", ToolName: "file_tools/write_file", Content: `{"relative_path":"version.txt","status":"modified"}`}},
		Verification: []schema.Verification{{Kind: "test", Status: "passed", Detail: "smoke passed"}},
	})
	server := NewServer(runtimeRef)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent-runs/"+runID+"/apply-staged-changes", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("save workspace version status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/workspace-versions", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list workspace versions status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var versions workspaceVersionsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &versions); err != nil {
		t.Fatal(err)
	}
	if versions.Mode != "live" || versions.Retention != 3 || len(versions.Versions) != 1 || !versions.Versions[0].CanRestore {
		t.Fatalf("unexpected workspace versions response: %#v", versions)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/workspace-versions/"+runID+"/restore", nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("restore workspace version status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "before\n" {
		t.Fatalf("workspace version restore failed: %q err=%v", data, err)
	}
}
