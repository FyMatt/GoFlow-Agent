package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBatchFixture(t *testing.T) (string, string) {
	t.Helper()
	workspaceRoot = t.TempDir()
	first := filepath.Join(workspaceRoot, "a.txt")
	second := filepath.Join(workspaceRoot, "b.txt")
	if err := os.WriteFile(first, []byte("alpha one\n"), 0o644); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := os.WriteFile(second, []byte("beta one\n"), 0o644); err != nil {
		t.Fatalf("write second: %v", err)
	}
	return first, second
}

func TestCallToolApplyEditsPatchesSeveralFilesInOneCall(t *testing.T) {
	first, second := writeBatchFixture(t)
	params, _ := json.Marshal(map[string]any{
		"name": "apply_edits",
		"arguments": map[string]any{
			"files": []map[string]any{
				{"path": "a.txt", "expected_sha256": sha256Hex([]byte("alpha one\n")), "edits": []map[string]any{{"old_text": "alpha", "new_text": "ALPHA"}}},
				{"path": "b.txt", "expected_sha256": sha256Hex([]byte("beta one\n")), "edits": []map[string]any{{"old_text": "beta", "new_text": "BETA"}}},
			},
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected batch success, got %#v", result)
	}
	for path, want := range map[string]string{first: "ALPHA one\n", second: "BETA one\n"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if string(data) != want {
			t.Fatalf("unexpected content for %s: %q", path, data)
		}
	}
	if !strings.Contains(result["content"].(string), `"applied":2`) {
		t.Fatalf("expected applied count, got %s", result["content"])
	}
}

// A failure in any file must restore the files already written by this call:
// a half-applied multi-file edit is worse than a failed one.
func TestCallToolApplyEditsRollsBackOnAnyFailure(t *testing.T) {
	first, second := writeBatchFixture(t)
	params, _ := json.Marshal(map[string]any{
		"name": "apply_edits",
		"arguments": map[string]any{
			"files": []map[string]any{
				{"path": "a.txt", "expected_sha256": sha256Hex([]byte("alpha one\n")), "edits": []map[string]any{{"old_text": "alpha", "new_text": "ALPHA"}}},
				{"path": "b.txt", "expected_sha256": sha256Hex([]byte("stale\n")), "edits": []map[string]any{{"old_text": "absent", "new_text": "BETA"}}},
			},
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) {
		t.Fatalf("expected batch failure, got %#v", result)
	}
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	if string(data) != "alpha one\n" {
		t.Fatalf("first file must be restored after rollback, got %q", data)
	}
	secondData, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if string(secondData) != "beta one\n" {
		t.Fatalf("second file must stay untouched, got %q", secondData)
	}
}
