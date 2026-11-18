package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteFileJournalRestoresModifiedFile(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "sample.txt")
	before := []byte("before\n")
	if err := os.WriteFile(path, before, 0o640); err != nil {
		t.Fatal(err)
	}
	result := callFileToolForJournal(t, "write_file", map[string]any{
		"path":            "sample.txt",
		"content":         "after\n",
		"expected_sha256": sha256Hex(before),
	})
	changeID := journalChangeID(t, result)

	restored := callFileToolForJournal(t, "restore_file_change", map[string]any{"change_id": changeID})
	if restored["status"] != "restored" || restored["recovery_scope"] != "file_only" {
		t.Fatalf("unexpected restore result: %#v", restored)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(before) {
		t.Fatalf("expected original content, data=%q err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("expected original permissions, info=%#v err=%v", info, err)
	}
}

func TestRestoreFileChangeRejectsConcurrentEdit(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "sample.txt")
	before := []byte("before\n")
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	result := callFileToolForJournal(t, "write_file", map[string]any{
		"path":            "sample.txt",
		"content":         "after\n",
		"expected_sha256": sha256Hex(before),
	})
	changeID := journalChangeID(t, result)
	if err := os.WriteFile(path, []byte("manual edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"name": "restore_file_change", "arguments": map[string]any{"change_id": changeID}})
	restoreResult := callTool(params)
	if !restoreResult["is_error"].(bool) || !strings.Contains(restoreResult["content"].(string), "restore conflict") {
		t.Fatalf("expected restore conflict, got %#v", restoreResult)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "manual edit\n" {
		t.Fatalf("restore conflict must preserve manual edit, got %q", data)
	}
}

func TestRestoreFileChangeRemovesAgentCreatedFile(t *testing.T) {
	workspaceRoot = t.TempDir()
	result := callFileToolForJournal(t, "write_file", map[string]any{"path": "created.txt", "content": "created\n"})
	changeID := journalChangeID(t, result)
	callFileToolForJournal(t, "restore_file_change", map[string]any{"change_id": changeID})
	if _, err := os.Stat(filepath.Join(workspaceRoot, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected created file to be removed, err=%v", err)
	}
}

func TestRestoreFileChangeRecoversDeletedFile(t *testing.T) {
	workspaceRoot = t.TempDir()
	path := filepath.Join(workspaceRoot, "deleted.txt")
	if err := os.WriteFile(path, []byte("recover me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := callFileToolForJournal(t, "delete_file", map[string]any{"path": "deleted.txt"})
	changeID := journalChangeID(t, result)
	callFileToolForJournal(t, "restore_file_change", map[string]any{"change_id": changeID})
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "recover me\n" {
		t.Fatalf("expected deleted file recovery, data=%q err=%v", data, err)
	}
}

func callFileToolForJournal(t *testing.T, name string, arguments map[string]any) map[string]any {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		t.Fatal(err)
	}
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("%s failed: %v", name, result["content"])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode %s result: %v", name, err)
	}
	return payload
}

func journalChangeID(t *testing.T, payload map[string]any) string {
	t.Helper()
	changeID, _ := payload["change_id"].(string)
	if !fileChangeIDPattern.MatchString(changeID) || payload["reversible"] != true {
		t.Fatalf("expected reversible file-change journal metadata, got %#v", payload)
	}
	return changeID
}
