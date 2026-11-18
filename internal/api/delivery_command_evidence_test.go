package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestDeliveryManifestUsesFullCommandEvidence(t *testing.T) {
	for _, kind := range []string{"agent", "workflow"} {
		for _, externalized := range []bool{false, true} {
			for _, exitCode := range []int{0, 1} {
				name := kind + "/inline"
				if externalized {
					name = kind + "/externalized"
				}
				if exitCode != 0 {
					name += "/failed"
				}
				t.Run(name, func(t *testing.T) {
					state := session.New(8)
					padding := strings.Repeat("command metadata ", 80)
					if externalized {
						store := session.NewArtifactObjectStore(t.TempDir())
						if err := store.Ensure(); err != nil {
							t.Fatal(err)
						}
						state.SetArtifactObjectStore(store)
						padding = strings.Repeat(padding, 8)
					}
					payload, err := json.Marshal(map[string]any{
						"audit_context": padding, "program": "go", "args": []string{"test", "./..."},
						"exit_code": exitCode, "stdout": "actual test output",
					})
					if err != nil {
						t.Fatal(err)
					}
					result := schema.AgentResult{
						Output:  "implemented and verified",
						Changes: []schema.Change{{Summary: "updated cache", Files: []string{"cache.go"}}},
						ToolResults: []schema.ToolResult{
							{CallID: "write-1", ToolName: "file_tools/apply_patch", Content: `{"relative_path":"cache.go","status":"modified","added_lines":1,"deleted_lines":1,"diff_preview":"- old\n+ new"}`},
							{CallID: "test-1", ToolName: "command_tools/run_command", Content: string(payload)},
						},
						Verification: []schema.Verification{{Kind: "tests", Status: "passed", Detail: "go test ./..."}},
					}
					var runID string
					if kind == "agent" {
						runID = state.StartAgentRun("implement and test")
						state.CompleteAgentRun(runID, "completed", result)
					} else {
						runID = state.StartWorkflowRun("delivery", "implement and test")
						state.CompleteWorkflowRun(runID, "completed", "done", "", "", nil, []session.WorkflowRunStageSnapshot{{Stage: "implement", Status: "completed", Result: result}})
					}
					server := NewServer(newAPITestRuntimeWithStateAndResponses(t, state, nil))
					var manifest runDeliveryManifest
					if kind == "agent" {
						run, _ := state.AgentRun(runID)
						manifest = server.agentRunDeliveryManifest(run)
					} else {
						run, _ := state.WorkflowRun(runID)
						manifest = server.workflowRunDeliveryManifest(run)
					}
					assertDeliveryGate(t, manifest, "changes_verified", exitCode == 0)
					assertDeliveryGate(t, manifest, "no_tool_errors", exitCode == 0)
					if (manifest.Conclusion == "success") != (exitCode == 0) {
						t.Fatalf("delivery must use the full command result, got %s", manifest.Conclusion)
					}
				})
			}
		}
	}
}

func TestDeliveryDoesNotGradePerformedReflectionAsVerification(t *testing.T) {
	checks := deliveryChecksFromVerifications("review", []schema.Verification{
		{Kind: "reflection", Status: "performed", Detail: "internal self-check recorded"},
		{Kind: "reflection", Status: "failed", Detail: "actual failed check"},
		{Kind: "tests", Status: "performed", Detail: "test outcome is not known"},
	})
	if len(checks) != 2 || checks[0].Status != "failed" || checks[1].Status == "passed" {
		t.Fatalf("only performed reflection metadata should be omitted, got %#v", checks)
	}
}
