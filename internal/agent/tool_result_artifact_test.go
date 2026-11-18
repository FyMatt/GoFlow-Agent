package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestMaterializeToolResultUsesOneEvidenceContractForSuccessErrorDeniedAndSuspended(t *testing.T) {
	state := session.New(8)
	inputs := []schema.ToolResult{
		{CallID: "ok", ToolName: "read_file", Content: "contents"},
		{CallID: "error", ToolName: "run_command", Content: "exit 1", IsError: true},
		{CallID: "denied", ToolName: "write_file", Content: "denied by policy", IsError: true, Denied: true},
		{CallID: "suspended", ToolName: "write_file", Content: "requires approval", Suspended: true},
	}
	for _, input := range inputs {
		result := materializeToolResult(state, input, "fixer", "fix", "run-1", "implement")
		if result.Summary == "" || result.ArtifactRef == "" || result.ArtifactHash == "" || result.ContentBytes == 0 || result.StoredBytes == 0 {
			t.Fatalf("result missing normalized evidence metadata: %#v", result)
		}
		if result.ArtifactRetention != "session" || result.Redaction != "none" {
			t.Fatalf("result missing retention/redaction metadata: %#v", result)
		}
	}
	if got := len(state.Artifacts(session.SessionArtifactFilter{Kind: "tool_result", Limit: 20})); got != len(inputs) {
		t.Fatalf("expected one artifact per material result, got %d", got)
	}
}

func TestMaterializeToolResultDoesNotDuplicateExistingArtifact(t *testing.T) {
	state := session.New(8)
	first := materializeToolResult(state, schema.ToolResult{CallID: "call-1", ToolName: "read_file", Content: strings.Repeat("x", 32)}, "auditor", "audit", "", "")
	second := materializeToolResult(state, first, "auditor", "audit", "", "")
	if first.ArtifactRef != second.ArtifactRef || first.ArtifactHash != second.ArtifactHash {
		t.Fatalf("existing artifact identity changed: first=%#v second=%#v", first, second)
	}
	if got := len(state.Artifacts(session.SessionArtifactFilter{Kind: "tool_result", Limit: 20})); got != 1 {
		t.Fatalf("expected one artifact after rematerializing result, got %d", got)
	}
}

func TestMaterializeToolResultRetainsLargeExactContentInArtifactStore(t *testing.T) {
	state := session.New(8)
	state.SetArtifactObjectStore(session.NewArtifactObjectStore(t.TempDir()))
	content := strings.Repeat("large-output\n", 5000)
	result := materializeToolResult(state, schema.ToolResult{CallID: "large", ToolName: "run_command", Content: content}, "fixer", "fix", "run-2", "test")
	if result.Content != content || result.ArtifactRef == "" || result.ArtifactHash == "" {
		t.Fatalf("normalized result should preserve exact content while exposing artifact ref: %#v", result)
	}
	artifact, ok := state.Artifact(result.ArtifactRef)
	if !ok || artifact.Content != content || artifact.WorkflowRunID != "run-2" || artifact.Stage != "test" {
		t.Fatalf("artifact did not preserve exact result context: ok=%t artifact=%#v", ok, artifact)
	}
}
