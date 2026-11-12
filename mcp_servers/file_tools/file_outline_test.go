package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallToolReadFileOutlineListsFunctionsAndTypes(t *testing.T) {
	workspaceRoot = t.TempDir()
	source := `package demo

// Config is a demo type.
type Config struct {
	Name string
}

func Load(data []byte) (Config, error) {
	return Config{}, nil
}

func (c *Config) Apply() error {
	return nil
}
`
	path := filepath.Join(workspaceRoot, "demo.go")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	params, _ := json.Marshal(map[string]any{"name": "read_file_outline", "arguments": map[string]any{"path": "demo.go"}})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected outline success, got %#v", result)
	}
	content := result["content"].(string)
	for _, expected := range []string{`"name":"Config"`, `"name":"Load"`, `"name":"Apply"`, `"total_lines":14`} {
		if !strings.Contains(content, expected) {
			t.Fatalf("outline missing %s: %s", expected, content)
		}
	}
}

func TestCallToolReadFileOutlineRejectsDirectories(t *testing.T) {
	workspaceRoot = t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	params, _ := json.Marshal(map[string]any{"name": "read_file_outline", "arguments": map[string]any{"path": "pkg"}})
	result := callTool(params)
	if !result["is_error"].(bool) {
		t.Fatalf("expected directory rejection, got %#v", result)
	}
}
