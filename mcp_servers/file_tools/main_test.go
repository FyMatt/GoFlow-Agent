package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallToolReadFileReturnsStructuredContent(t *testing.T) {
	tmp := t.TempDir()
	workspaceRoot = tmp
	path := filepath.Join(tmp, "sample.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	params, _ := json.Marshal(map[string]any{
		"name":      "read_file",
		"arguments": map[string]any{"path": path},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	if !strings.Contains(result["content"].(string), "hello") {
		t.Fatalf("expected file content in result: %v", result["content"])
	}
}

func TestCallToolReadFileRejectsNonUTF8Content(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "binary.bin")
	if err := os.WriteFile(path, []byte{0xff, 0xfe, 0xfd}, 0o644); err != nil {
		t.Fatalf("write binary temp file: %v", err)
	}

	params, _ := json.Marshal(map[string]any{
		"name":      "read_file",
		"arguments": map[string]any{"path": path},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "valid UTF-8") {
		t.Fatalf("expected UTF-8 rejection, got %#v", result)
	}
}

func TestCallToolReadFileReportsMissingPathClearly(t *testing.T) {
	workspaceRoot = t.TempDir()
	missing := filepath.Join("app", "models", "post.py")
	params, _ := json.Marshal(map[string]any{
		"name":      "read_file",
		"arguments": map[string]any{"path": missing},
	})
	result := callTool(params)
	if !result["is_error"].(bool) {
		t.Fatalf("expected missing path error, got %#v", result)
	}
	content := result["content"].(string)
	if !strings.Contains(content, "read_file path not found:") || !strings.Contains(content, missing) {
		t.Fatalf("expected friendly missing path message, got %q", content)
	}
	if strings.Contains(content, "GetFileAttributesEx") || strings.Contains(content, "The system cannot find the path specified") {
		t.Fatalf("expected raw Windows stat error to be hidden, got %q", content)
	}
}

func TestCallToolReadFileStripsUTF8BOM(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "bom.txt")
	if err := os.WriteFile(path, append([]byte{0xef, 0xbb, 0xbf}, []byte("hello\n")...), 0o644); err != nil {
		t.Fatalf("write bom temp file: %v", err)
	}

	params, _ := json.Marshal(map[string]any{
		"name":      "read_file",
		"arguments": map[string]any{"path": path},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode read payload: %v", err)
	}
	if got := payload["content"].(string); got != "hello\n" {
		t.Fatalf("expected BOM stripped UTF-8 content, got %q", got)
	}
}

func TestExtractPathRequiresPath(t *testing.T) {
	workspaceRoot = t.TempDir()
	_, err := extractPath(map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error when path is missing")
	}
}

func TestResolveWorkspacePathRejectsEscape(t *testing.T) {
	root := t.TempDir()
	_, err := resolveWorkspacePath("../outside.txt", root)
	if err == nil {
		t.Fatal("expected escape to be rejected")
	}
}

func TestResolveWorkspacePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := resolveWorkspacePath(filepath.Join("outside-link", "file.txt"), root)
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func TestResolveWorkspacePathAllowsRelativePathInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	resolved, err := resolveWorkspacePath(filepath.Join("nested", "file.txt"), root)
	if err != nil {
		t.Fatalf("resolve path: %v", err)
	}
	if !strings.HasPrefix(resolved, root) {
		t.Fatalf("expected resolved path to stay under root, got %s", resolved)
	}
}

func TestCallToolWriteFileRejectsNonStringContent(t *testing.T) {
	workspaceRoot = t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"name":      "write_file",
		"arguments": map[string]any{"path": "sample.txt", "content": 123},
	})
	result := callTool(params)
	if !result["is_error"].(bool) {
		t.Fatal("expected error for non-string content")
	}
}

func TestCallToolWriteFileReturnsLineChangeSummary(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "sample.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "write_file",
		"arguments": map[string]any{
			"path":    "sample.txt",
			"content": "one\nTWO\nthree\n",
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode write payload: %v", err)
	}
	if payload["status"] != "modified" {
		t.Fatalf("expected modified status, got %#v", payload)
	}
	if payload["old_range"] != "2-2" || payload["new_range"] != "2-2" {
		t.Fatalf("expected precise old/new line ranges, got %#v", payload)
	}
	if payload["added_lines"].(float64) != 1 || payload["deleted_lines"].(float64) != 1 {
		t.Fatalf("expected +1/-1 line counts, got %#v", payload)
	}
	if !strings.Contains(result["content"].(string), `"diff_preview"`) || !strings.Contains(result["content"].(string), "-    2") || !strings.Contains(result["content"].(string), "+         2") {
		t.Fatalf("expected diff preview in result, got %v", result["content"])
	}
}

func TestCallToolWriteFileReturnsCreatedFileSummary(t *testing.T) {
	workspaceRoot = t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"name": "write_file",
		"arguments": map[string]any{
			"path":    "new.txt",
			"content": "one\ntwo\n",
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode write payload: %v", err)
	}
	if payload["status"] != "created" || payload["new_range"] != "1-2" {
		t.Fatalf("expected created file line summary, got %#v", payload)
	}
	if payload["added_lines"].(float64) != 2 || payload["deleted_lines"].(float64) != 0 {
		t.Fatalf("expected +2/-0 line counts, got %#v", payload)
	}
	if !strings.Contains(payload["diff_preview"].(string), "+         1 | one") {
		t.Fatalf("expected line-numbered created diff, got %#v", payload["diff_preview"])
	}
}

