package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRepositoryInstructionsSupportsCodexAndClaudeFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Run focused tests.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex", "instructions.md"), []byte("Prefer small patches.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := LoadRepositoryInstructions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Path != "AGENTS.md" || items[1].Path != ".codex/instructions.md" {
		t.Fatalf("unexpected instruction files: %#v", items)
	}
	prompt := RepositoryInstructionPrompt(items)
	if !strings.Contains(prompt, "AGENTS.md") || !strings.Contains(prompt, "Prefer small patches.") {
		t.Fatalf("expected rendered repository instructions, got %q", prompt)
	}
}

func TestLoadRepositoryInstructionsRejectsOversizedFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(strings.Repeat("x", maxInstructionFileBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRepositoryInstructions(root); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

func TestLoadRepositoryInstructionsSkipsSymlinkFiles(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("do not load"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	items, err := LoadRepositoryInstructions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected symlink instruction to be ignored, got %#v", items)
	}
}
