package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/executionfingerprint"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func ordinaryReadCacheFixture(t *testing.T, name string) (ExecutionContext, schema.ToolCall, schema.Tool, *idempotencyMCP) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := schema.Tool{Name: "file_tools/" + name, Server: "file_tools", Kind: "read"}
	call := schema.ToolCall{ID: "first", Name: tool.Name, Arguments: []byte(`{"path":"a.txt"}`)}
	execCtx := ExecutionContext{AgentRunID: "run-1", WorkspaceRoot: root, readResultCache: newOrdinaryAgentReadCache()}
	return execCtx, call, tool, &idempotencyMCP{result: schema.ToolResult{Content: "first"}}
}

func runReadCacheCall(t *testing.T, execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool, mcp *idempotencyMCP, wantReplay bool) schema.ToolResult {
	t.Helper()
	result, replayed, err := executeToolCallWithIdempotency(context.Background(), mcp, execCtx, call, tool)
	if err != nil || replayed != wantReplay || result.CallID != call.ID {
		t.Fatalf("result=%#v replayed=%v want=%v err=%v", result, replayed, wantReplay, err)
	}
	return result
}

func TestOrdinaryReadCacheExternalChanges(t *testing.T) {
	for _, name := range []string{"read_file", "read_file_range"} {
		for _, change := range []string{"edit-preserving-size-and-mtime", "replace", "replace-identical", "delete"} {
			t.Run(name+"/"+change, func(t *testing.T) {
				execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, name)
				path := filepath.Join(execCtx.WorkspaceRoot, "a.txt")
				runReadCacheCall(t, execCtx, call, tool, mcp, false)
				call.ID = "unchanged"
				runReadCacheCall(t, execCtx, call, tool, mcp, true)
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if change == "delete" {
					err = os.Remove(path)
				} else {
					content := "other"
					if change == "replace-identical" {
						content = "first"
					}
					if strings.HasPrefix(change, "replace") {
						// Keep the original inode alive to avoid filesystem identity reuse.
						if err := os.Rename(path, path+".old"); err != nil {
							t.Fatal(err)
						}
					}
					err = os.WriteFile(path, []byte(content), 0o600)
					if err == nil {
						err = os.Chtimes(path, info.ModTime(), info.ModTime())
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				mcp.result.Content = "fresh result"
				call.ID = "changed"
				if result := runReadCacheCall(t, execCtx, call, tool, mcp, false); result.Content != "fresh result" {
					t.Fatalf("stale result: %#v", result)
				}
				if mcp.calls != 2 {
					t.Fatalf("calls=%d", mcp.calls)
				}
			})
		}
	}
}

func TestOrdinaryReadCacheNamespaceIsolation(t *testing.T) {
	for _, namespace := range []string{"workspace", "tool-workspace", "task-scope", "run", "server", "target"} {
		t.Run(namespace, func(t *testing.T) {
			execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, "read_file")
			other := t.TempDir()
			// Identical content and metadata must not erase path namespace boundaries.
			info, err := os.Stat(filepath.Join(execCtx.WorkspaceRoot, "a.txt"))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(other, "a.txt")
			if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			if namespace == "task-scope" {
				execCtx.ToolWorkspaceRoot = execCtx.WorkspaceRoot
				execCtx.ToolWorkspaceScope = schema.TaskWorkspaceScope{Root: execCtx.WorkspaceRoot, Workspace: execCtx.WorkspaceRoot, RunID: "scope-1"}
			}
			beforeKey, _ := ordinaryAgentReadCacheState(execCtx, call, tool)
			// Seed through the unscoped stub, retaining the intended scope namespace.
			key, fingerprint := ordinaryAgentReadCacheState(execCtx, call, tool)
			updateOrdinaryAgentReadCache(execCtx, call, tool, mcp.result, key, fingerprint)
			switch namespace {
			case "workspace":
				execCtx.WorkspaceRoot = other
			case "tool-workspace":
				execCtx.ToolWorkspaceRoot = other
			case "task-scope":
				execCtx.ToolWorkspaceScope.RunID = "scope-2"
			case "run":
				execCtx.AgentRunID = "run-2"
			case "server":
				tool.Server = "external"
			case "target":
				execCtx.TargetID = "remote"
			}
			mcp.result.Content = "other namespace"
			call.ID = "second"
			afterKey, _ := ordinaryAgentReadCacheState(execCtx, call, tool)
			if beforeKey == afterKey {
				t.Fatal("namespace omitted from cache key")
			}
			// The stub does not implement workspace-scoped execution.
			if execCtx.ToolWorkspaceRoot != "" {
				if _, replayed := replayOrdinaryAgentReadForTest(execCtx, call, tool); replayed {
					t.Fatal("cross-root replay")
				}
			} else {
				runReadCacheCall(t, execCtx, call, tool, mcp, false)
			}
		})
	}
}

