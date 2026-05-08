package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentpkg "github.com/FyMatt/GoFlow-Agent/internal/agent"
	skillpkg "github.com/FyMatt/GoFlow-Agent/internal/skill"
	"gopkg.in/yaml.v3"
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
	configPath := filepath.Join(root, "configs", "mcp_servers", name+".yaml")
	content := renderPythonMCPServerTemplate(name)
	if err := validatePythonMCPScaffold(content); err != nil {
		fmt.Fprintf(os.Stderr, "new tool validation error: %v\n", err)
		return true
	}
	configContent := renderMCPServerConfigTemplate(name)
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	if err := writeNewFile(configPath, configContent); err != nil {
		fmt.Fprintf(os.Stderr, "new tool error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("tool", fmt.Sprintf("created validated python MCP scaffold at %s", path)))
	fmt.Println(formatCommandSuccess("tool", fmt.Sprintf("created modular MCP config at %s", configPath)))
	fmt.Println(styleMuted("  next: review tool permissions, restart GoFlow or run /reload-tools after rebootstrap, then run /tools"))
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
	fmt.Println(styleMuted("  next: review permissions, restart GoFlow so configs/agents/*.yaml is loaded, then run /agents"))
	return true
}

func handleNewProviderCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 2 {
		fmt.Fprintln(os.Stderr, "usage: /new-provider <name>")
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[1:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new provider error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new provider error: %v\n", err)
		return true
	}
	path := filepath.Join(root, "configs", "providers", name+".yaml")
	content := renderProviderConfigTemplate(name)
	if err := validateProviderScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new provider validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new provider error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("provider", fmt.Sprintf("created validated config snippet at %s", path)))
	fmt.Println(styleMuted("  next: fill base_url/api_key/model, restart GoFlow so configs/providers/*.yaml is loaded, then assign agents to this provider"))
	return true
}

func handleNewKitCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintf(os.Stderr, "usage: /new-kit <preset> <name> [--materialize|--full]\n")
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(kitScaffoldPresetNames(), ", "))
		return true
	}
	presetName := strings.ToLower(strings.TrimSpace(fields[1]))
	preset, ok := kitScaffoldPresetByName(presetName)
	if !ok {
		fmt.Fprintf(os.Stderr, "new kit error: unknown preset %q\n", fields[1])
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(kitScaffoldPresetNames(), ", "))
		return true
	}
	materialize := false
	nameFields := make([]string, 0, len(fields)-2)
	for _, field := range fields[2:] {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "--materialize", "--full":
			materialize = true
		default:
			nameFields = append(nameFields, field)
		}
	}
	name, err := normalizeNewSkillName(strings.Join(nameFields, " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new kit error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new kit error: %v\n", err)
		return true
	}
	if materialize {
		if err := createMaterializedKitScaffold(root, name, preset); err != nil {
			fmt.Fprintf(os.Stderr, "new kit error: %v\n", err)
			return true
		}
		return true
	}
	path := filepath.Join(root, "kits", name, "kit.yaml")
	content := renderKitYAMLTemplate(name, preset)
	if err := validateKitScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new kit validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new kit error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("kit", fmt.Sprintf("created %s kit manifest at %s", preset.Name, path)))
	fmt.Println(styleMuted("  next: review referenced agents/skills/workflows, then open Studio resources or commit the kit.yaml"))
	return true
}

func handleNewPolicyRuleCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintf(os.Stderr, "usage: /new-policy-rule <preset> <name>\n")
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(policyRuleScaffoldPresetNames(), ", "))
		return true
	}
	presetName := strings.ToLower(strings.TrimSpace(fields[1]))
	preset, ok := policyRuleScaffoldPresetByName(presetName)
	if !ok {
		fmt.Fprintf(os.Stderr, "new policy rule error: unknown preset %q\n", fields[1])
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(policyRuleScaffoldPresetNames(), ", "))
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[2:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule error: %v\n", err)
		return true
	}
	path := filepath.Join(root, "policies", "workflow_rules", name+".yaml")
	content, err := renderPolicyRuleYAMLTemplate(name, preset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule error: %v\n", err)
		return true
	}
	if err := validatePolicyRuleScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new policy rule error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("policy-rule", fmt.Sprintf("created %s rule at %s", preset.Name, path)))
	fmt.Println(styleMuted("  next: use /policy-rules to confirm it is loaded, then select it from workflow policy_guard nodes"))
	return true
}

func handleNewTeamCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintf(os.Stderr, "usage: /new-team <preset> <name>\n")
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(teamScaffoldPresetNames(), ", "))
		return true
	}
	presetName := strings.ToLower(strings.TrimSpace(fields[1]))
	preset, ok := teamScaffoldPresetByName(presetName)
	if !ok {
		fmt.Fprintf(os.Stderr, "new team error: unknown preset %q\n", fields[1])
		fmt.Fprintf(os.Stderr, "available presets: %s\n", strings.Join(teamScaffoldPresetNames(), ", "))
		return true
	}
	name, err := normalizeNewSkillName(strings.Join(fields[2:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new team error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new team error: %v\n", err)
		return true
	}
	path := filepath.Join(root, "templates", "teams", name+".yaml")
	content, err := renderTeamTemplateYAMLTemplate(name, preset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new team error: %v\n", err)
		return true
	}
	if err := validateTeamTemplateScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new team validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new team error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("team", fmt.Sprintf("created %s team template at %s", preset.Name, path)))
	fmt.Println(styleMuted("  next: run /teams to inspect it, then reference it from workflow team nodes"))
	return true
}

func handleNewWorkflowCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 2 {
		fmt.Fprintln(os.Stderr, "usage: /new-workflow <name> [--template <template>]")
		return true
	}
	nameInput, templateName, err := parseNewWorkflowArgs(fields[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
	name, err := normalizeNewSkillName(nameInput)
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
	yamlContent, err := renderWorkflowYAMLTemplate(name, templateName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow error: %v\n", err)
		return true
	}
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
	fmt.Println(formatCommandSuccess("workflow", fmt.Sprintf("template=%s", templateName)))
	fmt.Println(styleMuted(fmt.Sprintf("  next: run /workflow %s <request> after adjusting stage skills and approvals", name)))
	return true
}

func handleNewWorkflowTemplateCommand(fields []string, skillManager *skillpkg.Manager) bool {
	if len(fields) < 3 {
		fmt.Fprintln(os.Stderr, "usage: /new-workflow-template <source-template> <name>")
		fmt.Fprintf(os.Stderr, "available sources: %s\n", strings.Join(workflowTemplateScaffoldSourceNames(), ", "))
		return true
	}
	source := strings.TrimSpace(fields[1])
	name, err := normalizeNewSkillName(strings.Join(fields[2:], " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow template error: %v\n", err)
		return true
	}
	root, err := runtimeHomeFromSkillManager(skillManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow template error: %v\n", err)
		return true
	}
	path := filepath.Join(root, "templates", "workflows", name+".yaml")
	content, err := renderWorkflowTemplateResourceYAML(name, source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new workflow template error: %v\n", err)
		return true
	}
	if err := validateWorkflowTemplateScaffold(name, content); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow template validation error: %v\n", err)
		return true
	}
	if err := writeNewFile(path, content); err != nil {
		fmt.Fprintf(os.Stderr, "new workflow template error: %v\n", err)
		return true
	}
	fmt.Println(formatCommandSuccess("workflow-template", fmt.Sprintf("forked %s into %s", source, path)))
	fmt.Println(styleMuted(fmt.Sprintf("  next: run /workflow-templates %s, then create workflows with /new-workflow <name> --template %s", name, name)))
	return true
}

