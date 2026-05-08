package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestExpandAtFileReferencesInjectsWorkspaceFileContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("important context\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	expanded, refs, err := expandAtFileReferences("summarize @notes.txt", root)
	if err != nil {
		t.Fatalf("expand refs: %v", err)
	}
	if len(refs) != 1 || refs[0].RelativePath != "notes.txt" {
		t.Fatalf("expected one notes reference, got %#v", refs)
	}
	if !strings.Contains(expanded, "Referenced workspace files:") || !strings.Contains(expanded, "important context") {
		t.Fatalf("expected referenced file content in prompt, got %q", expanded)
	}
}

func TestExpandAtFileReferencesRejectsWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	_, _, err := expandAtFileReferences("read @../outside.txt", root)
	if err == nil || !strings.Contains(err.Error(), "escapes workspace root") {
		t.Fatalf("expected workspace escape rejection, got %v", err)
	}
}

func TestExpandAtFileReferencesRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	link := filepath.Join(root, "outside-link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, _, err := expandAtFileReferences("read @outside-link.txt", root)
	if err == nil || !strings.Contains(err.Error(), "resolve real path") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func TestParseAtReferencePathsSupportsQuotedPaths(t *testing.T) {
	paths := parseAtReferencePaths(`compare @"dir/my file.txt" and @README.md.`)
	if len(paths) != 2 || paths[0] != "dir/my file.txt" || paths[1] != "README.md" {
		t.Fatalf("unexpected parsed paths: %#v", paths)
	}
}

func TestExpandAtFileReferencesWithToolsEmitsReadToolLogs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("tool context\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	renderer := newCLIStreamRenderer(false)
	mcp := &atRefMCP{payload: map[string]any{"path": path, "size": 13, "content": "tool context\n"}}
	var expanded string
	output := captureStdout(t, func() {
		var err error
		expanded, _, err = expandAtFileReferencesWithTools(context.Background(), "summarize @notes.txt", root, mcp, renderer)
		if err != nil {
			t.Fatalf("expand refs with tools: %v", err)
		}
	})
	if !strings.Contains(output, "calling read_file") || !strings.Contains(output, "read_file") || !strings.Contains(output, "done") {
		t.Fatalf("expected read_file tool logs, got %q", output)
	}
	if !strings.Contains(expanded, "tool context") {
		t.Fatalf("expected referenced content in expanded input, got %q", expanded)
	}
	if mcp.calledName != "file_tools/read_file" {
		t.Fatalf("expected qualified read_file call, got %q", mcp.calledName)
	}
}

func TestFormatAtReferenceSuggestionsShowsMatches(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	output, handled, err := formatAtReferenceSuggestions("@src", root)
	if err != nil {
		t.Fatalf("suggest refs: %v", err)
	}
	if !handled || !strings.Contains(output, "@src/") || !strings.Contains(output, "@src/main.go") {
		t.Fatalf("expected @src suggestions, handled=%t output=%q", handled, output)
	}
}

func TestFormatAtReferenceSuggestionsShowsRootMatchesForAtOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	output, handled, err := formatAtReferenceSuggestions("@", root)
	if err != nil {
		t.Fatalf("suggest refs: %v", err)
	}
	if !handled || !strings.Contains(output, "@README.md") {
		t.Fatalf("expected @ root suggestions, handled=%t output=%q", handled, output)
	}
}

func TestFormatAtReferenceSuggestionsSkipsLowValueDirectoriesBeforeVisitLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".venv", "Lib", "site-packages"), 0o755); err != nil {
		t.Fatalf("mkdir venv: %v", err)
	}
	for i := 0; i < 3050; i++ {
		if err := os.WriteFile(filepath.Join(root, ".venv", "Lib", "site-packages", fmt.Sprintf("ignored_%04d.py", i)), []byte("ignored"), 0o644); err != nil {
			t.Fatalf("write ignored file: %v", err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "pkg", "index.js"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write ignored node module: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "app", "models"), 0o755); err != nil {
		t.Fatalf("mkdir app models: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "models", "user.py"), []byte("class User: pass\n"), 0o644); err != nil {
		t.Fatalf("write user model: %v", err)
	}

	output, handled, err := formatAtReferenceSuggestions("@app", root)
	if err != nil {
		t.Fatalf("suggest refs: %v", err)
	}
	if !handled || !strings.Contains(output, "@app/") || !strings.Contains(output, "@app/models/user.py") {
		t.Fatalf("expected app suggestions despite ignored dependency trees, handled=%t output=%q", handled, output)
	}
	if strings.Contains(output, ".venv") || strings.Contains(output, "node_modules") {
		t.Fatalf("expected ignored directories to stay out of suggestions, got %q", output)
	}
}

func TestTrailingAtReferenceTokenFindsInlinePrefix(t *testing.T) {
	start, prefix, ok := trailingAtReferenceToken([]rune("please inspect @src/ma"))
	if !ok || prefix != "src/ma" || start != len([]rune("please inspect ")) {
		t.Fatalf("unexpected token: start=%d prefix=%q ok=%t", start, prefix, ok)
	}
}

func TestCompleteAtReferenceTokenCompletesUniqueMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	next, changed, message := completeAtReferenceToken([]rune("read @src/ma"), root)
	if !changed || message != "" || string(next) != "read @src/main.go" {
		t.Fatalf("expected unique completion, changed=%t message=%q next=%q", changed, message, string(next))
	}
}

func TestCompleteAtReferenceTokenUsesFuzzyMatchWhenPrefixMisses(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	next, changed, message := completeAtReferenceToken([]rune("read @smg"), root)
	if !changed || message != "" || string(next) != "read @src/main.go" {
		t.Fatalf("expected fuzzy file completion, changed=%t message=%q next=%q", changed, message, string(next))
	}
}

func TestFormatAtReferenceSuggestionsUsesFuzzyMatches(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	output, handled, err := formatAtReferenceSuggestions("@smg", root)
	if err != nil {
		t.Fatalf("suggest refs: %v", err)
	}
	if !handled || !strings.Contains(output, "@src/main.go") {
		t.Fatalf("expected fuzzy @ suggestions, handled=%t output=%q", handled, output)
	}
}

type atRefMCP struct {
	payload    map[string]any
	calledName string
}

func (m *atRefMCP) ListTools(context.Context) ([]schema.Tool, error)    { return nil, nil }
func (m *atRefMCP) RefreshTools(context.Context) ([]schema.Tool, error) { return nil, nil }
func (m *atRefMCP) HealthStatus(context.Context) map[string]string {
	return map[string]string{"stub": "ready"}
}
func (m *atRefMCP) ToolNames() []string { return []string{"file_tools/read_file"} }
func (m *atRefMCP) CallTool(_ context.Context, name string, _ []byte) (schema.ToolResult, error) {
	m.calledName = name
	encoded, _ := json.Marshal(m.payload)
	return schema.ToolResult{ToolName: "read_file", Content: string(encoded)}, nil
}