func TestCallToolWriteFileRejectsSymlinkParentEscape(t *testing.T) {
	workspaceRoot = t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspaceRoot, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "write_file",
		"arguments": map[string]any{
			"path":    filepath.Join("outside-link", "owned.txt"),
			"content": "should not escape\n",
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "outside workspace") {
		t.Fatalf("expected symlink parent escape rejection, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(outside, "owned.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected outside file not to be written, stat err=%v", err)
	}
}

func TestCallToolDeleteFileRemovesWorkspaceFile(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "delete-me.txt")
	if err := os.WriteFile(path, []byte("bye\n"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name":      "delete_file",
		"arguments": map[string]any{"path": "delete-me.txt"},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, stat err=%v", err)
	}
	if !strings.Contains(result["content"].(string), "deleted_file") {
		t.Fatalf("expected delete status payload, got %v", result["content"])
	}
}

func TestCallToolDeleteFileRefusesWorkspaceRoot(t *testing.T) {
	workspaceRoot = t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"name":      "delete_file",
		"arguments": map[string]any{"path": "."},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "refusing to delete workspace root") {
		t.Fatalf("expected workspace root delete rejection, got %#v", result)
	}
	if _, err := os.Stat(workspaceRoot); err != nil {
		t.Fatalf("expected workspace root to remain, stat err=%v", err)
	}
}

func TestCallToolDeleteDirectoryRequiresRecursive(t *testing.T) {
	workspaceRoot = t.TempDir()
	dir := filepath.Join(workspaceRoot, "nested")
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name":      "delete_file",
		"arguments": map[string]any{"path": "nested"},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "recursive=true") {
		t.Fatalf("expected non-recursive directory delete rejection, got %#v", result)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("expected directory to remain after rejected delete, stat err=%v", err)
	}

	params, _ = json.Marshal(map[string]any{
		"name":      "delete_file",
		"arguments": map[string]any{"path": "nested", "recursive": true},
	})
	result = callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected recursive directory delete success, got %#v", result)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected directory to be deleted, stat err=%v", err)
	}
	if !strings.Contains(result["content"].(string), "deleted_directory") {
		t.Fatalf("expected deleted_directory payload, got %#v", result)
	}
}

func TestCallToolSearchFilesFindsNameAndContent(t *testing.T) {
	workspaceRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "alpha.txt"), []byte("hello needle\n"), 0o644); err != nil {
		t.Fatalf("write alpha: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "needle-name.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write name match: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "search_files",
		"arguments": map[string]any{
			"path":            ".",
			"query":           "needle",
			"max_results":     10,
			"include_content": true,
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	content := result["content"].(string)
	if !strings.Contains(content, `"match_type":"content"`) || !strings.Contains(content, `"match_type":"name"`) {
		t.Fatalf("expected name and content matches, got %s", content)
	}
}

func TestCallToolSearchFilesSkipsSymlinkEscapes(t *testing.T) {
	workspaceRoot = t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("needle secret\n"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(workspaceRoot, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "search_files",
		"arguments": map[string]any{
			"path":            ".",
			"query":           "needle",
			"max_results":     10,
			"include_content": true,
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	if strings.Contains(result["content"].(string), "secret") {
		t.Fatalf("expected symlink target outside workspace to be skipped, got %s", result["content"])
	}
}

func TestCallToolListTreeSkipsSymlinkEscapes(t *testing.T) {
	workspaceRoot = t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "outside.txt"), []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(workspaceRoot, "outside-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name":      "list_tree",
		"arguments": map[string]any{"path": ".", "max_depth": 3, "max_entries": 20},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	content := result["content"].(string)
	if strings.Contains(content, "outside.txt") || strings.Contains(content, "outside-link") {
		t.Fatalf("expected symlink escape to be skipped, got %s", content)
	}
}

func TestCallToolListTreeReturnsBoundedEntries(t *testing.T) {
	workspaceRoot = t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "dir", "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "dir", "nested", "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name":      "list_tree",
		"arguments": map[string]any{"path": ".", "max_depth": 1, "max_entries": 10},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	content := result["content"].(string)
	if !strings.Contains(content, `"relative_path":"dir"`) || strings.Contains(content, `"relative_path":"dir/nested/file.txt"`) {
		t.Fatalf("expected max_depth=1 to include dir but not nested file, got %s", content)
	}
}

func TestCallToolListDirHidesGoFlowRuntimeDirectoryAtWorkspaceRoot(t *testing.T) {
	workspaceRoot = t.TempDir()
	if err := os.Mkdir(filepath.Join(workspaceRoot, ".goflow"), 0o755); err != nil {
		t.Fatalf("mkdir .goflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "calculator.py"), []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatalf("write calculator: %v", err)
	}

	params, _ := json.Marshal(map[string]any{
		"name":      "list_dir",
		"arguments": map[string]any{"path": "."},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	content := result["content"].(string)
	if strings.Contains(content, ".goflow") {
		t.Fatalf("expected root listing to hide .goflow, got %s", content)
	}
	if !strings.Contains(content, "calculator.py") {
		t.Fatalf("expected project file to remain visible, got %s", content)
	}
}

func TestCallToolListDirAllowsExplicitGoFlowDirectoryListing(t *testing.T) {
	workspaceRoot = t.TempDir()
	goflowDir := filepath.Join(workspaceRoot, ".goflow")
	if err := os.Mkdir(goflowDir, 0o755); err != nil {
		t.Fatalf("mkdir .goflow: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goflowDir, "session.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	params, _ := json.Marshal(map[string]any{
		"name":      "list_dir",
		"arguments": map[string]any{"path": ".goflow"},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	if !strings.Contains(result["content"].(string), "session.json") {
		t.Fatalf("expected explicit .goflow listing to remain available, got %v", result["content"])
	}
}
