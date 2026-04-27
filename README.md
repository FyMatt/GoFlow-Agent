# GoFlow Agent

[English](./README.md) | [简体中文](./README.zh-CN.md)

A local agent runtime in Go for building domain-specific agents with multi-agent workflows, MCP tools, dynamic skills, workspace-scoped session state, and an optional HTTP/SSE surface.

## What it is

GoFlow Agent separates the **runtime home** from the **workspace root** so you can run the framework from its own repo while pointing it at any target folder.

That makes it useful for:
- project bootstrapping in an empty directory
- documentation generation
- tool-augmented agent workflows
- building specialized agents by adding skills and MCP tools
- exposing the same runtime through CLI or HTTP

See [examples/extension-workflow](./examples/extension-workflow) for a complete extension that combines a Python MCP server, a custom read-only agent, a skill, and a graph workflow.

You can smoke-test that packaged example with `python scripts/validate_extension_workflow.py`.

Deployment files can be checked locally without Docker using `python scripts/validate_deployment_assets.py`.

For end-user installation and deployment commands, see [Installation And Deployment](./docs/install.md). For Chinese instructions, see [安装与部署](./docs/install.zh-CN.md).

## Key features

- Multi-agent runtime with named agent profiles
- Registry-backed built-in workflows plus runtime-home custom workflow graphs
- Branded CLI startup banner with runtime/workspace summary
- Best-effort terminal title updates in interactive CLI sessions
- Blocking workflow approvals with arrow-key + Enter selection and numeric fallback
- OpenAI-compatible provider integration with fallback support
- stdio-based MCP tool execution
- Cross-language MCP support with built-in Go and Python servers
- Dynamic skill loading from `SKILL.md`
- Per-workspace session persistence with workflow and pending approval snapshots
- Workspace confirmation gate: pure chat can run from the default directory, while file reads/writes, `@file` references, command execution, and workflows require a confirmed workspace
- Audit logging and CLI trace/status/session output
- External workspace mode via `--workspace`
- Windows, Linux, and Docker deployment paths
- HTTP JSON API and SSE streaming via `--http`

## Quick start

### Prerequisites

- Go 1.25+
- Python 3 on `PATH` if you want the `python_notes` MCP server
- An OpenAI-compatible model endpoint
- Environment variables for provider credentials and model

### Configure environment variables

```bash
export GOFLOW_BASE_URL=http://localhost:11434/v1
export GOFLOW_API_KEY=local-dev-key
export GOFLOW_MODEL=qwen2.5-coder:7b
export GOFLOW_BACKUP_BASE_URL=$GOFLOW_BASE_URL
export GOFLOW_BACKUP_API_KEY=$GOFLOW_API_KEY
export GOFLOW_BACKUP_MODEL=$GOFLOW_MODEL
```

For a ready-to-edit template, copy `.env.example` and fill in your own values.

On Windows, set the key in your shell and use the bootstrap script:

```bat
set GOFLOW_API_KEY=your-deepseek-key
run-goflow.example.cmd
```

You can edit `run-goflow.example.cmd` to point `--workspace` at your own target directory.

The bootstrap script fills in DeepSeek defaults without embedding credentials in the repo.

### Start the CLI

Use the current directory as the workspace:

```bash
go run ./cmd/goflow
```

When no `--workspace` is provided, the CLI treats the current directory as a default workspace candidate. Pure chat can continue immediately. Before any workspace-scoped action such as reading files, writing files, running commands, attaching `@file` references, generating project files, or running workflows, GoFlow asks you to confirm that default with:

```text
/workspace confirm
```

Use an external directory as the workspace:

```bash
go run ./cmd/goflow --workspace D:/my-empty-project
```

At startup the interactive CLI now prints a compact branded banner with runtime/workspace paths plus the currently active agent, current mode, tool policy, allowed tool kinds, explicitly allowed tool names, workflow summary, pending conversational handoff summary, pending approval count, and any remembered per-workspace tool approvals. It also sets the terminal title to `GoFlow Agent - <workspace>` when the terminal supports OSC title updates.

The `/agents` command surfaces each configured agent profile with its mode, tool policy, allowed tool kinds, and `allowed_tools` allowlist so operators can see why a given tool call is or is not permitted before switching agents.

HTTP server mode keeps the plain startup line and does not use the interactive banner.

### Start the HTTP API

```bash
go run ./cmd/goflow --http :8080
```

Or combine it with an external workspace:

```bash
go run ./cmd/goflow --workspace D:/my-empty-project --http :8080
```

At startup the server prints runtime, workspace, and listen address:

```text
GoFlow HTTP API ready. runtime=C:\path\to\agent workspace=D:\my-empty-project addr=:8080
```

Use `Ctrl+C` to stop HTTP mode. GoFlow shuts the server down gracefully and saves
the session snapshot before exiting.

### Run with Docker

For local source checkouts, copy `.env.example` to `.env`, fill in provider values, then run:

```bash
docker compose up --build
```

