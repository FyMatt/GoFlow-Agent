package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// After a failed verification the repair stage starts on a workspace that the
// implement stage already changed. A repair that only re-reads files must be
// rejected with a retryable zero-mutation error, while a real repair write must
// be accepted as progress on the same already-implemented target.
func TestRepairStageZeroWriteIsRetryableAndRealWriteCounts(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "internal", "cache", "cache.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package cache\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &WorkflowRunner{runtime: &Runtime{cfg: &config.Config{WorkspaceRoot: root}}}
	// The stage baseline is taken after the earlier implementation writes.
	runner.snapshotWorkspaceMutationBaseline()
	stage := workflowGraphStage{Name: "repair", NodeType: "agent", Params: map[string]string{"require_workspace_mutation": "true"}}
	graph := workflowGraph{Name: "complex-project-delivery", Stages: []workflowGraphStage{stage}}

	readsOnly := schema.AgentResult{
		Output:      "复查后没有需要修改的地方",
		ToolResults: []schema.ToolResult{{CallID: "read-1", ToolName: "file_tools/read_file", Content: `{"path":"internal/cache/cache.go"}`}},
	}
	err := runner.workflowGraphStageCompletionError(graph, stage, readsOnly)
	if err == nil || !strings.Contains(err.Error(), "produced no workspace file mutations") {
		t.Fatalf("a repair stage that writes nothing must be rejected, got %v", err)
	}
	if !workflowGraphStageErrorIsRetryable(context.Background(), err) {
		t.Fatal("a zero-write repair must be retried with the corrective prompt, not blocked")
	}

	if err := os.WriteFile(target, []byte("package cache\n\nfunc Get() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repaired := schema.AgentResult{
		Output:      "已刷新并发缓存实现",
		ToolResults: []schema.ToolResult{{CallID: "write-1", ToolName: "file_tools/write_file", Content: `{"path":"internal/cache/cache.go","workspace_changed":true}`}},
	}
	if err := runner.workflowGraphStageCompletionError(graph, stage, repaired); err != nil {
		t.Fatalf("a repair write on an already-implemented target must count as progress: %v", err)
	}
}
