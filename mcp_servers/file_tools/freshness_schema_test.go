package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestReadSchemasExposeSupportedCacheBypass(t *testing.T) {
	root := t.TempDir()
	previous := workspaceRoot
	workspaceRoot = root
	t.Cleanup(func() { workspaceRoot = previous })
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("current content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tool := range builtinTools() {
		if tool.Name != "read_file" && tool.Name != "read_file_range" {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
			AdditionalProperties bool `json:"additionalProperties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if schema.AdditionalProperties || schema.Properties["refresh"].Type != "boolean" || schema.Properties["reuse_if_unchanged"].Type != "boolean" {
			t.Fatalf("%s does not advertise typed freshness arguments", tool.Name)
		}
		params, _ := json.Marshal(map[string]any{"name": tool.Name, "arguments": map[string]any{"path": "note.txt", "refresh": true, "reuse_if_unchanged": false}})
		if result := callTool(params); result["is_error"] == true {
			t.Fatalf("%s rejected advertised freshness arguments: %#v", tool.Name, result)
		}
	}
}
