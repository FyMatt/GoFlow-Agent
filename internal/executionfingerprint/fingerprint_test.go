package executionfingerprint

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceFingerprintChangesWithContentAndIgnoresRuntimeState(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.FileCount != 1 || first.Hash == "" {
		t.Fatalf("unexpected first snapshot: %#v", first)
	}
	if err := os.MkdirAll(filepath.Join(root, ".goflow", "index"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".goflow", "index", "repository.json"), []byte("runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if second.Hash != first.Hash || second.FileCount != first.FileCount {
		t.Fatalf("runtime state changed fingerprint: first=%#v second=%#v", first, second)
	}
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if third.Hash == first.Hash || third.ByteCount == first.ByteCount {
		t.Fatalf("content change was not observed: first=%#v third=%#v", first, third)
	}
}

func TestWorkspaceFingerprintSkipsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	snapshot, err := Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.FileCount != 0 {
		t.Fatalf("symlink target must not be fingerprinted: %#v", snapshot)
	}
}

func TestWorkspaceFingerprintRejectsMissingRoot(t *testing.T) {
	_, err := Workspace(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("missing workspace root must not produce an empty reusable fingerprint")
	}
}

func TestWorkspaceFingerprintHashesLargeFileContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	firstContent := bytes.Repeat([]byte("a"), 33<<20)
	secondContent := bytes.Repeat([]byte("b"), 33<<20)
	if err := os.WriteFile(path, firstContent, 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, secondContent, 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash == second.Hash {
		t.Fatal("same-size large file content change must invalidate the fingerprint")
	}
}
