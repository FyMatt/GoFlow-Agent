package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestUnchangedPatchDoesNotBecomeAFileChange(t *testing.T) {
	for _, modeChanged := range []bool{false, true} {
		payload := map[string]any{"relative_path": "cache.go", "before_sha256": strings.Repeat("a", 64),
			"after_sha256": strings.Repeat("a", 64), "before_mode": "0644", "after_mode": "0644", "added_lines": 0, "deleted_lines": 0}
		if modeChanged {
			payload["after_mode"] = "0755"
		}
		encoded, _ := json.Marshal(payload)
		result := schema.ToolResult{ToolName: "file_tools/apply_patch", Content: string(encoded)}
		if got := workflowGraphChangedFilePathFromWriteToolResult(result); (got != "") != modeChanged {
			t.Fatalf("unchanged patch or actual mode change misclassified: modeChanged=%t path=%q", modeChanged, got)
		}
		if got := workflowGraphResultHasWorkspaceMutation(schema.AgentResult{ToolResults: []schema.ToolResult{result}}); got != modeChanged {
			t.Fatalf("mutation gate accepts a no-op or loses a mode change: %t", modeChanged)
		}
		output := WorkflowStageOutput{Values: map[string]any{"changed_files": []string{}}, Variables: map[string]string{"changed_files": "[]"}}
		reconcileWorkflowStageChangedFiles(&output, []schema.ToolResult{result})
		if (output.Variables["changed_files"] != "[]") != modeChanged {
			t.Fatalf("reconciliation fabricated a changed file: %#v", output.Values)
		}
	}
}