The container listens on `http://127.0.0.1:8080` and uses `./workspace` as `/workspace` inside the container. See [Deployment](./docs/deployment.md) for Windows, Linux, and Docker details.

To run a published image instead of building locally:

```bash
docker run --rm -it \
  --env-file .env \
  -p 8080:8080 \
  -v "$PWD/workspace:/workspace" \
  ghcr.io/fymatt/goflow-agent:<version>
```

Release archives and Docker images are covered in [Installation And Deployment](./docs/install.md).

## Common CLI commands

CLI commands use the `/` prefix.

```text
/help
/skills
/tools
/agents
/use <agent>
/mode <chat|plan|audit|fix>
/workspace [status|confirm|clear|use <path>]
/workflow plan-fix-audit [--approve] <request>
/workflow skill-chain <request>
/workflow <custom-name> <request>
/status
/session
/trace on|off
/approve <tool-call-id>
/deny <tool-call-id>
/reload
/reload-tools
/skill-templates
/new-skill <template> <name>
/new-tool python <name>
/new-agent <name>
/new-workflow <name>
exit
```

`/workspace status` shows whether the active workspace is explicit or still the current-directory default. `/workspace confirm` enables workspace-scoped tools for the current session. `/workspace clear` removes that confirmation. `/workspace use <path>` validates the requested path and tells you to restart with `--workspace <path>` when switching would require rebinding MCP servers.

`--approve` pre-approves the planner -> fixer stage transition. Without it, GoFlow first asks whether to enter the fixer stage, then separately prompts again if a fixer tool call requires confirmation.

`/new-workflow <name>` creates `workflows/<name>/workflow.yaml`. That file is executable through `/workflow <name> <request>` and can declare named stages with `agent`, `skill`, optional `approval`, optional `next_strategy`, and optional `next` edges.

Running `/workflow` without arguments prints usage plus discovered custom workflows.

## Workflow approvals

When `/workflow plan-fix-audit ...` pauses, the CLI can stop at two different approval boundaries:
- stage approval before entering the fixer stage
- tool approval for a suspended fixer tool call

Both approval steps are blocking. If both happen in one workflow run, GoFlow stays inside the approval flow until each decision is resolved.

If the operator chooses the workflow-level "approve all pending tool calls" path, later matching tool calls in the same workflow run stay auto-approved only within the same workflow/stage/tool scope. The scope does not leak across different workflows, stages, or tools.

In interactive terminals, the prompt uses an arrow-key selector:
- `Up` / `Down` move between approval actions
- `Enter` confirms the highlighted action
- `1`, `2`, and `3` still work as direct shortcuts

Available choices:
- approve the current tool call
- deny the current tool call
- approve all currently pending tool calls for this workflow run

When stdin/stdout are not interactive terminals, GoFlow falls back to the numeric `1/2/3` prompt so redirected sessions and simpler terminals still work.

While the workflow is paused:
- `/status` highlights the current workflow state, next stage, and each pending approval with tool, agent, stage, and argument summary
- `/session` shows structured `Workflow` and `Pending approvals` sections from the persisted session snapshot
- the latest workflow snapshot stores the current request, summary, next stage, and latest approval prompt/result

The current workflow state and pending approvals are also persisted into the session snapshot so operator-facing workflow state survives normal CLI inspection.

When a suspended fixer tool call is approved and the workflow resumes, GoFlow now replays the original suspended assistant/tool-call context together with the approved tool result back into the fixer conversation loop instead of restarting from a fresh prompt.

## Ordinary chat routing and plan confirmation

The shipped config now starts ordinary CLI sessions on the default `chat` agent instead of dropping directly into `planner` mode.

For plain-language requests, the runtime can reroute into planner/fixer/auditor roles when the request clearly asks for planning, implementation, or review. The runtime remains the final authority: workflow state, pending approvals, and other lifecycle constraints can still prevent an unsafe switch.

If ordinary chat first produces a plan and is waiting for approval to execute it, GoFlow records a pending conversational handoff in the session snapshot. Natural replies such as `可以`, `继续`, `就按这个执行`, `全部添加`, `yes`, or `go ahead` confirm the handoff, switch to the target executable agent, and continue from the approved plan instead of looping back into more planner output.

Esc cancellation is different from a pending handoff. Pressing Esc twice cancels
the active turn; a later standalone `继续`, `重试`, or `continue` re-runs the
cancelled request from the beginning with a clear retry log.

While that handoff is pending:
- the startup banner can show a pending handoff summary on restart
- `/session` includes a structured `Pending handoff` section
- normal write-capable work still follows the usual tool approval flow once execution begins

## HTTP API

Current endpoints:
- `GET /api/workspace`
- `POST /api/workspace/confirm`
- `POST /api/workspace/clear`
- `POST /api/workspace/select`
- `POST /api/run`
- `POST /api/run/stream`
- `GET /api/session`
- `POST /api/workflows/{name}`
- `POST /api/workflows/{name}/stream`
- `GET /api/workflow-graphs`
- `POST /api/workflow-graphs`
- `GET /api/workflow-graphs/{name}`
- `PUT /api/workflow-graphs/{name}`
- `DELETE /api/workflow-graphs/{name}`
- `GET /api/workflow-options`
- `POST /api/approvals/{callID}/approve`
- `POST /api/approvals/{callID}/approve/stream`
- `POST /api/approvals/{callID}/deny`
- `POST /api/approvals/{callID}/deny/stream`
- `POST /api/approvals/approve-all`