func TestOrdinaryReadCacheCanonicalArgumentsAndUnrelatedEdits(t *testing.T) {
	execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, "read_file_range")
	call.Arguments = []byte(`{"path":"a.txt","start_line":1,"end_line":2}`)
	runReadCacheCall(t, execCtx, call, tool, mcp, false)
	if err := os.WriteFile(filepath.Join(execCtx.WorkspaceRoot, "unrelated"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	call.ID = "second"
	call.Arguments = []byte(`{ "end_line": 2, "start_line": 1, "path": "a.txt" }`)
	runReadCacheCall(t, execCtx, call, tool, mcp, true)
}

func TestOrdinaryReadCacheSymlinkRetarget(t *testing.T) {
	execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, "read_file")
	link := filepath.Join(execCtx.WorkspaceRoot, "link.txt")
	if err := os.Symlink("a.txt", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	call.Arguments = []byte(`{"path":"link.txt"}`)
	runReadCacheCall(t, execCtx, call, tool, mcp, false)
	runReadCacheCall(t, execCtx, call, tool, mcp, true)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	// The cache must defer to the tool's boundary validation.
	mcp.result = schema.ToolResult{Content: "outside workspace", IsError: true}
	runReadCacheCall(t, execCtx, call, tool, mcp, false)
}

func replayOrdinaryAgentReadForTest(execCtx ExecutionContext, call schema.ToolCall, tool schema.Tool) (schema.ToolResult, bool) {
	key, fingerprint := ordinaryAgentReadCacheState(execCtx, call, tool)
	return replayOrdinaryAgentRead(execCtx, call, tool, key, fingerprint)
}

func TestOrdinaryReadCacheRejectsUnsuccessfulResults(t *testing.T) {
	for _, result := range []schema.ToolResult{
		{Content: "failed", IsError: true}, {Content: "pending", Suspended: true}, {Content: "denied", Denied: true},
	} {
		t.Run(result.Content, func(t *testing.T) {
			execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, "read_file")
			mcp.result = result
			runReadCacheCall(t, execCtx, call, tool, mcp, false)
			call.ID = "second"
			runReadCacheCall(t, execCtx, call, tool, mcp, false)
			if len(execCtx.readResultCache.results) != 0 {
				t.Fatal("unsuccessful result retained")
			}
		})
	}
}

