package session

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResourcePinsAreDeterministicNormalizedAndIsolated(t *testing.T) {
	left := NewResourcePin(" Skill ", "code-writing", map[string]any{
		"name": "code-writing",
		"meta": map[string]any{"b": 2, "a": 1},
	})
	right := NewResourcePin("skill", "code-writing", map[string]any{
		"meta": map[string]any{"a": 1, "b": 2},
		"name": "code-writing",
	})
	if left.Version != right.Version || !strings.HasPrefix(left.Version, "sha256:") || len(left.Version) != len("sha256:")+64 {
		t.Fatalf("expected deterministic SHA-256 version, left=%#v right=%#v", left, right)
	}

	pins := NormalizeResourcePins([]ResourcePin{
		{Kind: "provider", Name: "primary", Version: "sha256:b"},
		left,
		right,
		{Kind: "agent", Name: "chat", Version: "sha256:a"},
		{Kind: "", Name: "ignored", Version: "sha256:c"},
	})
	if len(pins) != 3 || pins[0].Kind != "agent" || pins[1].Kind != "provider" || pins[2].Kind != "skill" {
		t.Fatalf("expected sorted, deduplicated pins, got %#v", pins)
	}

	copied := CopyResourcePins(pins)
	copied[0].Name = "changed"
	if pins[0].Name != "chat" {
		t.Fatalf("expected copied pins to be isolated, got %#v", pins)
	}
}

func TestTaskResourcePinsPersistAcrossSessionSaveAndLoad(t *testing.T) {
	state := New(4)
	pins := []ResourcePin{
		NewResourcePin("provider", "primary", map[string]string{"model": "test"}),
		NewResourcePin("agent", "chat", map[string]string{"provider": "primary"}),
	}
	agentID := state.StartAgentRunWithOptions("inspect", AgentRunStartOptions{AgentID: "chat", ResourcePins: pins})
	workflowID := state.StartWorkflowRunWithOptions("review", "inspect", WorkflowRunStartOptions{ResourcePins: pins})

	path := filepath.Join(t.TempDir(), "session.json")
	if err := state.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded := New(4)
	if err := loaded.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	agentRun, ok := loaded.AgentRun(agentID)
	if !ok || !reflect.DeepEqual(agentRun.ResourcePins, NormalizeResourcePins(pins)) {
		t.Fatalf("expected Agent resource pins to persist, got %#v ok=%t", agentRun.ResourcePins, ok)
	}
	workflowRun, ok := loaded.WorkflowRun(workflowID)
	if !ok || !reflect.DeepEqual(workflowRun.ResourcePins, NormalizeResourcePins(pins)) {
		t.Fatalf("expected workflow resource pins to persist, got %#v ok=%t", workflowRun.ResourcePins, ok)
	}
	if !reflect.DeepEqual(loaded.Snapshot().Workflow.ResourcePins, NormalizeResourcePins(pins)) {
		t.Fatalf("expected current workflow resource pins to persist, got %#v", loaded.Snapshot().Workflow.ResourcePins)
	}
}
