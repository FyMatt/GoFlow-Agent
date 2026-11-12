package skill

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeForMatchPreservesReplacementSemantics(t *testing.T) {
	for _, input := range []string{"", "  Office / Research-Plan\\Docs  ", "研究 / 审计-测试", "İΣǅ", "line\tbreak\nnext", "_ / - \\"} {
		replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_", "\\", "_")
		want := strings.ToLower(replacer.Replace(strings.TrimSpace(input)))
		if got := normalizeForMatch(input); got != want {
			t.Errorf("normalize %q: got %q want %q", input, got, want)
		}
	}
}

func TestMatchReplacerConcurrentQueriesAndReload(t *testing.T) {
	root := t.TempDir()
	writeLayeredSkill(t, root, "operations", "Run operations procedures")
	writeLayeredSkill(t, root, "research", "Review research evidence")
	manager, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"operations", "research"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for range 30 {
				got, ok := manager.Match(name)
				if !ok || got.Name != name {
					t.Fatalf("query crossed domains: got=%v ok=%t", got, ok)
				}
			}
		})
	}
	// Reload must continue to read edits rather than retain normalized metadata.
	writeLayeredSkill(t, root, "operations", "Updated operations procedures")
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	got, ok := manager.Match("operations")
	if !ok || got.Description != "Updated operations procedures" || got.Path != filepath.Join(root, "operations", "SKILL.md") {
		t.Fatalf("reload returned stale skill: got=%v ok=%t", got, ok)
	}
}