HTTP mode also serves a basic workspace page at `GET /workspace`. It shows the
current workspace root, whether it is confirmed, and actions for confirm, clear,
or selecting another path. Selecting a different path returns a restart-required
response so MCP servers are rebound safely under the new workspace root.

### Example: final JSON response

```bash
curl -s -X POST http://127.0.0.1:8080/api/run \
  -H 'Content-Type: application/json' \
  -d '{"input":"summarize this workspace"}'
```

### Example: SSE stream

```bash
curl -N -X POST http://127.0.0.1:8080/api/run/stream \
  -H 'Content-Type: application/json' \
  -d '{"input":"hello"}'
```

The SSE endpoint forwards runtime `schema.StreamEvent` values directly, using the event type as the SSE `event:` field and the JSON-encoded event as the `data:` field.

SSE clients should handle these event types:

- `text`, `status`, `done`, and `final_message` for model output and turn completion
- `task_stage` for high-level stage changes such as inspect, plan, modify, verify, and summarize
- `tool_call`, `tool_result`, and `approval` for tool execution and confirmation pauses
- `token_usage` for provider-reported prompt/output/cache token counts
- `workflow_result` for final or paused workflow state from `/api/workflows/{name}/stream` and streamed approval resumes

The streamed approval endpoints resume ordinary chat or workflow execution when the approved or denied call has resumable context.

HTTP mode also serves the embedded Agent Studio at `GET /console` and
`GET /workflows`. The Studio is a modern visual workflow surface with a
workflow list, node palette, draggable stage canvas, property panel, run
preview, approvals, resource catalog, workspace controls, observability, and
settings/update guidance. The Playground can choose a specific agent before a
run, the resource catalog includes a validated Skill editor, and the main UI supports English and Chinese. Custom workflow graphs are persisted under
`workflows/<name>/workflow.yaml`. See [Workflow Graphs](./docs/workflows.md).

## Built-in MCP tools

- `file_tools/list_dir`
- `file_tools/list_tree`
- `file_tools/file_info`
- `file_tools/read_file`
- `file_tools/search_files`
- `file_tools/write_file`
- `file_tools/delete_file`
- `web_tools/web_search`
- `web_tools/fetch_url`
- `web_tools/fetch_page_assets`
- `python_notes/read_note`
- `python_notes/write_note`
- `python_notes/python_ast_summary`
- `python_notes/json_query`
- `python_notes/binary_file_info`
- `python_notes/binary_strings`
- `python_notes/hex_preview`

File, note, source-inspection, and binary-inspection tools resolve every path under the active workspace root and reject path traversal or symlink escapes. If the workspace was only defaulted from the current directory, GoFlow hides workspace-scoped read/write/exec tools from the model until you confirm it. Write operations render git-like CLI summaries with status letters, added/deleted line counts, changed line ranges, byte counts, and compact colored diff hunks. In the interactive CLI, type `@` to show workspace file suggestions, then type a prefix and press `Tab` to complete a unique match or show candidates. You can reference workspace files inline with `@relative/path.ext`; GoFlow reads the file through the normal `file_tools/read_file` path, prints the same tool logs, and attaches the file content before the request reaches the agent. In non-interactive input, type `@prefix` as a full line to list matching references.

Interactive CLI input also supports `Tab` completion for `/` commands and consumes arrow/Home/End/Delete key sequences so those keys do not appear as literal escape text in ordinary prompts.

## Example use cases

### Inspect a project

```text
Please read the README and summarize the current project structure.
```

### Run the staged workflow

```text
/workflow plan-fix-audit improve workflow error handling
```

### Generate files in an empty workspace

```bash
go run ./cmd/goflow --workspace D:/scratch-project
```

Then ask:

```text
Please generate an initial README and docs/overview.md for this empty project.
```

## Documentation

- [Architecture](./docs/architecture.md)
- [Configuration](./docs/configuration.md)
- [Deployment](./docs/deployment.md)
- [MCP Integration](./docs/mcp.md)
- [MCP Isolation Strategy](./docs/mcp-isolation.md)
- [MCP Tool Authoring](./docs/mcp-authoring.md)
- [Scaffold Commands](./docs/scaffolds.md)
- [Skills](./docs/skills.md)
- [Skill Authoring](./docs/skill-authoring.md)
- [Workflow Graphs](./docs/workflows.md)
- [Updates](./docs/updates.md)

## Status

The framework is working end-to-end, including:
- external workspace execution
- Windows, Linux, and Docker deployment paths
- built-in Go and Python MCP servers
- registry-backed workflow execution and approval-aware session state
- CLI status and session inspection
- HTTP JSON and SSE runtime transport
- safer Windows bootstrap without embedded credentials

