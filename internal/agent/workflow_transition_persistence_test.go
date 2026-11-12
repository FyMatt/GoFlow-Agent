package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestWorkflowEventPersistsBeforePublish(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".goflow", "session.json")
	state := session.New(8)
	state.SetPersistPath(path)
	runID := state.StartWorkflowRun("delivery", "finish the task")
	runner := NewWorkflowRunner(&Runtime{session: state})
	published := false
	handler := runner.recordWorkflowRunEvents(runID, func(event schema.StreamEvent) error {
		published = true
		onDisk := session.New(8)
		if err := onDisk.Load(path); err != nil {
			t.Fatalf("load transition inside publish handler: %v", err)
		}
		run, ok := onDisk.WorkflowRun(runID)
		if !ok || run.Status != "completed" {
			t.Fatalf("expected completed transition to be durable before publish, got %#v", run)
		}
		last := run.Events[len(run.Events)-1]
		if last.Type != string(schema.StreamEventWorkflowResult) || last.Content != "done" {
			t.Fatalf("expected durable workflow result before publish, got %#v", last)
		}
		return nil
	})
	err := handler(schema.StreamEvent{
		Type:           schema.StreamEventWorkflowResult,
		Content:        "done",
		WorkflowName:   "delivery",
		WorkflowStatus: "completed",
	})
	if err != nil {
		t.Fatalf("publish durable workflow event: %v", err)
	}
	if !published {
		t.Fatal("expected handler after durable transition")
	}
}

func TestWorkflowEventPersistenceFailurePreventsPublish(t *testing.T) {
	state := session.New(8)
	runID := state.StartWorkflowRun("delivery", "finish the task")
	state.SetPersistPath(t.TempDir()) // Saving a session over an existing directory must fail.
	runner := NewWorkflowRunner(&Runtime{session: state})
	published := false
	handler := runner.recordWorkflowRunEvents(runID, func(event schema.StreamEvent) error {
		published = true
		return nil
	})
	err := handler(schema.StreamEvent{Type: schema.StreamEventTaskStage, Content: "implementation started"})
	if err == nil || !strings.Contains(err.Error(), "persist workflow event before publish") {
		t.Fatalf("expected persistence failure, got %v", err)
	}
	if published {
		t.Fatal("event must not be published when its transition was not persisted")
	}
}

func TestWorkflowPauseStatusPersistsWithCompletedStages(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".goflow", "session.json")
	state := session.New(8)
	state.SetPersistPath(path)
	runID := state.StartWorkflowRun("delivery", "finish the task")
	runner := NewWorkflowRunner(&Runtime{session: state})
	runner.runID = runID

	runner.persistWorkflowState("delivery", "awaiting_approval", "approve-plan", "finish the task", "plan ready", "approve the plan")
	before, ok := state.WorkflowRun(runID)
	if !ok || before.Status != "running" || len(before.CompletedStages) != 0 {
		t.Fatalf("pause status must not become visible before completed stages, got %#v", before)
	}

	runner.completeWorkflowRun(runID, WorkflowResult{
		Name:            "delivery",
		Status:          "awaiting_approval",
		NextStage:       "approve-plan",
		ApprovalPrompt:  "approve the plan",
		PendingApproval: true,
		CompletedStages: []WorkflowStageResult{{
			Stage:  "plan",
			Status: "completed",
			Output: WorkflowStageOutput{Artifacts: []session.WorkflowRunArtifact{{
				ID:      "delivery-plan",
				Stage:   "plan",
				Kind:    "plan",
				Title:   "Delivery plan",
				Summary: "bounded delivery plan",
				Metadata: map[string]string{
					"declared": "true",
				},
			}}},
		}},
	})

	onDisk := session.New(8)
	if err := onDisk.Load(path); err != nil {
		t.Fatalf("load persisted pause result: %v", err)
	}
	paused, ok := onDisk.WorkflowRun(runID)
	if !ok || paused.Status != "awaiting_approval" || len(paused.CompletedStages) != 1 || len(paused.CompletedStages[0].Artifacts) == 0 {
		t.Fatalf("expected pause status and completed stage artifact to persist together, got %#v", paused)
	}
}
