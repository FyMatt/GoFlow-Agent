package session

import "testing"

func TestWorkflowStageTimesSurviveLaterCheckpoints(t *testing.T) {
	run := WorkflowRunSnapshot{StartedAt: "2026-10-22T00:00:00Z", CompletedStages: []WorkflowRunStageSnapshot{
		{Stage: "plan", Status: "completed", StartedAt: "2026-10-22T00:00:01Z", CompletedAt: "2026-10-22T00:01:00Z"},
	}}
	stages := enrichWorkflowRunStages(run, []WorkflowRunStageSnapshot{{Stage: "plan", Status: "completed"}, {Stage: "repair", Status: "incomplete"}}, "2026-10-22T00:10:00Z")
	if stages[0].CompletedAt != run.CompletedStages[0].CompletedAt {
		t.Fatalf("later checkpoint changed an already finished stage: %#v", stages[0])
	}
	if stages[1].CompletedAt != "" {
		t.Fatalf("incomplete stage must not have a completion time: %#v", stages[1])
	}
	run.CompletedStages = stages
	stages = enrichWorkflowRunStages(run, []WorkflowRunStageSnapshot{{Stage: "repair", Status: "completed"}}, "2026-10-22T00:15:00Z")
	if stages[0].CompletedAt != "2026-10-22T00:15:00Z" {
		t.Fatalf("resumed stage must finish at its actual final checkpoint: %#v", stages[0])
	}
}
