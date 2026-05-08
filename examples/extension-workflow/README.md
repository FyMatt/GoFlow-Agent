# End-To-End Extension Example

[English](./README.md) | [简体中文](./README.zh-CN.md)

This example shows how to combine a custom Python MCP server, a custom agent, a custom skill, and a graph workflow.

It is intentionally not enabled by default. Copy the parts you want into the runtime home, review permissions, then reload discovery from the CLI.

## Files

```text
examples/extension-workflow/
  mcp_servers/workspace_report.py
  configs/agents/workspace-reporter.yaml
  configs/mcp_servers/workspace_report.yaml
  skills/workspace-report/SKILL.md
  workflows/workspace-review/workflow.yaml
```

## Install Into A Runtime Home

From the GoFlow repo root:

```powershell
Copy-Item examples/extension-workflow/mcp_servers/workspace_report.py mcp_servers/workspace_report.py
Copy-Item -Recurse examples/extension-workflow/skills/workspace-report skills/workspace-report
Copy-Item -Recurse examples/extension-workflow/workflows/workspace-review workflows/workspace-review
Copy-Item examples/extension-workflow/configs/agents/workspace-reporter.yaml configs/agents/workspace-reporter.yaml
Copy-Item examples/extension-workflow/configs/mcp_servers/workspace_report.yaml configs/mcp_servers/workspace_report.yaml
```

The custom agent is already installed as a modular runtime profile at
`configs/agents/workspace-reporter.yaml`. Review that file before restarting
GoFlow.

The custom MCP server is installed as a modular runtime profile at
`configs/mcp_servers/workspace_report.yaml`. Review that file before
restarting GoFlow.

## Verify Discovery

Before copying the example into your runtime home, smoke-test the packaged files:

```bash
python scripts/validate_extension_workflow.py
```

Then reload discovery after copying:

```text
/reload
/reload-tools
/skills
/tools
/agents
```

Expected names:

- skill: `workspace-report`
- tool: `workspace_report/workspace_summary`
- agent: `workspace-reporter`
- workflow: `workspace-review`

## Run The Workflow

```text
/workflow workspace-review summarize this repository structure and identify risky large files
```

The workflow has two stages:

1. `inventory`: runs `workspace-reporter` with `workspace-report` and the custom read-only MCP tool.
2. `audit`: runs the built-in `auditor` with `code-audit` to review risks from the inventory.

## Why This Pattern

- The custom MCP server owns one narrow capability.
- The custom agent has only the read tools needed for that capability.
- The skill defines the output contract for the LLM.
- The workflow composes the custom stage with a built-in review stage.