func TestOrdinaryReadCacheDoesNotPublishAcrossConcurrentEdit(t *testing.T) {
	execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, "read_file")
	mcp.onCall = func() {
		if err := os.WriteFile(filepath.Join(execCtx.WorkspaceRoot, "a.txt"), []byte("other"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runReadCacheCall(t, execCtx, call, tool, mcp, false)
	mcp.onCall = nil
	mcp.result.Content = "other"
	call.ID = "second"
	runReadCacheCall(t, execCtx, call, tool, mcp, false)
	call.ID = "third"
	runReadCacheCall(t, execCtx, call, tool, mcp, true)
}

func TestOrdinaryReadCacheExplicitRefreshAlwaysExecutes(t *testing.T) {
	for _, arguments := range []string{`{"path":"a.txt","refresh":true}`, `{"path":"a.txt","reuse_if_unchanged":false}`} {
		t.Run(arguments, func(t *testing.T) {
			execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, "read_file")
			runReadCacheCall(t, execCtx, call, tool, mcp, false)
			call.Arguments = []byte(arguments)
			for range 2 {
				runReadCacheCall(t, execCtx, call, tool, mcp, false)
			}
			if mcp.calls != 3 {
				t.Fatalf("calls=%d", mcp.calls)
			}
		})
	}
}

func TestOrdinaryReadCacheNeverReplaysLiveTools(t *testing.T) {
	for _, name := range []string{"command_tools/start_process", "command_tools/process_status", "command_tools/stop_process", "network_tools/http_get", "web_tools/http_get", "web_tools/browser_snapshot", "ssh_tools/read_file", "file_tools/search_files", "file_tools/list_tree", "file_tools/repository_map"} {
		t.Run(name, func(t *testing.T) {
			execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, "read_file")
			tool.Name = name
			tool.Server, _, _ = strings.Cut(name, "/")
			if name == "command_tools/start_process" || name == "command_tools/stop_process" {
				tool.Kind = "exec"
			}
			call.Name = name
			for range 2 {
				runReadCacheCall(t, execCtx, call, tool, mcp, false)
			}
			if len(execCtx.readResultCache.results) != 0 {
				t.Fatal("live result retained")
			}
		})
	}
}

func TestWorkflowReadCacheDoesNotAliasExternalFileTools(t *testing.T) {
	for _, server := range []string{"web_tools", "network_tools", "ssh_tools", "external"} {
		t.Run(server, func(t *testing.T) {
			execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, "read_file")
			execCtx.Session = session.New(4)
			execCtx.WorkflowRunID = execCtx.Session.StartWorkflowRun("inspect", "live read")
			execCtx.WorkflowStage = "read"
			execCtx.IdempotentToolCalls = true
			tool.Server = server
			tool.Name = server + "/read_file"
			call.Name = tool.Name
			for range 2 {
				runReadCacheCall(t, execCtx, call, tool, mcp, false)
			}
			if mcp.calls != 2 {
				t.Fatalf("live read calls=%d", mcp.calls)
			}
		})
	}
}

func TestOrdinaryReadCacheBoundedAndConcurrent(t *testing.T) {
	execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, "read_file_range")
	for i := range ordinaryAgentReadCacheMaxEntries + 3 {
		call.Arguments = []byte(fmt.Sprintf(`{"path":"a.txt","start_line":%d}`, i+1))
		runReadCacheCall(t, execCtx, call, tool, mcp, false)
		if len(execCtx.readResultCache.results) > ordinaryAgentReadCacheMaxEntries {
			t.Fatal("unbounded cache")
		}
	}
	call.Arguments = []byte(`{"path":"a.txt","start_line":1000}`)
	mcp.result.Content = strings.Repeat("x", ordinaryAgentReadCacheMaxResultBytes+1)
	for range 2 {
		runReadCacheCall(t, execCtx, call, tool, mcp, false)
	}
	call.Arguments = []byte(`{"path":"a.txt","start_line":1}`)
	mcp.result.Content = "small"
	runReadCacheCall(t, execCtx, call, tool, mcp, false)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				if _, ok := replayOrdinaryAgentReadForTest(execCtx, call, tool); !ok {
					t.Error("missing cached result")
				}
			}
		})
	}
	wg.Wait()
}

