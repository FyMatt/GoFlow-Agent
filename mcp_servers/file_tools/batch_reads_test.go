package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBatchReadFixtures(t *testing.T) (string, string) {
	t.Helper()
	workspaceRoot = t.TempDir()
	first := filepath.Join(workspaceRoot, "a.txt")
	second := filepath.Join(workspaceRoot, "b.txt")
	if err := os.WriteFile(first, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := os.WriteFile(second, []byte("gamma\n"), 0o644); err != nil {
		t.Fatalf("write second: %v", err)
	}
	return first, second
}

func TestCallToolReadFilesReturnsSeveralFilesInOneCall(t *testing.T) {
	writeBatchReadFixtures(t)
	params, _ := json.Marshal(map[string]any{
		"name": "read_files",
		"arguments": map[string]any{
			"files": []map[string]any{{"path": "a.txt"}, {"path": "b.txt"}, {"path": "missing.txt"}},
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected batch read success, got %#v", result)
	}
	content := result["content"].(string)
	if !strings.Contains(content, "alpha") || !strings.Contains(content, "gamma") {
		t.Fatalf("expected both files in one result, got %s", content)
	}
	if !strings.Contains(content, `"count":3`) {
		t.Fatalf("expected three entries including the error one, got %s", content)
	}
	if !strings.Contains(content, "missing.txt") {
		t.Fatalf("expected a per-file error entry, got %s", content)
	}
}

func TestCallToolReadFilesRejectsEmptyList(t *testing.T) {
	workspaceRoot = t.TempDir()
	params, _ := json.Marshal(map[string]any{"name": "read_files", "arguments": map[string]any{"files": []map[string]any{}}})
	result := callTool(params)
	if !result["is_error"].(bool) {
		t.Fatalf("expected empty list rejection, got %#v", result)
	}
}
