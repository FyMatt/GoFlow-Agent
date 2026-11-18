package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBarePythonPrefersWorkspaceEnvironment(t *testing.T) {
	root := t.TempDir()
	rel := filepath.Join(".venv", "bin", "python")
	if runtime.GOOS == "windows" {
		rel = filepath.Join(".venv", "Scripts", "python.exe")
	}
	candidate := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(candidate), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte("test interpreter"), 0o755); err != nil {
		t.Fatal(err)
	}
	program, executable, scope, err := resolveCommandProgram("python", root, root)
	if err != nil || program != filepath.ToSlash(rel) || executable != candidate || scope != "workspace" {
		t.Fatalf("project environment not selected: %q %q %q %v", program, executable, scope, err)
	}
	if err := os.Mkdir(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, executable, _, err = resolveCommandProgram("python3", filepath.Join(root, "tests"), root)
	if err != nil || executable != candidate {
		t.Fatalf("nested cwd lost project interpreter: %q %v", executable, err)
	}
}

func TestAutoPythonEnvironmentCannotEscapeWorkspace(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".venv")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	rel := filepath.Join("bin", "python")
	if runtime.GOOS == "windows" {
		rel = filepath.Join("Scripts", "python.exe")
	}
	if err := os.MkdirAll(filepath.Join(outside, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, rel), []byte("outside"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := resolveCommandProgram("python", root, root)
	if err == nil || !strings.Contains(err.Error(), "outside workspace") {
		t.Fatalf("escaped environment must be rejected: %v", err)
	}
}