func TestOrdinaryReadCacheInvalidPathsAndLargeFiles(t *testing.T) {
	for _, path := range []string{"missing", "..", "a.txt"} {
		t.Run(path, func(t *testing.T) {
			execCtx, call, tool, mcp := ordinaryReadCacheFixture(t, "read_file")
			if path == "a.txt" {
				if err := os.WriteFile(filepath.Join(execCtx.WorkspaceRoot, path), make([]byte, ordinaryAgentReadCacheMaxFileBytes+1), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			call.Arguments = []byte(fmt.Sprintf(`{"path":%q}`, path))
			for range 2 {
				runReadCacheCall(t, execCtx, call, tool, mcp, false)
			}
			if len(execCtx.readResultCache.results) != 0 {
				t.Fatal("unsafe read cached")
			}
		})
	}
}

func TestOrdinaryReadCacheValidationEvidenceAndRefresh(t *testing.T) {
	for _, outcome := range []string{"success", "exit-failure", "timeout", "suspended", "missing-evidence", "external-edit", "refresh"} {
		t.Run(outcome, func(t *testing.T) {
			execCtx, _, _, mcp := ordinaryReadCacheFixture(t, "read_file")
			tool := reusableValidationTestTool()
			call := reusableValidationCall("first")
			mcp.result = successfulValidationResult(t, execCtx.WorkspaceRoot)
			switch outcome {
			case "exit-failure":
				mcp.result.Content = strings.Replace(mcp.result.Content, `"exit_code":0`, `"exit_code":1`, 1)
			case "timeout":
				mcp.result.Content = strings.Replace(mcp.result.Content, `"exit_code":0`, `"exit_code":0,"timed_out":true`, 1)
			case "suspended":
				mcp.result.Suspended = true
			case "missing-evidence":
				mcp.result.Content = `{"exit_code":0,"workspace_changed":false}`
			}
			runReadCacheCall(t, execCtx, call, tool, mcp, false)
			call.ID = "second"
			if outcome == "external-edit" {
				if err := os.WriteFile(filepath.Join(execCtx.WorkspaceRoot, "a.txt"), []byte("other"), 0o600); err != nil {
					t.Fatal(err)
				}
				mcp.result = successfulValidationResult(t, execCtx.WorkspaceRoot)
			}
			if outcome == "refresh" {
				call.Arguments = []byte(`{"program":"go","args":["test","./..."],"reuse_if_unchanged":false}`)
			}
			runReadCacheCall(t, execCtx, call, tool, mcp, outcome == "success")
		})
	}
}

func BenchmarkOrdinaryAgentReadCache(b *testing.B) {
	root := b.TempDir()
	for i := range 128 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%03d.txt", i)), []byte(strings.Repeat("content\n", 128)), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	tool := schema.Tool{Name: "file_tools/read_file", Server: "file_tools", Kind: "read"}
	call := schema.ToolCall{ID: "read", Name: tool.Name, Arguments: []byte(`{"path":"file-000.txt"}`)}
	for _, cached := range []bool{true, false} {
		name := "fresh-local-read"
		if cached {
			name = "duplicate-read"
		}
		b.Run(name, func(b *testing.B) {
			execCtx := ExecutionContext{AgentRunID: "benchmark", WorkspaceRoot: root}
			if cached {
				execCtx.readResultCache = newOrdinaryAgentReadCache()
			}
			mcp := &idempotencyMCP{}
			mcp.onCall = func() {
				data, err := os.ReadFile(filepath.Join(root, "file-000.txt"))
				if err != nil {
					b.Fatal(err)
				}
				content, err := json.Marshal(map[string]string{"content": string(data)})
				if err != nil {
					b.Fatal(err)
				}
				mcp.result = schema.ToolResult{Content: string(content)}
			}
			if _, _, err := executeToolCallWithIdempotency(context.Background(), mcp, execCtx, call, tool); err != nil {
				b.Fatal(err)
			}
			mcp.calls = 0
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, _, err := executeToolCallWithIdempotency(context.Background(), mcp, execCtx, call, tool); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(mcp.calls)/float64(b.N), "MCP-calls/op")
		})
	}
	b.Run("workspace-fingerprint", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := executionfingerprint.Workspace(root); err != nil {
				b.Fatal(err)
			}
		}
	})
}
