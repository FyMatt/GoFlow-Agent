package api

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestProcessPreviewUsesLastLifecycleState(t *testing.T) {
	start := schema.ToolResult{ToolName: "start_process", Content: `{"stdout":"Local: http://0.0.0.0:5001","running":true,"exit_code":-1}`}
	previews := runPreviews([]schema.ToolResult{start})
	if len(previews) != 1 || previews[0].URL != "http://127.0.0.1:5001" || !previews[0].Running {
		t.Fatalf("missing preview: %#v", previews)
	}
	stop := schema.ToolResult{ToolName: "stop_process", Content: `{"stdout":"Local: http://0.0.0.0:5001","running":false,"stop_requested":true,"exit_code":1}`}
	previews = runPreviews([]schema.ToolResult{start, stop})
	if len(previews) != 1 || previews[0].Running {
		t.Fatalf("stopped service advertised as live: %#v", previews)
	}
	if got := processPreview(map[string]any{"stdout": "http://user:password@localhost:5000", "running": true}); got != nil {
		t.Fatal("credential-bearing URL must not be displayed")
	}
}

func TestTimelineFileMetadataSurvivesLongContent(t *testing.T) {
	content := `{"content":"` + strings.Repeat("large source ", 500) + `","relative_path":"tests/test_ui_smoke.py","status":"created","added_lines":56,"deleted_lines":0}`
	metadata := timelineToolMetadata("tool_result", content)
	if metadata["relative_path"] != "tests/test_ui_smoke.py" || metadata["added_lines"] != float64(56) || metadata["deleted_lines"] != float64(0) || metadata["content"] != nil {
		t.Fatalf("file metadata lost or source leaked: %#v", metadata)
	}
}