func parseNewWorkflowArgs(fields []string) (string, string, error) {
	templateName := "plan-fix-audit"
	nameFields := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		field := strings.TrimSpace(fields[i])
		switch field {
		case "--template", "-t":
			if i+1 >= len(fields) {
				return "", "", fmt.Errorf("%s requires a template name", field)
			}
			templateName = strings.TrimSpace(fields[i+1])
			i++
		default:
			nameFields = append(nameFields, field)
		}
	}
	name := strings.TrimSpace(strings.Join(nameFields, " "))
	if name == "" {
		return "", "", fmt.Errorf("workflow name is required")
	}
	return name, templateName, nil
}

func validatePythonMCPScaffold(content string) error {
	required := []string{
		"GOFLOW_WORKSPACE_ROOT",
		"tools/list",
		"tools/call",
		"additionalProperties",
		"resolve_path",
		"relative_path",
		"is_error",
		"escapes workspace root",
		"resolves outside workspace root",
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

func validateProviderScaffold(name, content string) error {
	required := []string{
		name + ":",
		"provider:",
		"base_url:",
		"api_key:",
		"model:",
		"timeout:",
		"retry_count:",
	}
	return requireScaffoldContent("provider scaffold", content, required)
}

func validateWorkflowScaffold(name, content string) error {
	required := []string{
		"name: " + name,
		"stages:",
		"agent:",
		"skill:",
		"node_type:",
	}
	return requireScaffoldContent("workflow scaffold", content, required)
}

func validateWorkflowTemplateScaffold(name, content string) error {
	var template agentpkg.WorkflowTemplate
	if err := yaml.Unmarshal([]byte(content), &template); err != nil {
		return fmt.Errorf("parse workflow template scaffold: %w", err)
	}
	if template.Name != name {
		return fmt.Errorf("workflow template scaffold name %q does not match %q", template.Name, name)
	}
	if strings.TrimSpace(template.Graph.Name) != name {
		return fmt.Errorf("workflow template graph name %q does not match %q", template.Graph.Name, name)
	}
	if len(template.Graph.Stages) == 0 {
		return fmt.Errorf("workflow template scaffold requires at least one stage")
	}
	required := []string{
		"kind: goflow.workflow_template_resource",
		"version: 2",
		"name: " + name,
		"graph:",
		"stages:",
	}
	return requireScaffoldContent("workflow template scaffold", content, required)
}

func validateKitScaffold(name, content string) error {
	required := []string{
		"kind: goflow.kit",
		"version: 1",
		"name: " + name,
		"title:",
		"description:",
		"agents:",
		"skills:",
		"examples:",
	}
	return requireScaffoldContent("kit scaffold", content, required)
}

func validatePolicyRuleScaffold(name, content string) error {
	var definition agentpkg.WorkflowPolicyRuleDefinition
	if err := yaml.Unmarshal([]byte(content), &definition); err != nil {
		return fmt.Errorf("parse policy rule scaffold: %w", err)
	}
	if definition.Name != name {
		return fmt.Errorf("policy rule scaffold name %q does not match %q", definition.Name, name)
	}
	if err := agentpkg.ValidateWorkflowPolicyRuleDefinition(definition); err != nil {
		return err
	}
	required := []string{
		"kind: goflow.workflow_policy_rule",
		"version: 2",
		"name: " + name,
		"operator:",
	}
	return requireScaffoldContent("policy rule scaffold", content, required)
}

func validateTeamTemplateScaffold(name, content string) error {
	var template agentpkg.TeamTemplate
	if err := yaml.Unmarshal([]byte(content), &template); err != nil {
		return fmt.Errorf("parse team template scaffold: %w", err)
	}
	if template.Name != name {
		return fmt.Errorf("team template scaffold name %q does not match %q", template.Name, name)
	}
	if _, err := (&agentpkg.WorkflowRunner{}).ValidateTeamTemplateResource(name, template); err != nil {
		return err
	}
	required := []string{
		"kind: goflow.team_template_resource",
		"version: 2",
		"name: " + name,
		"role_templates:",
	}
	return requireScaffoldContent("team template scaffold", content, required)
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
Its startup config belongs in configs/mcp_servers/%s.yaml.
"""
import json
import os
import sys
from pathlib import Path


MAX_TEXT_BYTES = 1024 * 1024
SERVER_NAME = %q


def workspace_root() -> Path:
    value = os.environ.get("GOFLOW_WORKSPACE_ROOT", "").strip()
    if value:
        return Path(value).resolve()
    return Path(os.environ.get("WORKSPACE_ROOT") or os.getcwd()).resolve()


WORKSPACE_ROOT = workspace_root()


def respond(request_id, result=None, error=None):
    payload = {"jsonrpc": "2.0", "id": request_id}
    if error is not None:
        payload["error"] = {"code": -32000, "message": str(error)}
    else:
        payload["result"] = result
    print(json.dumps(payload), flush=True)


def resolve_path(raw, must_exist=True, allow_root=False):
    if not isinstance(raw, str) or not raw.strip():
        raise ValueError("path is required")
    candidate = Path(raw)
    if not candidate.is_absolute():
        candidate = WORKSPACE_ROOT / candidate
    candidate = candidate.resolve(strict=False)
    try:
        candidate.relative_to(WORKSPACE_ROOT)
    except ValueError as exc:
        raise ValueError(f'path "{raw}" escapes workspace root') from exc
    if not allow_root and candidate == WORKSPACE_ROOT:
        raise ValueError("path must not be workspace root")
    if must_exist:
        if not candidate.exists():
            raise FileNotFoundError(str(candidate))
        candidate = candidate.resolve(strict=True)
        try:
            candidate.relative_to(WORKSPACE_ROOT)
        except ValueError as exc:
            raise ValueError(f'path "{raw}" resolves outside workspace root') from exc
        if not allow_root and candidate == WORKSPACE_ROOT:
            raise ValueError("path must not be workspace root")
        return candidate

    parent = candidate.parent
    while not parent.exists():
        if parent == parent.parent:
            raise ValueError(f'path "{raw}" has no existing parent inside workspace')
        parent = parent.parent
    parent = parent.resolve(strict=True)
    try:
        parent.relative_to(WORKSPACE_ROOT)
    except ValueError as exc:
        raise ValueError(f'path "{raw}" resolves outside workspace root') from exc
    return candidate


def relative_path(path):
    try:
        return path.relative_to(WORKSPACE_ROOT).as_posix()
    except ValueError:
        return str(path)


def read_text_limited(path):
    size = path.stat().st_size
    if size > MAX_TEXT_BYTES:
        raise ValueError(f"file too large: {size} bytes exceeds {MAX_TEXT_BYTES}")
    return path.read_text(encoding="utf-8-sig")


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


def handle_ping(_args):
    return {"server": SERVER_NAME, "status": "ok"}


def handle_read_text(args):
    path = resolve_path(args.get("path", ""))
    return {"path": str(path), "relative_path": relative_path(path), "content": read_text_limited(path)}


def handle_write_text(args):
    path = resolve_path(args.get("path", ""), must_exist=False)
    if path.exists() and not args.get("overwrite", False):
        raise ValueError("file exists; pass overwrite=true to replace it")
    if path.exists() and path.is_dir():
        raise ValueError("path is a directory")
    content = args.get("content")
    if not isinstance(content, str):
        raise ValueError("content must be a string")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    return {"path": str(path), "relative_path": relative_path(path), "bytes_written": len(content.encode("utf-8"))}


def call_tool(params):
    name = params.get("name", "") if isinstance(params, dict) else ""
    args = params.get("arguments") if isinstance(params, dict) else {}
    if args is None:
        args = {}
    if not isinstance(args, dict):
        return {"content": "arguments must be an object", "is_error": True}
    handlers = {
        "ping": handle_ping,
        "read_text": handle_read_text,
        "write_text": handle_write_text,
    }
    handler = handlers.get(name)
    if handler is None:
        return {"content": f"unknown tool: {name}", "is_error": True}
    try:
        body = handler(args)
        return {"content": json.dumps(body, ensure_ascii=False), "is_error": False}
    except Exception as exc:
        return {"content": str(exc), "is_error": True}


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
        request_id = None
        try:
            request = json.loads(line)
            request_id = request.get("id")
            respond(request.get("id"), handle(request))
        except Exception as exc:
            respond(request_id, error=exc)


if __name__ == "__main__":
    main()
`, serverName, name, serverName)
}

func renderMCPServerConfigTemplate(name string) string {
	serverName := strings.ReplaceAll(name, "-", "_")
	return fmt.Sprintf(`# Loaded automatically from configs/mcp_servers/*.yaml on next startup.
mcp_servers:
  - name: %s
    command: python
    args:
      - ./mcp_servers/%s.py
    enabled: true
    timeout: 30s
    workdir: .
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, TMP, TEMP]
    isolation: process_group
    restart_limit: 3
    cooldown: 10s
    allowed_commands:
      - python
    max_request_bytes: 65536
    max_response_bytes: 2097152
`, serverName, name)
}

func renderAgentConfigTemplate(name string) string {
	title := strings.ReplaceAll(name, "-", " ")
	return fmt.Sprintf(`# Loaded automatically from configs/agents/*.yaml on next startup.
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

func renderProviderConfigTemplate(name string) string {
	envName := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name)) + "_API_KEY"
	return fmt.Sprintf(`# Loaded automatically from configs/providers/*.yaml on next startup.
# Fill in provider details before assigning agents to this provider.
%s:
  provider: openai-compatible
  base_url: https://api.example.com/v1
  api_key: ${%s}
  model: model-name
  timeout: 60s
  temperature: 0.2
  max_tokens: 4096
  retry_count: 2
  retry_backoff: 2s
`, name, envName)
}

type kitScaffoldPresetDefinition struct {
	Name              string
	Title             string
	Description       string
	Category          string
	Tags              []string
	Agents            []string
	Providers         []string
	Skills            []string
	Tools             []string
	Workflows         []string
	WorkflowTemplates []string
	TeamTemplates     []string
	PolicyRules       []string
	RequiredEnv       []string
	ExampleRequest    string
	ExampleWorkflow   string
	ExampleAgent      string
}

func kitScaffoldPresetByName(name string) (kitScaffoldPresetDefinition, bool) {
	for _, preset := range kitScaffoldPresets() {
		if strings.EqualFold(preset.Name, name) {
			return preset, true
		}
	}
	return kitScaffoldPresetDefinition{}, false
}

func kitScaffoldPresetNames() []string {
	presets := kitScaffoldPresets()
	names := make([]string, 0, len(presets))
	for _, preset := range presets {
		names = append(names, preset.Name)
	}
	sort.Strings(names)
	return names
}

func kitScaffoldPresets() []kitScaffoldPresetDefinition {
	return []kitScaffoldPresetDefinition{
		{
			Name:              "multi-domain-agent",
			Title:             "Multi-Domain Agent Starter Kit",
			Description:       "Route mixed requests to linked software, security, documentation, operations, support, and platform teams.",
			Category:          "starter",
			Tags:              []string{"starter", "multi-domain", "workflow", "team", "quality"},
			Agents:            []string{"chat", "planner", "fixer", "auditor"},
			Skills:            []string{"execution-plan", "code-writing", "code-audit", "vulnerability-research", "web-vulnerability-research", "binary-vulnerability-research", "reverse-engineering"},
			Tools:             []string{"file_tools/read_file", "file_tools/search_files", "file_tools/write_file", "web_tools/web_search", "web_tools/fetch_url", "web_tools/fetch_page_assets", "python_notes/binary_file_info", "python_notes/binary_strings", "python_notes/hex_preview"},
			Workflows:         []string{"plan-fix-audit", "skill-chain"},
			WorkflowTemplates: []string{"multi-domain-intake-router", "task-decomposition-plan", "plan-fix-audit", "software-team-review-gate", "web-research-risk", "binary-triage", "docs-review-publish", "operations-runbook", "customer-support-triage", "agent-framework-extension"},
			TeamTemplates:     []string{"software-task-team", "audit-security-team", "web-research-team", "binary-triage-team", "documentation-team", "operations-runbook-team", "customer-support-team", "framework-extension-team"},
			PolicyRules:       []string{"expression", "contains", "risk_at_least", "ref_truthy", "team_approval_gate"},
			ExampleRequest:    "Help me decide how to handle this project request and route it to the right workflow.",
			ExampleWorkflow:   "multi-domain-intake-router",
			ExampleAgent:      "chat",
		},
		{
			Name:              "software-engineering",
			Title:             "Software Engineering Kit",
			Description:       "Plan, implement, verify, and audit software changes with explicit approval boundaries.",
			Category:          "software",
			Tags:              []string{"software", "coding", "review"},
			Agents:            []string{"planner", "fixer", "auditor"},
			Skills:            []string{"execution-plan", "code-writing", "code-audit"},
			Workflows:         []string{"plan-fix-audit"},
			WorkflowTemplates: []string{"plan-fix-audit", "software-team-review-gate"},
			TeamTemplates:     []string{"software-task-team"},
			ExampleRequest:    "Refactor the config loader and add tests.",
			ExampleWorkflow:   "plan-fix-audit",
			ExampleAgent:      "chat",
		},
		{
			Name:              "agent-framework",
			Title:             "Agent Framework Extension Kit",
			Description:       "Design and generate linked Agent, Skill, Tool, Workflow, Team, Policy, and Kit resources for GoFlow second development.",
			Category:          "platform",
			Tags:              []string{"agent-framework", "extension", "kit", "workflow"},
			Agents:            []string{"chat", "planner", "fixer", "auditor"},
			Skills:            []string{"execution-plan", "code-writing", "code-audit"},
			Tools:             []string{"file_tools/read_file", "file_tools/search_files", "file_tools/write_file"},
			WorkflowTemplates: []string{"agent-framework-extension", "multi-domain-intake-router", "task-decomposition-plan", "software-quality-gate"},
			TeamTemplates:     []string{"framework-extension-team", "software-task-team"},
			PolicyRules:       []string{"ref_truthy", "team_approval_gate", "expression"},
			ExampleRequest:    "Create a vertical Agent kit for an internal release-review assistant.",
			ExampleWorkflow:   "agent-framework-extension",
			ExampleAgent:      "planner",
		},
		{
			Name:              "web-security",
			Title:             "Web Security Kit",
			Description:       "Collect target pages and assets, audit source evidence, and triage likely web vulnerabilities.",
			Category:          "security",
			Tags:              []string{"security", "web", "audit"},
			Agents:            []string{"planner", "auditor"},
			Skills:            []string{"web-vulnerability-research", "vulnerability-research", "code-audit"},
			Tools:             []string{"web_tools/fetch_url", "web_tools/fetch_page_assets", "web_tools/web_search"},
			WorkflowTemplates: []string{"web-research-risk"},
			TeamTemplates:     []string{"web-research-team", "audit-security-team"},
			ExampleRequest:    "Assess this target URL for client-side security issues.",
			ExampleAgent:      "auditor",
		},
		{
			Name:              "security-research",
			Title:             "Security Research Kit",
			Description:       "Coordinate reconnaissance, evidence collection, vulnerability research, and risk review.",
			Category:          "security",
			Tags:              []string{"security", "research", "triage"},
			Agents:            []string{"planner", "auditor"},
			Skills:            []string{"vulnerability-research", "code-audit", "execution-plan"},
			WorkflowTemplates: []string{"human-input-security-review"},
			TeamTemplates:     []string{"audit-security-team"},
			PolicyRules:       []string{"risk_at_least", "team_approval_gate"},
			ExampleRequest:    "Create a safe vulnerability research plan for the provided scope.",
			ExampleAgent:      "auditor",
		},
		{
			Name:              "binary-analysis",
			Title:             "Binary Analysis Kit",
			Description:       "Triage binaries, extract strings/metadata, and plan reverse-engineering evidence review.",
			Category:          "security",
			Tags:              []string{"binary", "reverse-engineering", "security"},
			Agents:            []string{"planner", "auditor"},
			Skills:            []string{"reverse-engineering", "binary-vulnerability-research", "vulnerability-research"},
			Tools:             []string{"python_notes/binary_file_info", "python_notes/binary_strings", "python_notes/hex_preview"},
			WorkflowTemplates: []string{"binary-triage"},
			TeamTemplates:     []string{"binary-triage-team"},
			ExampleRequest:    "Triage this binary and identify risky imports or suspicious strings.",
			ExampleAgent:      "auditor",
		},
		{
			Name:              "documentation",
			Title:             "Documentation Kit",
			Description:       "Plan, draft, review, and publish technical documentation updates.",
			Category:          "docs",
			Tags:              []string{"docs", "writing", "review"},
			Agents:            []string{"planner", "fixer", "auditor"},
			Skills:            []string{"execution-plan", "code-writing", "code-audit"},
			WorkflowTemplates: []string{"docs-review-publish"},
			TeamTemplates:     []string{"documentation-team"},
			ExampleRequest:    "Update the install guide for Docker and Windows releases.",
			ExampleAgent:      "chat",
		},
		{
			Name:              "operations-runbook",
			Title:             "Operations Runbook Kit",
			Description:       "Create runbooks, diagnose operational changes, and keep approval checkpoints explicit.",
			Category:          "operations",
			Tags:              []string{"ops", "runbook", "incident"},
			Agents:            []string{"planner", "fixer", "auditor"},
			Skills:            []string{"execution-plan", "code-writing", "code-audit"},
			WorkflowTemplates: []string{"operations-runbook"},
			TeamTemplates:     []string{"operations-runbook-team"},
			ExampleRequest:    "Draft a rollback runbook for a failed deployment.",
			ExampleAgent:      "planner",
		},
		{
			Name:              "customer-support",
			Title:             "Customer Support Kit",
			Description:       "Triage support requests, gather context, and prepare clear operator/customer responses.",
			Category:          "support",
			Tags:              []string{"support", "triage", "handoff"},
			Agents:            []string{"chat", "planner", "auditor"},
			Skills:            []string{"execution-plan", "code-audit"},
			WorkflowTemplates: []string{"customer-support-triage"},
			TeamTemplates:     []string{"customer-support-team"},
			ExampleRequest:    "Triage this customer report and draft a response with next steps.",
			ExampleAgent:      "chat",
		},
	}
}

func renderKitYAMLTemplate(name string, preset kitScaffoldPresetDefinition) string {
	title := preset.Title
	if title == "" {
		title = strings.ReplaceAll(name, "-", " ") + " Kit"
	}
	workflow := firstNonEmptyString(preset.ExampleWorkflow, firstString(preset.Workflows), firstString(preset.WorkflowTemplates))
	agent := firstNonEmptyString(preset.ExampleAgent, firstString(preset.Agents), "chat")
	var b strings.Builder
	b.WriteString("kind: goflow.kit\n")
	b.WriteString("version: 1\n")
	b.WriteString("min_supported_version: 1\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "title: %q\n", title)
	fmt.Fprintf(&b, "description: %q\n", preset.Description)
	fmt.Fprintf(&b, "category: %s\n", firstNonEmptyString(preset.Category, "custom"))
	writeYAMLStringList(&b, "tags", preset.Tags)
	writeYAMLStringList(&b, "providers", preset.Providers)
	writeYAMLStringList(&b, "agents", preset.Agents)
	writeYAMLStringList(&b, "skills", preset.Skills)
	writeYAMLStringList(&b, "tools", preset.Tools)
	writeYAMLStringList(&b, "workflows", preset.Workflows)
	writeYAMLStringList(&b, "workflow_templates", preset.WorkflowTemplates)
	writeYAMLStringList(&b, "team_templates", preset.TeamTemplates)
	writeYAMLStringList(&b, "policy_rules", preset.PolicyRules)
	writeYAMLStringList(&b, "required_env", preset.RequiredEnv)
	b.WriteString("examples:\n")
	fmt.Fprintf(&b, "  - title: %q\n", "Try the kit")
	fmt.Fprintf(&b, "    request: %q\n", firstNonEmptyString(preset.ExampleRequest, "Describe the task this kit should handle."))
	if workflow != "" {
		fmt.Fprintf(&b, "    workflow: %s\n", workflow)
	}
	if agent != "" {
		fmt.Fprintf(&b, "    agent: %s\n", agent)
	}
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  scaffold_preset: %s\n", preset.Name)
	b.WriteString("  owner: local\n")
	return b.String()
}

type cliMaterializedKitNames struct {
	Kit              string
	Agent            string
	Skill            string
	Tool             string
	ToolServer       string
	Workflow         string
	WorkflowTemplate string
	TeamTemplate     string
	PolicyRule       string
}

func createMaterializedKitScaffold(root, name string, preset kitScaffoldPresetDefinition) error {
	names := cliMaterializedKitResourceNames(name)
	files := []struct {
		kind    string
		path    string
		content string
	}{
		{"kit", filepath.Join(root, "kits", names.Kit, "kit.yaml"), renderMaterializedKitYAMLTemplate(names, preset)},
		{"agent", filepath.Join(root, "configs", "agents", names.Agent+".yaml"), renderMaterializedAgentConfigTemplate(names, preset)},
		{"skill", filepath.Join(root, "skills", names.Skill, "SKILL.md"), renderMaterializedSkillTemplate(names, preset)},
		{"tool", filepath.Join(root, "mcp_servers", names.Tool+".py"), renderPythonMCPServerTemplate(names.Tool)},
		{"tool config", filepath.Join(root, "configs", "mcp_servers", names.Tool+".yaml"), renderContainerMCPServerConfigTemplate(root, names)},
		{"workflow", filepath.Join(root, "workflows", names.Workflow, "workflow.yaml"), renderMaterializedWorkflowYAML(names, preset, names.Workflow)},
		{"workflow doc", filepath.Join(root, "workflows", names.Workflow, "WORKFLOW.md"), renderMaterializedWorkflowMarkdown(names, preset)},
		{"workflow template", filepath.Join(root, "templates", "workflows", names.WorkflowTemplate+".yaml"), renderMaterializedWorkflowTemplateYAML(names, preset)},
		{"team template", filepath.Join(root, "templates", "teams", names.TeamTemplate+".yaml"), renderMaterializedTeamTemplateYAML(names, preset)},
		{"policy rule", filepath.Join(root, "policies", "workflow_rules", names.PolicyRule+".yaml"), renderMaterializedPolicyRuleYAML(names, preset)},
	}
	if err := validateAgentScaffold(names.Agent, files[1].content); err != nil {
		return fmt.Errorf("validate materialized agent scaffold: %w", err)
	}
	if err := validatePythonMCPScaffold(files[3].content); err != nil {
		return fmt.Errorf("validate materialized tool scaffold: %w", err)
	}
	if err := validateKitScaffold(names.Kit, files[0].content); err != nil {
		return fmt.Errorf("validate materialized kit scaffold: %w", err)
	}
	for _, file := range files {
		if err := writeNewFile(file.path, file.content); err != nil {
			return fmt.Errorf("create %s: %w", file.kind, err)
		}
		fmt.Println(formatCommandSuccess("kit", fmt.Sprintf("created %s at %s", file.kind, file.path)))
	}
	fmt.Println(styleMuted("  next: restart GoFlow so generated agent/tool modules are loaded, then run the generated workflow in Studio"))
	fmt.Println(styleMuted("  sandbox: generated helper uses Docker/Podman container isolation with a read-only workspace mount"))
	return nil
}

func cliMaterializedKitResourceNames(name string) cliMaterializedKitNames {
	base := normalizeResourceNameForCLI(name)
	return cliMaterializedKitNames{
		Kit:              base,
		Agent:            base + "-agent",
		Skill:            base + "-skill",
		Tool:             base + "-helper",
		ToolServer:       strings.ReplaceAll(base+"-helper", "-", "_"),
		Workflow:         base + "-workflow",
		WorkflowTemplate: base + "-template",
		TeamTemplate:     base + "-team",
		PolicyRule:       base + "-gate",
	}
}

func normalizeResourceNameForCLI(name string) string {
	return strings.Trim(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), " ", "-")), "-")
}

func renderMaterializedKitYAMLTemplate(names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) string {
	var b strings.Builder
	b.WriteString("kind: goflow.kit\n")
	b.WriteString("version: 1\n")
	b.WriteString("min_supported_version: 1\n")
	fmt.Fprintf(&b, "name: %s\n", names.Kit)
	fmt.Fprintf(&b, "title: %q\n", firstNonEmptyString(preset.Title, strings.ReplaceAll(names.Kit, "-", " ")+" Kit"))
	fmt.Fprintf(&b, "description: %q\n", firstNonEmptyString(preset.Description, "Linked materialized starter kit."))
	fmt.Fprintf(&b, "category: %s\n", firstNonEmptyString(preset.Category, "custom"))
	writeYAMLStringList(&b, "tags", append(append([]string(nil), preset.Tags...), "materialized", "starter", "linked"))
	writeYAMLStringList(&b, "providers", []string{"primary"})
	writeYAMLStringList(&b, "agents", []string{names.Agent})
	writeYAMLStringList(&b, "skills", []string{names.Skill})
	writeYAMLStringList(&b, "tools", []string{names.ToolServer})
	writeYAMLStringList(&b, "workflows", []string{names.Workflow})
	writeYAMLStringList(&b, "workflow_templates", []string{names.WorkflowTemplate})
	writeYAMLStringList(&b, "team_templates", []string{names.TeamTemplate})
	writeYAMLStringList(&b, "policy_rules", []string{names.PolicyRule})
	writeYAMLStringList(&b, "required_env", preset.RequiredEnv)
	b.WriteString("examples:\n")
	fmt.Fprintf(&b, "  - title: %q\n", "Run the linked starter workflow")
	fmt.Fprintf(&b, "    request: %q\n", firstNonEmptyString(preset.ExampleRequest, "Describe the task, scope, expected output, and acceptance criteria."))
	fmt.Fprintf(&b, "    workflow: %s\n", names.Workflow)
	fmt.Fprintf(&b, "    agent: %s\n", names.Agent)
	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  scaffold_preset: %s\n", preset.Name)
	b.WriteString("  materialized: \"true\"\n")
	fmt.Fprintf(&b, "  generated_agent: %s\n", names.Agent)
	fmt.Fprintf(&b, "  generated_skill: %s\n", names.Skill)
	fmt.Fprintf(&b, "  generated_tool: %s\n", names.ToolServer)
	fmt.Fprintf(&b, "  generated_workflow: %s\n", names.Workflow)
	return b.String()
}

func renderMaterializedAgentConfigTemplate(names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) string {
	title := firstNonEmptyString(preset.Title, strings.ReplaceAll(names.Kit, "-", " ")+" Kit")
	return fmt.Sprintf(`# Loaded automatically from configs/agents/*.yaml on next startup.
# Generated as part of a linked materialized starter kit.
%s:
  name: %s Agent
  description: Entry agent for the %s linked starter kit.
  provider: primary
  mode: chat
  tool_policy: confirm
  allowed_tool_kinds: [read]
  allowed_tools: [%s/ping, %s/read_text]
  max_iterations: 6
  system_prompt: |
    You are the entry agent for the %s kit.
    Use the linked skill, workflow, team template, and policy gate as the default operating model.
    Keep outputs concise, evidence-backed, and suitable for downstream workflow nodes.
`, names.Agent, title, names.Kit, names.ToolServer, names.ToolServer, names.Kit)
}

func renderMaterializedSkillTemplate(names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) string {
	return renderSkillTemplate(names.Skill, skillTemplateDefinition{
		Name:             names.Skill,
		Description:      "Linked starter skill for planning, evidence collection, review, and handoff inside a generated kit workflow.",
		Mode:             "chat",
		PreferredAgent:   names.Agent,
		AllowedToolKinds: []string{"read"},
		OutputKind:       "structured_handoff",
		Tools:            []skillTemplateTool{{Name: names.ToolServer + "/read_text"}},
		Keywords:         []string{names.Kit, preset.Name, "starter-kit", "workflow-handoff"},
		ExampleRequest:   firstNonEmptyString(preset.ExampleRequest, "Create a scoped plan and final handoff."),
		Body: `## Role

Operate as a reusable workflow skill for this kit. Produce compact, structured outputs that can be passed to the next workflow node.

## Workflow

1. Restate the current task, scope, assumptions, and missing inputs.
2. Collect only the evidence required for the current stage. Use the linked read-only helper tool when a workspace file must be inspected.
3. Emit clear sections for Findings, Decisions, Risks, Next Inputs, and Acceptance Criteria.
4. Keep large raw evidence out of the final answer; reference artifact IDs or file paths instead.
5. When blocked, state the exact missing input or approval required so the workflow can route correctly.`,
	})
}

func renderContainerMCPServerConfigTemplate(root string, names cliMaterializedKitNames) string {
	toolSource := filepath.ToSlash(filepath.Join(root, "mcp_servers", names.Tool+".py"))
	return fmt.Sprintf(`# Loaded automatically from configs/mcp_servers/*.yaml on next startup.
# Generated helper runs through Docker/Podman-style container isolation.
mcp_servers:
  - name: %s
    command: python
    args:
      - /goflow-tools/%s.py
    enabled: true
    timeout: 30s
    workdir: .
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, TMP, TEMP]
    network_disabled: true
    isolation: container
    isolation_options:
      runtime: docker
      image: ghcr.io/fymatt/goflow-agent-mcp-python:latest
      workspace_mount: ro
      workspace_target: /workspace
      container_workdir: /workspace
      network: disabled
      ipc: none
      userns: auto
      memory: 256m
      memory_swap: 256m
      cpus: "0.5"
      pids_limit: "64"
      pull_policy: missing
      readonly_rootfs: "true"
      no_new_privileges: "true"
      cap_drop: all
      tmpfs: /tmp:rw,noexec,nosuid,size=64m;/run:rw,noexec,nosuid,size=8m
      init: "true"
      tool_mount: ro
      tool_source: %s
      tool_target: /goflow-tools/%s.py
      user: 65532:65532
    restart_limit: 3
    cooldown: 10s
    allowed_commands:
      - python
    max_request_bytes: 65536
    max_response_bytes: 2097152
`, names.ToolServer, names.Tool, toolSource, names.Tool)
}

func renderMaterializedWorkflowYAML(names cliMaterializedKitNames, preset kitScaffoldPresetDefinition, graphName string) string {
	graph := materializedWorkflowGraph(names, preset, graphName)
	data, err := yaml.Marshal(graph)
	if err != nil {
		return fmt.Sprintf("name: %s\nstages: []\n", graphName)
	}
	return string(data)
}

func renderMaterializedWorkflowTemplateYAML(names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) string {
	template := agentpkg.WorkflowTemplate{
		WorkflowTemplateSummary: agentpkg.WorkflowTemplateSummary{
			Kind:        "goflow.workflow_template_resource",
			Version:     2,
			MinVersion:  1,
			Name:        names.WorkflowTemplate,
			Title:       firstNonEmptyString(preset.Title, strings.ReplaceAll(names.Kit, "-", " ")+" Kit") + " Workflow Template",
			Description: "Reusable linked workflow template generated from a GoFlow kit scaffold.",
			Category:    firstNonEmptyString(preset.Category, "custom"),
			Tags:        append(append([]string(nil), preset.Tags...), "materialized", "starter", "linked"),
		},
		Graph: materializedWorkflowGraph(names, preset, names.WorkflowTemplate),
	}
	data, err := yaml.Marshal(template)
	if err != nil {
		return ""
	}
	return "# Loaded automatically from templates/workflows/*.yaml.\n" + string(data)
}

func materializedWorkflowGraph(names cliMaterializedKitNames, preset kitScaffoldPresetDefinition, graphName string) agentpkg.WorkflowGraphDocument {
	title := firstNonEmptyString(preset.Title, strings.ReplaceAll(names.Kit, "-", " ")+" Kit")
	return agentpkg.WorkflowGraphDocument{
		Name:        graphName,
		Description: "Linked starter workflow. Earlier node outputs are passed into later node inputs.",
		Stages: []agentpkg.WorkflowGraphStageDocument{
			{Name: "start", NodeType: "start", Next: []string{"team"}, Position: agentpkg.WorkflowGraphPosition{X: 80, Y: 260}},
			{Name: "team", NodeType: "team", Agent: names.Agent, Params: map[string]string{"team": names.TeamTemplate, "execute": "false"}, Outputs: map[string]string{"team": "result.structured", "roles": "result.roles"}, Next: []string{"plan"}, Position: agentpkg.WorkflowGraphPosition{X: 340, Y: 260}},
			{Name: "plan", NodeType: "skill", Agent: names.Agent, Skill: names.Skill, Input: map[string]string{"request": "params.request", "scope": "params.scope", "team": "stages.team.outputs.team"}, Outputs: map[string]string{"summary": "result.summary", "plan": "result.output", "findings": "result.findings"}, Artifacts: []agentpkg.WorkflowGraphArtifactDocument{{Name: "starter-plan", Kind: "plan", Ref: "result.output", Title: title + " Plan"}}, Next: []string{"gate"}, Position: agentpkg.WorkflowGraphPosition{X: 620, Y: 260}},
			{Name: "gate", NodeType: "policy_guard", Params: map[string]string{"rule": names.PolicyRule, "ref": "stages.plan.outputs.summary", "reason": "The planning stage did not produce a usable handoff summary."}, Routes: map[string]string{"allow": "report", "deny": "revise"}, Position: agentpkg.WorkflowGraphPosition{X: 900, Y: 260}},
			{Name: "report", NodeType: "skill", Agent: names.Agent, Skill: names.Skill, Input: map[string]string{"request": "Build the final handoff from the approved plan.", "upstream": "stages.plan.outputs.plan"}, Outputs: map[string]string{"final_report": "result.output", "summary": "result.summary"}, Artifacts: []agentpkg.WorkflowGraphArtifactDocument{{Name: "starter-final-report", Kind: "report", Ref: "result.output", Title: title + " Final Report"}}, Next: []string{"end"}, Position: agentpkg.WorkflowGraphPosition{X: 1190, Y: 180}},
			{Name: "revise", NodeType: "skill", Agent: names.Agent, Skill: names.Skill, Input: map[string]string{"request": "Revise the workflow handoff because the policy gate denied the previous output.", "upstream": "stages.gate.outputs.reason"}, Outputs: map[string]string{"revision_plan": "result.output", "summary": "result.summary"}, Next: []string{"end"}, Position: agentpkg.WorkflowGraphPosition{X: 1190, Y: 360}},
			{Name: "end", NodeType: "end", Position: agentpkg.WorkflowGraphPosition{X: 1500, Y: 260}},
		},
	}
}

func renderMaterializedTeamTemplateYAML(names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) string {
	template := agentpkg.TeamTemplate{
		TeamTemplateSummary: agentpkg.TeamTemplateSummary{
			Kind:                  "goflow.team_template_resource",
			Version:               2,
			MinVersion:            1,
			Name:                  names.TeamTemplate,
			Title:                 firstNonEmptyString(preset.Title, strings.ReplaceAll(names.Kit, "-", " ")+" Kit") + " Team",
			Description:           "Linked starter team template generated with the kit scaffold.",
			Category:              firstNonEmptyString(preset.Category, "custom"),
			Tags:                  append(append([]string(nil), preset.Tags...), "materialized", "starter", "team"),
			RecommendedWorkflow:   names.Workflow,
			RecommendedEntryAgent: names.Agent,
		},
		RoleTemplates: []agentpkg.TeamRoleTemplate{
			{Name: "planner", Label: "Planner", Agent: names.Agent, Skill: names.Skill, Responsibilities: []string{"clarify scope", "produce a compact execution plan", "declare evidence and acceptance criteria"}, Produces: []string{"plan", "acceptance_criteria"}, Tools: []string{names.ToolServer + "/read_text"}},
			{Name: "reviewer", Label: "Reviewer", Agent: names.Agent, Skill: names.Skill, Responsibilities: []string{"review the plan", "identify risks", "approve or request revision"}, Consumes: []string{"plan"}, Produces: []string{"review_decision", "risk_notes"}},
		},
		Handoffs: []agentpkg.TeamHandoffTemplate{{From: "planner", To: "reviewer", Kind: "review", Subject: "Plan review and risk check", Artifacts: []string{"starter-plan"}, Blackboard: []string{"task_scope", "risk_register"}}},
		BlackboardTemplates: []agentpkg.TeamBlackboardTemplate{
			{Kind: "task_scope", Title: "Task Scope", OwnerRole: "planner", Status: "open", Tags: []string{"scope"}, Description: "Current scope, constraints, and assumptions."},
			{Kind: "risk_register", Title: "Risk Register", OwnerRole: "reviewer", Status: "open", Tags: []string{"risk"}, Description: "Risks that should affect policy routing or approvals."},
		},
		QuorumPresets:  []agentpkg.TeamQuorumPreset{{Name: "default-review", Title: "Default review quorum", Required: 1, Roles: []string{"reviewer"}, RejectBlocks: true, Default: true}},
		OutputContract: []string{"plan", "review_decision", "risk_notes", "final_report"},
	}
	data, err := yaml.Marshal(template)
	if err != nil {
		return ""
	}
	return "# Loaded automatically from templates/teams/*.yaml.\n" + string(data)
}

func renderMaterializedPolicyRuleYAML(names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) string {
	doc := agentpkg.MigrateWorkflowPolicyRuleDefinition(agentpkg.WorkflowPolicyRuleDefinition{
		Name:        names.PolicyRule,
		Label:       firstNonEmptyString(preset.Title, strings.ReplaceAll(names.Kit, "-", " ")+" Kit") + " Gate",
		Description: "Allow the starter workflow to continue only when the planning handoff produced a truthy summary.",
		Operator:    "ref_truthy",
		Reason:      "Planning output must produce a compact summary before downstream reporting.",
		Defaults:    map[string]string{"ref": "stages.plan.outputs.summary"},
		Params:      []agentpkg.WorkflowNodeFieldOption{{Name: "ref", Label: "Plan summary reference", Type: "reference", Required: true}},
	})
	data, err := yaml.Marshal(doc)
	if err != nil {
		return ""
	}
	return "# Loaded automatically from policies/workflow_rules/*.yaml.\n" + string(data)
}

func renderMaterializedWorkflowMarkdown(names cliMaterializedKitNames, preset kitScaffoldPresetDefinition) string {
	return fmt.Sprintf(`# %s

Generated linked starter workflow for %s.

## Linked Resources

- Agent: %s
- Skill: %s
- Tool: %s
- Workflow template: %s
- Team template: %s
- Policy rule: %s

## Next Steps

1. Restart GoFlow so the generated agent and MCP server config are loaded.
2. Open this workflow in Studio and adjust node inputs, routing, and team roles.
3. Switch the helper tool to a write-capable container preset only when mutation is required.
`, firstNonEmptyString(preset.Title, names.Kit), names.Kit, names.Agent, names.Skill, names.ToolServer, names.WorkflowTemplate, names.TeamTemplate, names.PolicyRule)
}

type policyRuleScaffoldPresetDefinition struct {
	Name        string
	Title       string
	Description string
	Operator    string
	Expression  string
	Reason      string
	Defaults    map[string]string
	Params      []agentpkg.WorkflowNodeFieldOption
}

func policyRuleScaffoldPresetByName(name string) (policyRuleScaffoldPresetDefinition, bool) {
	for _, preset := range policyRuleScaffoldPresets() {
		if preset.Name == name {
			return preset, true
		}
	}
	return policyRuleScaffoldPresetDefinition{}, false
}

func policyRuleScaffoldPresetNames() []string {
	presets := policyRuleScaffoldPresets()
	names := make([]string, 0, len(presets))
	for _, preset := range presets {
		names = append(names, preset.Name)
	}
	sort.Strings(names)
	return names
}

func policyRuleScaffoldPresets() []policyRuleScaffoldPresetDefinition {
	return []policyRuleScaffoldPresetDefinition{
		{
			Name:        "risk-threshold",
			Title:       "High Risk Gate",
			Description: "Pass when a referenced risk/severity output is at least the configured threshold.",
			Operator:    "risk_at_least",
			Reason:      "risk threshold was not met",
			Defaults:    map[string]string{"ref": "stages.audit.outputs.risk", "minimum": "high"},
			Params: []agentpkg.WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Evidence reference", Type: "reference", Description: "Reference to a stage output containing low, medium, high, or critical."},
				{Name: "params.minimum", Label: "Minimum severity", Type: "select", Options: []string{"low", "medium", "high", "critical"}},
			},
		},
		{
			Name:        "truthy-reference",
			Title:       "Finding Present Gate",
			Description: "Pass when a referenced workflow output is present and truthy.",
			Operator:    "ref_truthy",
			Reason:      "required reference was empty",
			Defaults:    map[string]string{"ref": "stages.audit.outputs.finding"},
			Params: []agentpkg.WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Reference", Type: "reference", Description: "Reference that must be truthy for the guard to pass."},
			},
		},
		{
			Name:        "contains-text",
			Title:       "Output Contains Gate",
			Description: "Pass when a referenced output contains an expected substring.",
			Operator:    "contains",
			Reason:      "expected text was not found",
			Defaults:    map[string]string{"ref": "stages.review.outputs.summary", "needle": "approved"},
			Params: []agentpkg.WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Reference", Type: "reference"},
				{Name: "params.needle", Label: "Expected text", Type: "text"},
			},
		},
		{
			Name:        "minimum-count",
			Title:       "Minimum Findings Gate",
			Description: "Pass when a referenced list or count reaches a configured minimum.",
			Operator:    "min_count",
			Reason:      "minimum count was not reached",
			Defaults:    map[string]string{"ref": "stages.audit.outputs.findings", "minimum": "1"},
			Params: []agentpkg.WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "List/count reference", Type: "reference"},
				{Name: "params.minimum", Label: "Minimum count", Type: "number"},
			},
		},
		{
			Name:        "team-review-quorum",
			Title:       "Team Review Gate",
			Description: "Pass, block, or pause based on executable team approval/rejection quorum state.",
			Operator:    "team_approval_gate",
			Reason:      "team review quorum was not satisfied",
			Defaults:    map[string]string{"status": "passed", "wait_for_quorum": "true"},
			Params: []agentpkg.WorkflowNodeFieldOption{
				{Name: "params.team", Label: "Team", Type: "text", Description: "Optional team name to filter approval packets."},
				{Name: "params.status", Label: "Required status", Type: "select", Options: []string{"passed", "blocked", "pending"}},
				{Name: "params.approval_quorum", Label: "Approval quorum", Type: "number"},
				{Name: "params.approval_preset", Label: "Quorum preset", Type: "text", Description: "Reusable quorum preset from the selected team template."},
			},
		},
		{
			Name:        "expression",
			Title:       "Custom Expression Gate",
			Description: "Pass when a custom workflow expression evaluates truthy.",
			Operator:    "expression",
			Expression:  "{{params.ref}} == true",
			Reason:      "custom expression did not pass",
			Defaults:    map[string]string{"ref": "stages.audit.outputs.passed"},
			Params: []agentpkg.WorkflowNodeFieldOption{
				{Name: "params.ref", Label: "Reference", Type: "reference"},
			},
		},
	}
}

func renderPolicyRuleYAMLTemplate(name string, preset policyRuleScaffoldPresetDefinition) (string, error) {
	definition := agentpkg.WorkflowPolicyRuleDefinition{
		Name:        name,
		Label:       preset.Title,
		Description: preset.Description,
		Operator:    preset.Operator,
		Expression:  preset.Expression,
		Reason:      preset.Reason,
		Defaults:    copyStringMap(preset.Defaults),
		Params:      append([]agentpkg.WorkflowNodeFieldOption(nil), preset.Params...),
	}
	definition = agentpkg.MigrateWorkflowPolicyRuleDefinition(definition)
	data, err := yaml.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("render policy rule scaffold: %w", err)
	}
	return "# Loaded automatically from policies/workflow_rules/*.yaml.\n" + string(data), nil
}

type teamScaffoldPresetDefinition struct {
	Name                  string
	Title                 string
	Description           string
	Category              string
	Tags                  []string
	BaseTemplate          string
	RecommendedWorkflow   string
	RecommendedEntryAgent string
}

func teamScaffoldPresetByName(name string) (teamScaffoldPresetDefinition, bool) {
	for _, preset := range teamScaffoldPresets() {
		if preset.Name == name {
			return preset, true
		}
	}
	return teamScaffoldPresetDefinition{}, false
}

func teamScaffoldPresetNames() []string {
	presets := teamScaffoldPresets()
	names := make([]string, 0, len(presets))
	for _, preset := range presets {
		names = append(names, preset.Name)
	}
	sort.Strings(names)
	return names
}

func teamScaffoldPresets() []teamScaffoldPresetDefinition {
	return []teamScaffoldPresetDefinition{
		{
			Name:                  "software-review",
			Title:                 "Custom Software Review Team",
			Description:           "Planner, implementer, reviewer, and reporter team for code changes with reusable review quorum.",
			Category:              "software",
			Tags:                  []string{"software", "review", "quality"},
			BaseTemplate:          "software-task-team",
			RecommendedWorkflow:   "plan-fix-audit",
			RecommendedEntryAgent: "planner",
		},
		{
			Name:                  "agent-framework",
			Title:                 "Custom Framework Extension Team",
			Description:           "Product, platform, workflow, safety, and handoff team for creating GoFlow extensions.",
			Category:              "platform",
			Tags:                  []string{"agent-framework", "extension", "kit"},
			BaseTemplate:          "framework-extension-team",
			RecommendedWorkflow:   "agent-framework-extension",
			RecommendedEntryAgent: "planner",
		},
		{
			Name:                  "security-review",
			Title:                 "Custom Security Review Team",
			Description:           "Scope, analysis, audit, and policy-review team for evidence-backed security reviews.",
			Category:              "security",
			Tags:                  []string{"security", "audit", "evidence"},
			BaseTemplate:          "audit-security-team",
			RecommendedWorkflow:   "human-input-security-review",
			RecommendedEntryAgent: "auditor",
		},
		{
			Name:                  "web-research",
			Title:                 "Custom Web Research Team",
			Description:           "Asset collection, JavaScript review, and risk-analysis team for scoped web research.",
			Category:              "security",
			Tags:                  []string{"web", "assets", "risk"},
			BaseTemplate:          "web-research-team",
			RecommendedWorkflow:   "web-research-risk",
			RecommendedEntryAgent: "auditor",
		},
		{
			Name:                  "binary-triage",
			Title:                 "Custom Binary Triage Team",
			Description:           "Static binary triage, reverse-analysis, vulnerability review, and report handoff team.",
			Category:              "security",
			Tags:                  []string{"binary", "reverse", "triage"},
			BaseTemplate:          "binary-triage-team",
			RecommendedWorkflow:   "binary-triage",
			RecommendedEntryAgent: "auditor",
		},
		{
			Name:                  "documentation",
			Title:                 "Custom Documentation Team",
			Description:           "Plan, draft, review, and publication-handoff team for documentation changes.",
			Category:              "documentation",
			Tags:                  []string{"docs", "review", "publish"},
			BaseTemplate:          "documentation-team",
			RecommendedWorkflow:   "docs-review-publish",
			RecommendedEntryAgent: "planner",
		},
		{
			Name:                  "operations-runbook",
			Title:                 "Custom Operations Runbook Team",
			Description:           "Operational planning, runbook authoring, risk review, and operator handoff team.",
			Category:              "operations",
			Tags:                  []string{"ops", "runbook", "approval"},
			BaseTemplate:          "operations-runbook-team",
			RecommendedWorkflow:   "operations-runbook",
			RecommendedEntryAgent: "planner",
		},
		{
			Name:                  "customer-support",
			Title:                 "Custom Customer Support Team",
			Description:           "Triage, context gathering, response drafting, and support-review handoff team.",
			Category:              "support",
			Tags:                  []string{"support", "triage", "response"},
			BaseTemplate:          "customer-support-team",
			RecommendedWorkflow:   "customer-support-triage",
			RecommendedEntryAgent: "chat",
		},
	}
}

func renderTeamTemplateYAMLTemplate(name string, preset teamScaffoldPresetDefinition) (string, error) {
	template, ok := agentpkg.LoadTeamTemplate(preset.BaseTemplate)
	if !ok {
		return "", fmt.Errorf("base team template %q is unavailable", preset.BaseTemplate)
	}
	template.Name = name
	template.Title = firstNonEmptyString(preset.Title, template.Title)
	template.Description = firstNonEmptyString(preset.Description, template.Description)
	template.Category = firstNonEmptyString(preset.Category, template.Category)
	if len(preset.Tags) > 0 {
		template.Tags = append([]string(nil), preset.Tags...)
	}
	template.RecommendedWorkflow = firstNonEmptyString(preset.RecommendedWorkflow, template.RecommendedWorkflow)
	template.RecommendedEntryAgent = firstNonEmptyString(preset.RecommendedEntryAgent, template.RecommendedEntryAgent)
	template.Source = ""
	template.Path = ""
	template.Custom = false
	template.Kind = ""
	template.Version = 0
	template.MinVersion = 0
	template.MigratedFromVersion = 0
	if len(template.QuorumPresets) == 0 {
		template.QuorumPresets = defaultTeamTemplateQuorumPresets(template.RoleTemplates)
	}
	template, err := (&agentpkg.WorkflowRunner{}).ValidateTeamTemplateResource(name, template)
	if err != nil {
		return "", err
	}
	data, err := yaml.Marshal(template)
	if err != nil {
		return "", fmt.Errorf("render team template scaffold: %w", err)
	}
	return "# Loaded automatically from templates/teams/*.yaml.\n" + string(data), nil
}

func defaultTeamTemplateQuorumPresets(roles []agentpkg.TeamRoleTemplate) []agentpkg.TeamQuorumPreset {
	if len(roles) == 0 {
		return nil
	}
	roleNames := make([]string, 0, len(roles))
	for _, role := range roles {
		if strings.TrimSpace(role.Name) != "" {
			roleNames = append(roleNames, role.Name)
		}
	}
	if len(roleNames) == 0 {
		return nil
	}
	required := 1
	if len(roleNames) >= 3 {
		required = 2
	}
	return []agentpkg.TeamQuorumPreset{{
		Name:         "default-review",
		Title:        "Default review quorum",
		Description:  "Reusable review gate preset generated from this team scaffold.",
		Required:     required,
		Roles:        roleNames,
		RejectBlocks: true,
		Default:      true,
	}}
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func writeYAMLStringList(b *strings.Builder, key string, values []string) {
	if b == nil || len(values) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n", key)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		fmt.Fprintf(b, "  - %s\n", value)
	}
}

func firstString(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func renderWorkflowYAMLTemplate(name, templateName string) (string, error) {
	return agentpkg.RenderWorkflowTemplateYAML(name, templateName)
}

func workflowTemplateScaffoldSourceNames() []string {
	rows := (&agentpkg.WorkflowRunner{}).WorkflowTemplates()
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	sort.Strings(names)
	return names
}

func renderWorkflowTemplateResourceYAML(name, source string) (string, error) {
	template, ok := (&agentpkg.WorkflowRunner{}).WorkflowTemplate(source)
	if !ok {
		return "", fmt.Errorf("unknown workflow template: %s", source)
	}
	template.Name = name
	template.Graph.Name = name
	template.Title = firstNonEmptyString(template.Title, name)
	template.Description = firstNonEmptyString(template.Description, fmt.Sprintf("Forked from workflow template %s.", source))
	if strings.TrimSpace(template.Category) == "" {
		template.Category = "custom"
	}
	if !workflowTemplateStringListContains(template.Tags, "fork") {
		template.Tags = append(append([]string(nil), template.Tags...), "fork")
	}
	template.Kind = "goflow.workflow_template_resource"
	template.Version = 2
	template.MinVersion = 1
	template.MigratedFromVersion = 0
	template.Source = ""
	template.Path = ""
	template.Custom = false
	template.Stages = len(template.Graph.Stages)
	if strings.TrimSpace(template.Graph.Description) == "" {
		template.Graph.Description = template.Description
	}
	data, err := yaml.Marshal(template)
	if err != nil {
		return "", fmt.Errorf("render workflow template scaffold: %w", err)
	}
	return "# Loaded automatically from templates/workflows/*.yaml.\n" + string(data), nil
}

func workflowTemplateStringListContains(values []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
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
