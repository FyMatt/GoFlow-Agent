# Binary Analysis Kit Example

[English](./README.md) | [简体中文](./README.zh-CN.md)

This example is a copyable materialized kit for binary triage. It shows how a
kit can connect an Agent, Skill, Docker/Podman-isolated MCP helper, Workflow,
Workflow Template, Team Template, and Policy Rule.

It is intentionally not enabled by default. Copy only the parts you want into a
runtime home, review permissions, set the tool path, then restart or reload
GoFlow.

## Files

```text
examples/binary-analysis-kit/
  configs/agents/binary-analysis-kit-agent.yaml
  configs/mcp_servers/binary-analysis-kit-helper.yaml
  kits/binary-analysis-kit/kit.yaml
  mcp_servers/binary-analysis-kit-helper.py
  policies/workflow_rules/binary-analysis-kit-gate.yaml
  skills/binary-analysis-kit-skill/SKILL.md
  templates/teams/binary-analysis-kit-team.yaml
  templates/workflows/binary-analysis-kit-template.yaml
  workflows/binary-analysis-kit-workflow/workflow.yaml
```

## Install Into A Runtime Home

From the GoFlow repo root:

```powershell
Copy-Item examples/binary-analysis-kit/mcp_servers/binary-analysis-kit-helper.py mcp_servers/binary-analysis-kit-helper.py
Copy-Item -Recurse examples/binary-analysis-kit/skills/binary-analysis-kit-skill skills/binary-analysis-kit-skill
Copy-Item -Recurse examples/binary-analysis-kit/workflows/binary-analysis-kit-workflow workflows/binary-analysis-kit-workflow
Copy-Item -Recurse examples/binary-analysis-kit/kits/binary-analysis-kit kits/binary-analysis-kit
Copy-Item examples/binary-analysis-kit/configs/agents/binary-analysis-kit-agent.yaml configs/agents/binary-analysis-kit-agent.yaml
Copy-Item examples/binary-analysis-kit/configs/mcp_servers/binary-analysis-kit-helper.yaml configs/mcp_servers/binary-analysis-kit-helper.yaml
Copy-Item examples/binary-analysis-kit/policies/workflow_rules/binary-analysis-kit-gate.yaml policies/workflow_rules/binary-analysis-kit-gate.yaml
Copy-Item examples/binary-analysis-kit/templates/teams/binary-analysis-kit-team.yaml templates/teams/binary-analysis-kit-team.yaml
Copy-Item examples/binary-analysis-kit/templates/workflows/binary-analysis-kit-template.yaml templates/workflows/binary-analysis-kit-template.yaml
```

The MCP config uses a container single-tool bind mount. Set the absolute helper
path before startup:

```powershell
setx GOFLOW_BINARY_ANALYSIS_KIT_TOOL_SOURCE "C:\path\to\goflow\mcp_servers\binary-analysis-kit-helper.py"
```

For Linux/macOS shells:

```bash
export GOFLOW_BINARY_ANALYSIS_KIT_TOOL_SOURCE=/opt/goflow/mcp_servers/binary-analysis-kit-helper.py
```

The helper defaults to `isolation: container`, read-only workspace access,
disabled network, non-root user, resource limits, read-only rootfs, dropped
capabilities, and tmpfs scratch mounts. Keep that boundary unless the tool
really needs more access.

## Verify Discovery

Restart GoFlow, or reload what can be reloaded from the CLI:

```text
/reload
/reload-tools
/agents
/skills
/tools
/workflow-templates
/teams
/kits
```

Expected names:

- agent: `binary-analysis-kit-agent`
- skill: `binary-analysis-kit-skill`
- tools:
  - `binary-analysis-kit-helper/binary_file_info`
  - `binary-analysis-kit-helper/binary_strings`
  - `binary-analysis-kit-helper/hex_preview`
  - `binary-analysis-kit-helper/read_text`
- workflow: `binary-analysis-kit-workflow`
- workflow template: `binary-analysis-kit-template`
- team template: `binary-analysis-kit-team`
- policy rule: `binary-analysis-kit-gate`
- kit: `binary-analysis-kit`

## Run The Workflow

```text
/workflow binary-analysis-kit-workflow inspect sample.bin and report likely attack surfaces
```

Pass the binary path as plain text. Do not use `@sample.bin` here: `@file`
references are for UTF-8 text content, while raw binary artifacts should be
opened by binary inspection tools.

The example workflow passes outputs between nodes:

1. `team` adds reusable collaboration context.
2. `plan` uses `binary-analysis-kit-skill`, calls the binary helper tools, and
   emits `summary`, `plan`, and `findings`.
3. `gate` applies `binary-analysis-kit-gate` to the planning summary.
4. `report` builds a final handoff when the gate passes.
5. `revise` produces a correction path when the gate denies.

For the built-in lightweight path, use the packaged `binary-analysis-kit` under
`kits/` together with the built-in `binary-triage` template and
`binary-vulnerability-research` skill. This example is the fuller materialized
version for studying how resources connect.

Validate the packaged example from the repo root:

```bash
python scripts/validate_binary_analysis_kit.py
```
