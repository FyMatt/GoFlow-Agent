package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunScriptExecutesDeclaredSkillScript(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root)
	oldSkillsDir := skillsDir
	skillsDir = root
	t.Cleanup(func() { skillsDir = oldSkillsDir })

	result := callTool(context.Background(), json.RawMessage(`{
		"name": "run_script",
		"arguments": {
			"skill": "echo-skill",
			"script": "echo-json",
			"args": {"message": "hello"}
		}
	}`))
	if isError, _ := result["is_error"].(bool); isError {
		t.Fatalf("expected script success, got %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["skill"] != "echo-skill" || payload["script"] != "echo-json" || payload["output_json_valid"] != true {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	stdout, _ := payload["stdout"].(string)
	if !strings.Contains(stdout, `"message":"hello"`) || !strings.Contains(stdout, `"skill":"echo-skill"`) {
		t.Fatalf("unexpected stdout: %s", stdout)
	}
}

func TestRunScriptRejectsArgumentsOutsideDeclaredSchema(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root)
	oldSkillsDir := skillsDir
	skillsDir = root
	t.Cleanup(func() { skillsDir = oldSkillsDir })

	result := callTool(context.Background(), json.RawMessage(`{
		"name": "run_script",
		"arguments": {
			"skill": "echo-skill",
			"script": "echo-json",
			"args": {"message": "hello", "extra": true}
		}
	}`))
	if isError, _ := result["is_error"].(bool); !isError {
		t.Fatalf("expected schema failure, got %#v", result)
	}
	if !strings.Contains(result["content"].(string), "extra") {
		t.Fatalf("expected schema field error, got %s", result["content"])
	}
}

func TestLoadDeclaredScriptRejectsPathLikeSkillAndScriptNames(t *testing.T) {
	root := t.TempDir()
	writeTestSkill(t, root)
	oldSkillsDir := skillsDir
	skillsDir = root
	t.Cleanup(func() { skillsDir = oldSkillsDir })

	if _, err := loadDeclaredScript("../echo-skill", "echo-json"); err == nil {
		t.Fatal("expected path-like skill name to be rejected")
	}
	if _, err := loadDeclaredScript("echo-skill", "scripts/echo.go"); err == nil {
		t.Fatal("expected path-like script name to be rejected")
	}
}

func writeTestSkill(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "echo-skill")
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	skillDoc := `---
name: echo-skill
description: Echo JSON input for tests.
version: 1.0.0
author: GoFlow
activation:
  keywords: ["echo"]
scripts:
  - name: echo-json
    description: Echo the provided message as JSON.
    path: scripts/echo.go
    runtime: go
    output: json
    timeout: 30s
    workspace_mount: none
    approval: required
    args_schema:
      type: object
      additionalProperties: false
      properties:
        message:
          type: string
      required: [message]
---

Run the declared helper through skill_runner only.
`
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillDoc), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	script := `package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	var args map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&args); err != nil {
		panic(err)
	}
	out, _ := json.Marshal(map[string]any{
		"message": args["message"],
		"skill": os.Getenv("GOFLOW_SKILL_NAME"),
		"workspace": os.Getenv("GOFLOW_WORKSPACE_ROOT"),
	})
	fmt.Print(string(out))
}
`
	if err := os.WriteFile(filepath.Join(dir, "scripts", "echo.go"), []byte(script), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
}
