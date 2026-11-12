package session

import (
	"strings"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// publishToolResultArtifact reports whether a tool result is useful as a
// user-facing deliverable. All tool results remain available in the durable
// run result and event log; this only keeps routine inspection traffic out of
// the artifact list.
func publishToolResultArtifact(result schema.ToolResult) bool {
	if result.IsError || result.Denied || result.Suspended {
		return true
	}
	name := workflowArtifactToolBaseName(result.ToolName)
	if name == "" {
		return false
	}
	switch name {
	case "read_file", "read_file_range", "list_dir", "list_tree", "file_info", "search_files", "repository_map",
		"office_capabilities", "inspect_office_document":
		return false
	default:
		return strings.TrimSpace(result.Content) != "" || strings.TrimSpace(result.ArtifactRef) != "" || strings.TrimSpace(result.ArtifactHash) != ""
	}
}

func publishStructuredArtifact(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "summary", "provider", "mode", "skill", "output_kind", "tools", "reflection", "workflow":
		return false
	default:
		return true
	}
}

func publishAuditArtifact(outcome string) bool {
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	for _, marker := range []string{"error", "fail", "deny", "block", "suspend", "cancel", "timeout"} {
		if strings.Contains(outcome, marker) {
			return true
		}
	}
	return false
}

func publishVerificationArtifact(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if strings.HasPrefix(kind, "acceptance:") {
		return false
	}
	switch kind {
	case "", "response", "plan", "tool":
		return false
	default:
		return true
	}
}

// publishAcceptanceArtifact keeps failed or unresolved acceptance evidence
// visible while omitting routine successful checks that are already captured
// by the quality gate summary.
func publishAcceptanceArtifact(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "passed", "pass", "success", "succeeded":
		return false
	default:
		return true
	}
}

func workflowArtifactToolBaseName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if index := strings.LastIndexAny(name, "/\\"); index >= 0 {
		name = name[index+1:]
	}
	return strings.TrimSpace(name)
}
