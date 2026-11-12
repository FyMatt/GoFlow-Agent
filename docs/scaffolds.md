# Scaffolds

Scaffolds create a small, connected starting point. They are not a second product surface and do not expose native sandbox internals.

## Tool Scaffold

The built-in `python-local` preset creates:

- `mcp_servers/<name>.py`
- `configs/mcp_servers/<name>.yaml`

The connection uses `process_group`, fixed command allowlists, `GOFLOW_WORKSPACE_ROOT` checks, traversal rejection, bounded reads, and structured MCP errors. Third-party tools should be switched to `auto` after review so the runtime can require `native_strong`.

```text
GET /api/resources/tools/scaffolds
POST /api/resources/tools/scaffolds/python-local
```

Ordinary inputs are the Chinese name, Chinese description, and whether an existing resource may be replaced. Runtime image, mount, namespace, and low-level security forms do not exist in V3.

## Agent And Workflow Scaffolds

Agent scaffolds create a Chinese profile with a narrow tool list. Workflow scaffolds create a readable graph with explicit inputs, outputs, policy gates, and delivery validation. Keep workflows small: simple tasks run directly; complex tasks use only the stages needed to produce and verify artifacts.

The `complex-project-delivery` scaffold demonstrates unified verification and a bounded `context.max_tokens` budget. The `agent-framework-extension` scaffold supports extension materialization; generated bundles include `activation_steps` and remain under the selected runtime home.

## Materialized Kit

Materialization creates one connected set:

- Kit metadata
- Agent or subagent
- Skill
- local MCP helper
- Workflow and workflow template
- team and policy resources used internally by the workflow

Generated resources require review before activation. The helper uses the native process boundary and task workspace guards. Write permission is added only when the workflow actually needs it.

## Quality Rules

- Names and descriptions shown to ordinary users are Chinese.
- Generated commands use exact allowlists.
- Generated workflows declare artifacts and quality gates.
- No scaffold embeds an API Key, SSH private key, or user secret.
- A generated resource is useful only after a real run proves its outputs.

See [resources](./resources.md), [configuration](./configuration.md), and [MCP](./mcp.md).
