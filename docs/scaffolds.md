# Scaffold Commands

GoFlow includes small generators for common second-development tasks. They are intentionally conservative: generated files are examples that keep workspace boundaries visible and require the operator to choose when to expose new permissions.

## Commands

```text
/skill-templates
/new-skill <template> <name>
/new-tool python <name>
/new-agent <name>
/new-workflow <name>
```

Generated paths are relative to the runtime home, not the active workspace:

- `skills/<name>/SKILL.md`
- `mcp_servers/<name>.py`
- `configs/agents/<name>.yaml`
- `workflows/<name>/workflow.yaml`
- `workflows/<name>/WORKFLOW.md`

The active workspace remains the root for file tools and `@file` references.

## Skill Scaffold

List available templates:

```text
/skill-templates
```

Create a skill:

```text
/new-skill code-audit dependency-audit
```

The generated `SKILL.md` includes:

- metadata front matter
- tool declarations
- activation keywords
- preferred mode and agent
- output kind
- an example request
- extension notes for keywords, tools, agent boundary, and follow-up skills

After editing:

```text
/reload
/skills
```

Check that the skill appears with the expected mode, agent, output kind, and activation hints.

## Python MCP Tool Scaffold

Create a Python MCP server:

```text
/new-tool python workspace-helper
```

The generated server includes:

- stdio JSON-RPC loop
- `initialize`
- `tools/list`
- `tools/call`
- strict object schemas with `additionalProperties: false`
- `GOFLOW_WORKSPACE_ROOT` path scoping
- sample `ping`, `read_text`, and `write_text` tools

Add it to `configs/agent.yaml`:

```yaml
mcp_servers:
  - name: workspace_helper
    command: python
    args:
      - ./mcp_servers/workspace-helper.py
    enabled: true
    timeout: 30s
    workdir: .
    env_allowlist: [PATH, HOME, USERPROFILE, TMP, TEMP]
    isolation: process_group
    allowed_commands:
      - python
    max_request_bytes: 65536
    max_response_bytes: 2097152
```

Then run:

```text
/reload-tools
/tools
```

If two servers expose the same short tool name, use the qualified name such as `workspace_helper/read_text`.

`isolation: process_group` is optional but recommended for third-party servers. It improves process lifecycle boundaries where the host OS supports separate process groups. It does not replace workspace path checks, command allowlists, tool policy, or network-aware agent permissions.

## Agent Scaffold

Create an agent config snippet:

```text
/new-agent researcher
```

The generated file is not automatically merged into `configs/agent.yaml`; copy or merge it under the top-level `agents:` map after reviewing permissions.

Start narrow:

```yaml
allowed_tool_kinds: [read]
```

When adding network or write access, prefer an exact allowlist:

```yaml
allowed_tool_kinds: [read, network]
allowed_tools: [web_tools/fetch_url, web_tools/web_search]
```

Run:

```text
/agents
```

Confirm the new agent displays the expected mode, policy, tool kinds, and allowlist.

## Workflow Scaffold

Create an executable workflow graph:

```text
/new-workflow release-check
```

The generated workflow starts with:

- `triage` through `planner` and `execution-plan`
- optional branch to `implement` or `audit`
- `implement` through `fixer` and `code-writing` with `approval: true`
- `audit` through `auditor` and `code-audit`

Run it:

```text
/workflow release-check review the auth changes
```

Rules to keep:

- each stage names one configured agent
- each stage names one loaded skill
- write or exec stages should normally have `approval: true`
- `next` can only point to declared stage names
- agent permissions remain the final tool boundary

## Verification Checklist

1. Run the relevant reload command: `/reload` or `/reload-tools`.
2. Inspect discovery output: `/skills`, `/tools`, or `/agents`.
3. Confirm no schema warnings or unknown references appear.
4. Run a small request before using the scaffold in a larger workflow.
5. Check `/status` and `/session` when a workflow or approval pauses.

## Complete Extension Example

See `examples/extension-workflow` for a copyable example that combines:

- a Python MCP server under `mcp_servers/`
- a narrow read-only agent snippet under `configs/agents/`
- a custom skill under `skills/`
- a graph workflow under `workflows/`

Use it when you want to verify the full second-development path end to end instead of testing each scaffold in isolation.

Run the packaged smoke test before copying the example into a runtime home:

```bash
python scripts/validate_extension_workflow.py
```

