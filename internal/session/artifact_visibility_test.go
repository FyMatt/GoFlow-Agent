package session

import (
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestPublishToolResultArtifactFiltersRoutineInspection(t *testing.T) {
	tests := []struct {
		name string
		in   schema.ToolResult
		want bool
	}{
		{name: "file read", in: schema.ToolResult{ToolName: "file_tools/read_file", Content: "source"}, want: false},
		{name: "directory list", in: schema.ToolResult{ToolName: "list_tree", Content: "files"}, want: false},
		{name: "office inspection", in: schema.ToolResult{ToolName: "office_tools/inspect_office_document", Content: "text"}, want: false},
		{name: "write", in: schema.ToolResult{ToolName: "file_tools/write_file", Content: "changed"}, want: true},
		{name: "command", in: schema.ToolResult{ToolName: "command_tools/run_command", Content: "tests passed"}, want: true},
		{name: "web evidence", in: schema.ToolResult{ToolName: "web_tools/fetch_url", Content: "page"}, want: true},
		{name: "office delivery", in: schema.ToolResult{ToolName: "office_tools/create_docx", Content: "document"}, want: true},
		{name: "failed read", in: schema.ToolResult{ToolName: "read_file", Content: "denied", IsError: true}, want: true},
		{name: "externalized read remains routine", in: schema.ToolResult{ToolName: "read_file", ArtifactRef: "sha256:abc"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := publishToolResultArtifact(test.in); got != test.want {
				t.Fatalf("publishToolResultArtifact() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestPublishVerificationArtifactFiltersDerivedMetadata(t *testing.T) {
	for _, kind := range []string{"response", "plan", "tool", "acceptance:has-goal"} {
		if publishVerificationArtifact(kind) {
			t.Fatalf("expected %q verification to remain in run diagnostics only", kind)
		}
	}
	for _, kind := range []string{"go test", "go vet", "format", "security scan"} {
		if !publishVerificationArtifact(kind) {
			t.Fatalf("expected %q verification to be user-facing evidence", kind)
		}
	}
}

func TestPublishAcceptanceArtifactKeepsOnlyActionableEvidence(t *testing.T) {
	for _, status := range []string{"passed", "pass", "success", "succeeded"} {
		if publishAcceptanceArtifact(status) {
			t.Fatalf("expected successful acceptance status %q to remain in diagnostics only", status)
		}
	}
	for _, status := range []string{"failed", "warning", "unknown", ""} {
		if !publishAcceptanceArtifact(status) {
			t.Fatalf("expected actionable acceptance status %q to remain user-facing", status)
		}
	}
}

func TestBuildWorkflowRunStageArtifactsKeepsDeliverablesOnly(t *testing.T) {
	stage := WorkflowRunStageSnapshot{
		Stage: "implement",
		Result: schema.AgentResult{
			Output: "implementation complete",
			ToolResults: []schema.ToolResult{
				{CallID: "read", ToolName: "file_tools/read_file", Content: "source"},
				{CallID: "write", ToolName: "file_tools/write_file", Content: "changed"},
				{CallID: "test", ToolName: "command_tools/run_command", Content: "ok"},
			},
			Structured: []schema.StructuredSection{
				{Kind: "summary", Title: "Summary", Summary: "implementation complete"},
				{Kind: "tools", Title: "Tools", Items: []string{"read_file", "write_file"}},
			},
			Verification: []schema.Verification{{Kind: "go test", Status: "passed", Detail: "ok"}},
		},
	}
	artifacts := buildWorkflowRunStageArtifacts(stage)
	if len(artifacts) != 2 {
		t.Fatalf("expected write and command artifacts without duplicate verification, got %#v", artifacts)
	}
	for _, artifact := range artifacts {
		if artifact.ToolCallID == "read" || artifact.Kind == "structured" || artifact.Kind == "output" {
			t.Fatalf("unexpected routine artifact %#v", artifact)
		}
	}
}

func TestBuildWorkflowRunStageArtifactsUsesOutputAsFallback(t *testing.T) {
	artifacts := buildWorkflowRunStageArtifacts(WorkflowRunStageSnapshot{
		Stage: "inspect",
		Result: schema.AgentResult{
			Output:      "inspection summary",
			ToolResults: []schema.ToolResult{{ToolName: "read_file", Content: "source"}},
		},
	})
	if len(artifacts) != 1 || artifacts[0].Kind != "output" || artifacts[0].Content != "inspection summary" {
		t.Fatalf("expected one fallback stage output, got %#v", artifacts)
	}
}

func TestDeriveAgentRunArtifactsOmitsInternalMetadataAndSuccessfulAudit(t *testing.T) {
	result := schema.AgentResult{
		Output: "done",
		ToolResults: []schema.ToolResult{
			{CallID: "read", ToolName: "read_file", Content: "source"},
			{CallID: "write", ToolName: "write_file", Content: "changed"},
		},
		Structured: []schema.StructuredSection{
			{Kind: "summary", Summary: "done"},
			{Kind: "mode", Items: []string{"fix"}},
		},
		AuditTrail: []schema.AuditEntry{
			{Type: "tool", Outcome: "success", Detail: "write completed"},
			{Type: "tool", Outcome: "blocked", Detail: "policy blocked"},
		},
	}
	artifacts := deriveAgentRunArtifacts(AgentRunSnapshot{ID: "run-1", Output: result.Output, Result: &result}, "now")
	if len(artifacts) != 3 {
		t.Fatalf("expected final output, write result and blocked audit, got %#v", artifacts)
	}
	for _, artifact := range artifacts {
		if artifact.ToolCallID == "read" || artifact.Kind == "summary" || artifact.Kind == "mode" {
			t.Fatalf("unexpected internal artifact %#v", artifact)
		}
	}
}
