package agent

import (
	"encoding/json"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"os"
	"path/filepath"
	"testing"
)

func TestChangeSlicesRecoverFromStoredStageWhenPlanTextIsTruncated(t *testing.T) {
	state := session.New(8)
	id := state.StartWorkflowRun("recovery", "finish approved change")
	output := "goflow-change-slices\n" + `[{"id":"write","title":"Implement","mode":"write","target_files":["main.go"],"verification_commands":["go test ./..."]}]`
	slices := compileWorkflowChangeSlices(output, nil)
	if len(slices) != 1 {
		t.Fatal("invalid fixture")
	}
	state.CompleteWorkflowRun(id, "awaiting_approval", "", "implement", "", nil, []session.WorkflowRunStageSnapshot{{Stage: "plan", Status: "completed", ChangeSlices: slices, Result: schema.AgentResult{Output: output}}})
	snapshot := state.Snapshot()
	snapshot.WorkflowRuns[0].ChangeSlices = nil
	snapshot.WorkflowRuns[0].CompletedStages[0].Result.Output = "[GoFlow: content truncated for session storage]"
	bytes, _ := json.Marshal(snapshot)
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	loaded := session.New(8)
	if err := loaded.Load(path); err != nil {
		t.Fatal(err)
	}
	runner := &WorkflowRunner{runtime: &Runtime{session: loaded}, runID: id}
	got := runner.workflowChangeSlicesForRun(nil)
	if len(got) != 1 || got[0].ContractHash != slices[0].ContractHash {
		t.Fatalf("stored approved contract lost: %#v", got)
	}
	restored, _ := loaded.WorkflowRun(id)
	if len(restored.ChangeSlices) != 1 {
		t.Fatal("recovered schedule was not saved on the run")
	}
}
