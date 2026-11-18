package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func TestCallToolProtectsSecretsAndRuntimeMetadataInLiveWorkspace(t *testing.T) {
	workspaceRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".env"), []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	readParams, _ := json.Marshal(map[string]any{
		"name":      "read_file",
		"arguments": map[string]any{"path": ".env"},
	})
	readResult := callTool(readParams)
	if !readResult["is_error"].(bool) || !strings.Contains(readResult["content"].(string), "protected") {
		t.Fatalf("expected secret read rejection, got %#v", readResult)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".goflow"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeParams, _ := json.Marshal(map[string]any{
		"name":      "write_file",
		"arguments": map[string]any{"path": ".goflow/owned.txt", "content": "no"},
	})
	writeResult := callTool(writeParams)
	if !writeResult["is_error"].(bool) || !strings.Contains(writeResult["content"].(string), "protected") {
		t.Fatalf("expected runtime metadata write rejection, got %#v", writeResult)
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

func TestCallToolReadFileSuggestsPathWithoutDuplicatedWorkspacePrefix(t *testing.T) {
	workspaceRoot = t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "cache"), 0o755); err != nil {
		t.Fatalf("create cache directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "cache", "cache.go"), []byte("package cache\n"), 0o644); err != nil {
		t.Fatalf("write cache file: %v", err)
	}
	duplicated := filepath.Join(filepath.Base(workspaceRoot), "cache", "cache.go")
	params, _ := json.Marshal(map[string]any{
		"name":      "read_file",
		"arguments": map[string]any{"path": duplicated},
	})
	result := callTool(params)
	if !result["is_error"].(bool) {
		t.Fatalf("expected duplicated workspace prefix error, got %#v", result)
	}
	content := result["content"].(string)
	if !strings.Contains(content, "repeats the workspace root") || !strings.Contains(content, "cache/cache.go") {
		t.Fatalf("expected actionable relative path suggestion, got %q", content)
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

func TestCallToolReadFileRangeReturnsExactContentAndWholeFileSHA(t *testing.T) {
	workspaceRoot = t.TempDir()
	content := "one\r\ntwo\r\nthree\r\nfour\r\n"
	path := filepath.Join(workspaceRoot, "sample.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "read_file_range",
		"arguments": map[string]any{
			"path":       "sample.txt",
			"start_line": 2,
			"end_line":   3,
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["content"] != "two\r\nthree\r\n" {
		t.Fatalf("expected exact CRLF range, got %q", payload["content"])
	}
	if payload["start_line"].(float64) != 2 || payload["end_line"].(float64) != 3 || payload["total_lines"].(float64) != 4 {
		t.Fatalf("unexpected range metadata: %#v", payload)
	}
	if payload["sha256"] != sha256Hex([]byte(content)) {
		t.Fatalf("expected whole-file SHA, got %#v", payload["sha256"])
	}
}

func TestCallToolReadFileRangeRejectsInvalidBounds(t *testing.T) {
	workspaceRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "sample.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name":      "read_file_range",
		"arguments": map[string]any{"path": "sample.txt", "start_line": 3},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "start_line") {
		t.Fatalf("expected range rejection, got %#v", result)
	}
}

func TestCallToolReadFileRangeClampsExplicitEndPastFile(t *testing.T) {
	workspaceRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "sample.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name":      "read_file_range",
		"arguments": map[string]any{"path": "sample.txt", "start_line": 1, "end_line": 3},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected range to be clamped, got %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode range payload: %v", err)
	}
	if payload["content"] != "one\ntwo\n" || payload["end_line"].(float64) != 2 {
		t.Fatalf("expected content through EOF, got %#v", payload)
	}
	if payload["requested_end_line"].(float64) != 3 || payload["range_truncated"] != true {
		t.Fatalf("expected explicit truncation metadata, got %#v", payload)
	}
}

func TestCallToolReadFileRangeSupportsLargeFilesWithBoundedOutput(t *testing.T) {
	workspaceRoot = t.TempDir()
	line := strings.Repeat("x", 100) + "\n"
	content := strings.Repeat(line, 12000)
	if len(content) <= maxReadBytes {
		t.Fatalf("test fixture must exceed normal read limit, got %d", len(content))
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "large.txt"), []byte(content), 0o644); err != nil {
		t.Fatalf("write large sample: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "read_file_range",
		"arguments": map[string]any{
			"path":       "large.txt",
			"start_line": 6000,
			"end_line":   6001,
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected bounded range success for large file, got %#v", result)
	}
	if !strings.Contains(result["content"].(string), `"total_lines":12000`) {
		t.Fatalf("expected whole-file metadata, got %s", result["content"])
	}
}

func TestCallToolApplyPatchUsesSHAAndWritesAtomically(t *testing.T) {
	workspaceRoot = t.TempDir()
	oldContent := "one\ntwo\nthree\n"
	path := filepath.Join(workspaceRoot, "sample.txt")
	if err := os.WriteFile(path, []byte(oldContent), 0o640); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "apply_patch",
		"arguments": map[string]any{
			"path":            "sample.txt",
			"expected_sha256": sha256Hex([]byte(oldContent)),
			"edits": []map[string]any{{
				"old_text": "two\n",
				"new_text": "TWO\n",
			}},
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected patch success, got %#v", result)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(data) != "one\nTWO\nthree\n" {
		t.Fatalf("unexpected patched content: %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat patched file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("expected mode preservation, got %o", info.Mode().Perm())
	}
	compact := result["content"].(string)
	if !strings.Contains(compact, `"added_lines":1`) || !strings.Contains(compact, `"new_sha256"`) || !strings.Contains(compact, `"change_id"`) {
		t.Fatalf("expected compact change metadata, got %s", compact)
	}
	if strings.Contains(compact, `"diff_preview"`) {
		t.Fatalf("patch result must not echo the diff; the change artifact already stores it: %s", compact)
	}
	leftovers, err := filepath.Glob(filepath.Join(workspaceRoot, ".goflow-write-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("expected no temporary files, leftovers=%v err=%v", leftovers, err)
	}
}

func TestCallToolApplyPatchAppliesWhenStaleHashStillMatches(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "sample.txt")
	if err := os.WriteFile(path, []byte("current\n"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "apply_patch",
		"arguments": map[string]any{
			"path":            "sample.txt",
			"expected_sha256": sha256Hex([]byte("stale\n")),
			"edits":           []map[string]any{{"old_text": "current", "new_text": "changed"}},
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("a stale hash must not fail when the pending edit still matches uniquely: %#v", result)
	}
	if !strings.Contains(result["content"].(string), `"sha256_relaxed":true`) {
		t.Fatalf("result must report the relaxed hash so the caller updates its state: %s", result["content"])
	}
	data, _ := os.ReadFile(path)
	if string(data) != "changed\n" {
		t.Fatalf("edit should be applied, got %q", data)
	}
}

func TestCallToolApplyPatchRejectsWhenEditNoLongerMatches(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "sample.txt")
	if err := os.WriteFile(path, []byte("current\n"), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "apply_patch",
		"arguments": map[string]any{
			"path":            "sample.txt",
			"expected_sha256": sha256Hex([]byte("stale\n")),
			"edits":           []map[string]any{{"old_text": "absent", "new_text": "changed"}},
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) {
		t.Fatalf("an edit that no longer matches must still fail, got %#v", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "current\n" {
		t.Fatalf("file must keep its content, got %q", data)
	}
}

func TestCallToolApplyPatchRejectsOccurrenceMismatchWithoutWriting(t *testing.T) {
	workspaceRoot = t.TempDir()
	content := "same\nsame\n"
	path := filepath.Join(workspaceRoot, "sample.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "apply_patch",
		"arguments": map[string]any{
			"path":            "sample.txt",
			"expected_sha256": sha256Hex([]byte(content)),
			"edits":           []map[string]any{{"old_text": "same", "new_text": "changed"}},
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "found 2") {
		t.Fatalf("expected occurrence rejection, got %#v", result)
	}
	var failure struct {
		RelativePath string `json:"relative_path"`
	}
	if err := json.Unmarshal([]byte(result["content"].(string)), &failure); err != nil || failure.RelativePath != "sample.txt" {
		t.Fatalf("rejected patch must retain its exact path for recovery tracking: %#v err=%v", result, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != content {
		t.Fatalf("expected mismatch to leave file unchanged, got %q", data)
	}
}

func TestCallToolApplyPatchPreservesUTF8BOM(t *testing.T) {
	workspaceRoot = t.TempDir()
	oldData := append([]byte{0xef, 0xbb, 0xbf}, []byte("hello\n")...)
	path := filepath.Join(workspaceRoot, "sample.txt")
	if err := os.WriteFile(path, oldData, 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	params, _ := json.Marshal(map[string]any{
		"name": "apply_patch",
		"arguments": map[string]any{
			"path":            "sample.txt",
			"expected_sha256": sha256Hex(oldData),
			"edits":           []map[string]any{{"old_text": "hello", "new_text": "world"}},
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected patch success, got %#v", result)
	}
	data, _ := os.ReadFile(path)
	if len(data) < 3 || data[0] != 0xef || data[1] != 0xbb || data[2] != 0xbf || string(data[3:]) != "world\n" {
		t.Fatalf("expected preserved BOM and patched content, got %v", data)
	}
}

func TestCallToolReplaceTextUsesSHAAndExactOccurrence(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "sample.txt")
	old := "one\ntwo\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"name": "replace_text", "arguments": map[string]any{
		"path": "sample.txt", "expected_sha256": sha256Hex([]byte(old)), "old_text": "two", "new_text": "TWO",
	}})
	result := callTool(params)
	if result["is_error"].(bool) || !strings.Contains(result["content"].(string), `"operation":"replace_text"`) {
		t.Fatalf("expected replace_text success, got %#v", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "one\nTWO\n" {
		t.Fatalf("unexpected replacement: %q", data)
	}
}

func TestCallToolReplaceTextRejectsAmbiguousMatchWithoutWriting(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "sample.txt")
	old := "same\nsame\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"name": "replace_text", "arguments": map[string]any{
		"path": "sample.txt", "expected_sha256": sha256Hex([]byte(old)), "old_text": "same", "new_text": "changed",
	}})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "found 2") {
		t.Fatalf("expected ambiguity rejection, got %#v", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != old {
		t.Fatalf("expected ambiguous replacement to leave file unchanged, got %q", data)
	}
}

func TestCallToolMovePathStaysInsideWorkspaceAndChecksSourceSHA(t *testing.T) {
	workspaceRoot = t.TempDir()
	source := filepath.Join(workspaceRoot, "source.txt")
	if err := os.WriteFile(source, []byte("move me"), 0o644); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"name": "move_path", "arguments": map[string]any{
		"source": "source.txt", "destination": "nested/destination.txt", "expected_sha256": sha256Hex([]byte("move me")),
	}})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "destination parent directory") {
		t.Fatalf("expected missing destination parent rejection, got %#v", result)
	}
	if err := os.Mkdir(filepath.Join(workspaceRoot, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	result = callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected move success, got %#v", result)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("expected source to move, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "nested", "destination.txt")); err != nil {
		t.Fatalf("expected destination file, err=%v", err)
	}
}

func TestCallToolMovePathRejectsOverwriteByDefault(t *testing.T) {
	workspaceRoot = t.TempDir()
	_ = os.WriteFile(filepath.Join(workspaceRoot, "source.txt"), []byte("source"), 0o644)
	_ = os.WriteFile(filepath.Join(workspaceRoot, "destination.txt"), []byte("keep"), 0o644)
	params, _ := json.Marshal(map[string]any{"name": "move_path", "arguments": map[string]any{
		"source": "source.txt", "destination": "destination.txt", "expected_sha256": sha256Hex([]byte("source")),
	}})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "destination already exists") {
		t.Fatalf("expected overwrite rejection, got %#v", result)
	}
}

func TestCallToolMkdirCreatesNestedDirectoryAndIsIdempotent(t *testing.T) {
	workspaceRoot = t.TempDir()
	params, _ := json.Marshal(map[string]any{"name": "mkdir", "arguments": map[string]any{"path": "a/b/c"}})
	result := callTool(params)
	if result["is_error"].(bool) || !strings.Contains(result["content"].(string), `"status":"created"`) {
		t.Fatalf("expected mkdir creation, got %#v", result)
	}
	result = callTool(params)
	if result["is_error"].(bool) || !strings.Contains(result["content"].(string), `"status":"already_exists"`) {
		t.Fatalf("expected idempotent mkdir, got %#v", result)
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
			"path":            "sample.txt",
			"content":         "one\nTWO\nthree\n",
			"expected_sha256": sha256Hex([]byte("one\ntwo\nthree\n")),
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
	if payload["index_invalidated"] != true {
		t.Fatalf("expected index invalidation metadata, got %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, ".goflow", "index", "dirty")); err != nil {
		t.Fatalf("expected index dirty marker: %v", err)
	}
	if !strings.Contains(result["content"].(string), `"diff_preview"`) || !strings.Contains(result["content"].(string), "-    2") || !strings.Contains(result["content"].(string), "+         2") {
		t.Fatalf("expected diff preview in result, got %v", result["content"])
	}
}

func TestMarkIndexInvalidatedRecordsChangedPathsAndFullDirectoryChanges(t *testing.T) {
	workspaceRoot = t.TempDir()
	payload := map[string]any{}
	markIndexInvalidated(payload, filepath.Join(workspaceRoot, "src", "main.go"))
	dirty := filepath.Join(workspaceRoot, ".goflow", "index", "dirty")
	data, err := os.ReadFile(dirty)
	if err != nil || string(data) != "src/main.go\n" {
		t.Fatalf("unexpected path invalidation marker %q err=%v", data, err)
	}

	payload = map[string]any{"is_dir": true}
	markIndexInvalidated(payload, filepath.Join(workspaceRoot, "src"))
	data, err = os.ReadFile(dirty)
	if err != nil || !strings.HasSuffix(string(data), "*\n") {
		t.Fatalf("expected directory change to request full rebuild, marker=%q err=%v", data, err)
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

func TestCallToolWriteFileCreatesWhenExpectedHashProvidedForMissingFile(t *testing.T) {
	workspaceRoot = t.TempDir()
	params, _ := json.Marshal(map[string]any{
		"name": "write_file",
		"arguments": map[string]any{
			"path":            "deliverables/security-report.md",
			"content":         "# report\n",
			"expected_sha256": strings.Repeat("a", 64),
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("missing file should be created even if expected_sha256 is present, got %#v", result)
	}
	data, err := os.ReadFile(filepath.Join(workspaceRoot, "deliverables", "security-report.md"))
	if err != nil {
		t.Fatalf("read created file: %v", err)
	}
	if string(data) != "# report\n" {
		t.Fatalf("unexpected content %q", data)
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
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["relative_path"] != "delete-me.txt" || payload["added_lines"] != float64(0) || payload["deleted_lines"] != float64(1) {
		t.Fatalf("deletion must report actual file and line delta: %#v", payload)
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

func TestCallToolSearchFilesDiscardsPartialMatchesFromInvalidUTF8(t *testing.T) {
	workspaceRoot = t.TempDir()
	data := append([]byte("needle before invalid data\n"), 0xff)
	if err := os.WriteFile(filepath.Join(workspaceRoot, "binary.dat"), data, 0o644); err != nil {
		t.Fatalf("write binary fixture: %v", err)
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
		t.Fatalf("expected binary file to be skipped, got error: %v", result["content"])
	}
	if strings.Contains(result["content"].(string), `"match_type":"content"`) {
		t.Fatalf("expected invalid UTF-8 content matches to be discarded, got %s", result["content"])
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

func TestCallToolListTreeSkipsGitAndVendorDirectories(t *testing.T) {
	workspaceRoot = t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".git", "HEAD"), []byte("ref"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "vendor", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "web", ".venv", "Lib", "site-packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "web", ".venv", "Lib", "site-packages", "dependency.py"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "交付证据"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "交付证据", "report.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "vendor", "pkg", "lib.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "internal", "cache.go"), []byte("package cache\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{
		"name":      "list_tree",
		"arguments": map[string]any{"path": ".", "max_depth": 4, "max_entries": 50},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected success, got error: %v", result["content"])
	}
	content := result["content"].(string)
	if strings.Contains(content, ".git/HEAD") || strings.Contains(content, "vendor/pkg") || strings.Contains(content, "site-packages") || strings.Contains(content, "交付证据") {
		t.Fatalf("list_tree must skip runtime, dependency, and evidence directories, got %s", content)
	}
	if !strings.Contains(content, "internal/cache.go") {
		t.Fatalf("list_tree should still list source files, got %s", content)
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
