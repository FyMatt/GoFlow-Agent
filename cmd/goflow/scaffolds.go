package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	skillpkg "github.com/FyMatt/GoFlow-Agent/internal/skill"
)

func handleNewToolCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintln(os.Stderr, "usage: /new-tool python <name>")
		return true
	}
	language := strings.ToLower(strings.TrimSpace(fields[1]))
	if language != "python" {
		fmt.Fprintf(os.Stderr, "new tool error: unsupported language %q; currently supported: python\n", fields[1])
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[2:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	path := filepath.Join(root, "mcp_servers", name+".py")
	content := renderPythonMCPServerTemplate(name)
	if err := validatePythonMCPScaffold(content); err != nil {
		fmt.Fprintf(os.Stderr, "new tool validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("tool", fmt.Sprintf("created validated python MCP scaffold at %s", path)))
	fmt.Println(styleMuted("  next: add the server to configs/agent.yaml, then run /reload-tools and /tools"))
	return true
}

func handleNewAgentCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 2 {
		fmt.Fprintln(os.Stderr, "usage: /new-agent <name>")
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[1:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new agent error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new agent error: %v\n", err)
		return true
	}
	path := filepath.Join(root, "configs", "agents", name+".yaml")
	content := renderAgentConfigTemplate(name)
	if err := validateAgentScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new agent validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new agent error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("agent", fmt.Sprintf("created validated config snippet at %s", path)))
	fmt.Println(styleMuted("  next: copy or merge this snippet under agents: in configs/agent.yaml"))
	return true
}

func handleNewWorkflowCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 2 {
		fmt.Fprintln(os.Stderr, "usage: /new-workflow <name>")
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[1:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	dir := filepath.Join(root, "workflows", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	yamlPath := filepath.Join(dir, "workflow.yaml")
	docPath := filepath.Join(dir, "WORKFLOW.md")
	yamlContent := renderWorkflowYAMLTemplate(name)
	if err := validateWorkflowScaffold(name, yamlContent); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(yamlPath, yamlContent); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	if err := writeNewFile(docPath, renderWorkflowMarkdownTemplate(name)); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("workflow", fmt.Sprintf("created blueprint at %s", dir)))
	fmt.Println(styleMuted(fmt.Sprintf("  next: run /workflow %s <request> after adjusting stage skills and approvals", name)))
	return true
}

func validatePythonMCPScaffold(content string) error {
	required := []string{
		"GOFLOW_WORKSPACE_ROOT",
		"tools/list",
		"tools/call",
		"additionalProperties",
		"workspace_path",
		"write_text",
	}
	return requireScaffoldContent("python MCP scaffold", content, required)
}

func validateAgentScaffold(name, content string) error {
	required := []string{
		name + ":",
		"provider:",
		"mode:",
		"tool_policy:",
		"allowed_tool_kinds:",
		"max_iterations:",
	}
	return requireScaffoldContent("agent scaffold", content, required)
}

func validateWorkflowScaffold(name, content string) error {
	required := []string{
		"name: " + name,
		"stages:",
		"agent:",
		"skill:",
		"approval:",
	}
	return requireScaffoldContent("workflow scaffold", content, required)
}

func requireScaffoldContent(kind, content string, required []string) error {
	for _, needle := range required {
		if !strings.Contains(content, needle) {
			return fmt.Errorf("%s missing %q", kind, needle)
		}
	}
	return nil
}

func runtimeHomeFromSkillManager(skillManager *skillpkg.Manager) (string, error) {
	if skillManager == nil {
		return "", fmt.Errorf("skill manager is unavailable")
	}
	root := strings.TrimSpace(skillManager.Root())
	if root == "" {
		return "", fmt.Errorf("skill root is empty")
	}
	return filepath.Dir(root), nil
}

func writeNewFile(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("file already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func renderPythonMCPServerTemplate(name string) string {
	serverName := strings.ReplaceAll(name, "-", "_")
	return fmt.Sprintf(`#!/usr/bin/env python3
"""%s MCP server scaffold.

This server uses stdio JSON-RPC and keeps file paths inside GOFLOW_WORKSPACE_ROOT.
Add it to configs/agent.yaml under mcp_servers after editing tools.
"""
import json
import os
import sys
from pathlib import Path


SERVER_NAME = %q
WORKSPACE_ROOT = Path(os.environ.get("GOFLOW_WORKSPACE_ROOT") or os.environ.get("WORKSPACE_ROOT") or os.getcwd()).resolve()


def respond(request_id, result=None, error=None):
    payload = {"jsonrpc": "2.0", "id": request_id}
    if error is not None:
        payload["error"] = {"code": -32000, "message": str(error)}
    else:
        payload["result"] = result
    print(json.dumps(payload), flush=True)


def workspace_path(path):
    candidate = (WORKSPACE_ROOT / path).resolve()
    if candidate != WORKSPACE_ROOT and WORKSPACE_ROOT not in candidate.parents:
        raise ValueError("path escapes workspace")
    return candidate


def list_tools():
    return {
        "tools": [
            {
                "name": "ping",
                "description": "Return a health response from this MCP server.",
                "kind": "read",
                "input_schema": {
                    "type": "object",
                    "properties": {},
                    "additionalProperties": False,
                },
            },
            {
                "name": "read_text",
                "description": "Read a UTF-8 text file inside the workspace.",
                "kind": "read",
                "input_schema": {
                    "type": "object",
                    "properties": {"path": {"type": "string"}},
                    "required": ["path"],
                    "additionalProperties": False,
                },
            },
            {
                "name": "write_text",
                "description": "Write a UTF-8 text file inside the workspace.",
                "kind": "write",
                "input_schema": {
                    "type": "object",
                    "properties": {
                        "path": {"type": "string"},
                        "content": {"type": "string"},
                        "overwrite": {"type": "boolean"},
                    },
                    "required": ["path", "content"],
                    "additionalProperties": False,
                },
            },
        ]
    }


def call_tool(params):
    name = params.get("name", "")
    args = params.get("arguments") or {}
    if name == "ping":
        return {"content": json.dumps({"server": SERVER_NAME, "status": "ok"})}
    if name == "read_text":
        path = workspace_path(args.get("path", ""))
        return {"content": json.dumps({"path": str(path), "content": path.read_text(encoding="utf-8")})}
    if name == "write_text":
        path = workspace_path(args.get("path", ""))
        if path.exists() and not args.get("overwrite", False):
            raise ValueError("file exists; pass overwrite=true to replace it")
        path.parent.mkdir(parents=True, exist_ok=True)
        content = args.get("content", "")
        path.write_text(content, encoding="utf-8")
        return {"content": json.dumps({"path": str(path), "bytes_written": len(content.encode("utf-8"))})}
    raise ValueError(f"unknown tool: {name}")


def handle(request):
    method = request.get("method")
    params = request.get("params") or {}
    if method == "initialize":
        return {"server": SERVER_NAME, "capabilities": {"tools": True}}
    if method == "tools/list":
        return list_tools()
    if method == "tools/call":
        return call_tool(params)
    raise ValueError(f"unknown method: {method}")


def main():
    for line in sys.stdin:
        if not line.strip():
            continue
        try:
            request = json.loads(line)
            respond(request.get("id"), handle(request))
        except Exception as exc:
            respond(request.get("id") if "request" in locals() else None, error=exc)


if __name__ == "__main__":
    main()
`, serverName, serverName)
}

func renderAgentConfigTemplate(name string) string {
	title := strings.ReplaceAll(name, "-", " ")
	return fmt.Sprintf(`# Copy this block under agents: in configs/agent.yaml.
# Keep this profile narrow. Route write/exec work to a dedicated fixer-style agent.
%s:
  name: %s
  description: Describe this agent's role and permission boundary.
  provider: primary
  mode: chat
  tool_policy: confirm
  allowed_tool_kinds: [read]
  # Optional: restrict this agent to exact tools even within allowed kinds.
  # allowed_tools: [file_tools/list_tree, file_tools/read_file]
  # For a network researcher, prefer a narrow allowlist:
  # allowed_tool_kinds: [read, network]
  # allowed_tools: [web_tools/fetch_url, web_tools/web_search]
  max_iterations: 6
`, name, title)
}

func renderWorkflowYAMLTemplate(name string) string {
	return fmt.Sprintf(`name: %s
description: Describe the staged workflow goal.
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    approval: false
    next_strategy: select
    next: [implement, audit]
  - name: implement
    agent: fixer
    skill: code-writing
    approval: true
  - name: audit
    agent: auditor
    skill: code-audit
    approval: false
`, name)
}

func renderWorkflowMarkdownTemplate(name string) string {
	return fmt.Sprintf(`# %s Workflow

This workflow is executable with:

`+"```text"+`
/workflow %s <request>
`+"```"+`

Edit `+"`workflow.yaml`"+` to declare staged agent/skill orchestration.

## Stages

1. Plan: collect context and produce executable steps.
2. Implement: apply approved changes with the fixer agent.
3. Audit: verify results and report residual risk.

## Approval Boundaries

- `+"`approval: true`"+` pauses before a stage starts.
- Tool approval is separate. A stage can start, then pause again if the selected agent calls a confirm-policy tool such as `+"`file_tools/write_file`"+`.
- After tool approval, GoFlow resumes the suspended stage with the approved tool result and then continues to the next stage.

For dynamic branching, have the planning stage produce one of:

- Next skill: code-writing
- Next skills: code-writing, code-audit

## Implementation Notes

- Keep each stage tied to one agent permission boundary.
- Use skill names to keep stage prompts reusable.
- Add explicit approval boundaries before write or exec stages.
- See `+"`docs/workflows.md`"+` for branch selection, stage approval, and approval-resume examples.
`, name, name)
}
