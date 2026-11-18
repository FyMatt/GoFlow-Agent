package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestStageSnapshotsDoNotTurnReadEvidenceIntoWritePlans(t *testing.T) {
	read := schema.ToolResult{ToolName: "file_tools/read_file", Content: `{"relative_path":"go.mod","sha256":"` + strings.Repeat("a", 64) + `"}`}
	plan := "```goflow-change-slices\n" + `[{"id":"cache","title":"Implement cache","mode":"write","depends_on":[],"input_files":[],"target_files":["ttlcache/cache.go","ttlcache/regression_test.go"],"verification_commands":["go test ./..."]}]` + "\n```"
	snapshots := workflowRunStageSnapshots([]WorkflowStageResult{
		{Stage: "clarify", Result: schema.AgentResult{Output: "go.mod remains unchanged", ToolResults: []schema.ToolResult{read}}},
		{Stage: "plan", Result: schema.AgentResult{Output: plan}},
	})
	if len(snapshots[0].ChangeSlices) != 0 {
		t.Fatal("read-only evidence manufactured a writable plan")
	}
	if len(snapshots[1].ChangeSlices) != 1 || snapshots[1].ChangeSlices[0].TargetFiles[0] != "ttlcache/cache.go" {
		t.Fatalf("the actual declared plan was lost: %#v", snapshots[1].ChangeSlices)
	}
	state := session.New(4)
	runID := state.StartWorkflowRun("custom", "Implement the requested cache")
	for _, snapshot := range snapshots {
		if err := state.AdvanceWorkflowRunChangeSlicesDurable(runID, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	run, _ := state.WorkflowRun(runID)
	if len(run.ChangeSlices) != 1 || run.ChangeSlices[0].TargetFiles[0] != "ttlcache/cache.go" {
		t.Fatalf("durable run plan was replaced by read evidence: %#v", run.ChangeSlices)
	}
}

func TestRequiredChangeSlicesRejectReadOnlyFallbackEvidence(t *testing.T) {
	result := schema.AgentResult{Output: "A plan without the required machine block", ToolResults: []schema.ToolResult{{
		ToolName: "file_tools/list_tree", Content: `{"entries":[{"relative_path":"go.mod","is_dir":false}]}`,
	}}}
	if workflowGraphResultHasChangeSlices(result) {
		t.Fatal("a repository listing satisfied the required plan contract")
	}
}

func TestMalformedDeclaredSlicesDoNotFallBackToReadEvidence(t *testing.T) {
	reads := []schema.ToolResult{{ToolName: "file_tools/list_tree", Content: `{"entries":[{"relative_path":"go.mod","is_dir":false}]}`}}
	for _, output := range []string{"goflow-change-slices\n[]", "goflow-change-slices\n[not-json]"} {
		if slices := compileWorkflowChangeSlices(output, reads); len(slices) != 0 {
			t.Fatalf("malformed explicit plan became guessed writes: %#v", slices)
		}
	}
}
