# Architecture

## Design principles

GoFlow Agent is built around a few core rules:

1. Keep the runtime centered on small, explicit primitives.
2. Separate agent reasoning, tool execution, session state, and audit logging.
3. Make tool policy visible and enforceable.
4. Keep operator-facing behavior inspectable through the CLI.
5. Separate **runtime home** from **workspace root**.
6. Reuse shared schemas across CLI, HTTP, and future transports instead of forking runtime behavior.

## Runtime home vs workspace root

This is now a first-class architectural concept.

- **runtime home**: the GoFlow project itself, which provides `configs/agent.yaml`, `skills/`, and built-in MCP server code under `mcp_servers/`
- **workspace root**: the target working directory where tools read/write files and where session state is stored

This separation allows GoFlow to be launched against an arbitrary empty external folder while still loading its own runtime assets from the framework repository.

## Main modules

### `cmd/goflow`

CLI entrypoint.

Key responsibilities:
- resolve runtime home
- parse `--workspace`
- bootstrap the shared runtime app from `internal/app`
- render streaming events in the terminal
- render the branded startup banner in interactive CLI mode
- set the terminal title when supported
- own blocking approval input, including interactive selector and numeric fallback

### `internal/app`

Shared bootstrap layer used by the CLI, HTTP server mode, and future transports.

Key responsibilities:
- load runtime config from runtime home while binding workspace root
- initialize skill manager, MCP manager, provider registry, session state, and audit logger
- construct a reusable `agent.Runtime`
- return transport-neutral app state such as config path, workspace root, and runtime handles

### `internal/agent`

Owns the request lifecycle and multi-agent coordination.

Key responsibilities:
- select the active agent profile
- match a skill
- build the system prompt
- run the LLM/tool loop
- emit streaming events
- coordinate staged workflows

Important files:
- `internal/agent/agent.go` - core request loop
- `internal/agent/runtime.go` - runtime coordination for active agent, mode, audit, and session
- `internal/agent/executor.go` - tool execution path
- `internal/agent/execution.go` - policy enforcement and approval signaling
- `internal/agent/workflow.go` - built-in planner/fixer/auditor workflow
- `internal/agent/workflow_graph.go` - runtime-home workflow graph loading and execution

### `internal/skill`

Loads `SKILL.md` files from disk, validates frontmatter, and performs keyword-based matching.

Skill discovery is resolved from runtime home, not from the external workspace.

### `internal/mcp`

Manages stdio-based MCP servers, cached tool discovery, tool routing, restart/cooldown state, and server health reporting.

The MCP layer now launches built-in servers from runtime-home-relative paths while passing the active workspace through `GOFLOW_WORKSPACE_ROOT`.

### `internal/llm`

Provides the provider registry and OpenAI-compatible chat client used by each configured agent.

### `internal/session`

Stores bounded session context and persists it under the current workspace.

Tracked state includes:
- active agent
- mode
- last matched skill
- recent prompts
- recent tool summaries
- current workflow snapshot
- pending approval summaries
- pending ordinary-chat handoff state for plan -> execution confirmation

### `internal/runtime`

Holds shared runtime concerns such as audit logging.

### `pkg/schema`

Defines the provider-neutral request/result/event contract used by the runtime, CLI renderer, HTTP JSON endpoints, SSE streaming, and future transports.

## Request lifecycle

1. CLI receives input
2. CLI resolves runtime home and workspace root
3. Config is loaded from runtime home
4. Runtime-relative paths are normalized for skills and MCP startup
5. Workspace-relative paths are normalized for session persistence and file sandboxing
6. Runtime chooses the active agent profile
7. Skill manager selects a matching skill
8. Runtime lists available MCP tools
9. System prompt is built from agent profile, matched skill, and session snapshot
10. Provider streams model output as text and/or tool calls
11. Executor validates policy and arguments, then either:
    - executes the tool
    - blocks it
    - emits approval-required behavior
12. Tool results are fed back into the model loop until completion
13. Final result includes output, tool results, structured sections, and audit entries

## Workflow lifecycle

Workflow execution is registry-backed rather than hard-coded at the transport boundary.

The built-in `plan-fix-audit` and `skill-chain` workflows are registered once and then invoked through the shared workflow runner from both CLI and HTTP paths. If a workflow name is not built in, the runner attempts to load `workflows/<name>/workflow.yaml` from runtime home and execute it as a graph-backed workflow.

HTTP streaming endpoints forward the same `schema.StreamEvent` objects used by the CLI renderer. `/api/run/stream` streams normal agent turns, `/api/workflows/<name>/stream` streams workflow stage execution and ends with a `workflow_result` event, and streamed approval endpoints can resume ordinary or workflow tool loops while preserving token, task-stage, tool-result, and approval events.

`plan-fix-audit` runs three named agents in order:

1. `planner`
2. `fixer`
3. `auditor`

Behavior:
- planner produces an implementation plan
- fixer runs only when the workflow is invoked with `--approve`
- write-policy tool calls can suspend the fix stage and surface a blocking approval prompt in the CLI
- in interactive terminals, the CLI approval layer uses arrow-key selection with Enter confirmation
- the operator can approve one call, deny one call, or approve all currently pending calls for the workflow run
- workflow-level approve-all now stays narrow: later matching calls auto-approve only within the same workflow/stage/tool scope
- non-interactive sessions fall back to the numeric `1/2/3` approval prompt
- workflow status and pending approval summaries are persisted into session state for later inspection
- `/status` surfaces the current workflow snapshot plus approval details

`skill-chain` starts from the best matching skill, follows declared `next_skills`, and chooses an agent for each stage from `preferred_agent` or the skill mode. Skill metadata never expands permissions; the selected agent profile remains the final tool-policy boundary.
- Custom workflow graphs define explicit stage names, agents, skills, optional stage approval, optional next-stage lists, and optional branch selection strategy.
- Graph stages run with the configured skill instructions while the selected agent profile remains the final permission boundary.
- Graph workflows support sequential execution, declared next-stage edges, explicit stage selection through `Next skill: ...` / `Next skills: ...`, and suspended tool approval/resume.
- Custom workflow files live in runtime home, while any file tools used by stages still operate against the active workspace root.
- For graph schema and examples, see `docs/workflows.md`.
- `/session` renders persisted workflow, pending approvals, pending handoff, and last-routing sections for operator inspection
- ordinary chat can also persist a pending plan -> execution handoff so natural-language confirmations resume into the target executable agent instead of restarting planner output
- when a suspended fixer tool call is approved, resume replays the original assistant/tool-call context plus the approved tool result back into the fixer loop
- auditor reviews the completed implementation
- the original active agent is restored after workflow execution
- bounded workflow summaries are added to session history

## Built-in MCP servers

### `file_tools` (Go)

Provides:
- `read_file`
- `write_file`
- `list_dir`
- `list_tree`
- `file_info`
- `search_files`
- `delete_file`

Behavior:
- resolves paths inside the current workspace only
- rejects path escape attempts
- enforces file size and request/response limits

### `python_notes` (Python)

Provides:
- `write_note`
- `read_note`
- `python_ast_summary`
- `json_query`
- `binary_file_info`
- `binary_strings`
- `hex_preview`

Purpose:
- demonstrate cross-language MCP implementation
- prove that MCP servers do not need to be written in Go

## Streaming model

Runtime output is incremental. `schema.StreamEvent` currently supports:

- `text`
- `status`
- `tool_call`
- `tool_result`
- `error`
- `done`
- `final_message`
- `approval`

The CLI renderer consumes these events directly.

The HTTP transport reuses the same event model through Server-Sent Events (SSE):
- `POST /api/run/stream` emits one SSE frame per runtime stream event
- the SSE `event:` field is the stream event type
- the SSE `data:` field is the JSON-encoded `schema.StreamEvent` payload

This keeps CLI and HTTP streaming on the same runtime contract instead of inventing a second event schema.

## Current limitations

- skill matching is still keyword-based for skill discovery, although the latest match score and reason are now exposed through runtime status
- MCP transport is still stdio only
- ordinary-chat routing is still a narrow slice rather than a fully general planner/fixer/auditor intent system
- real provider verification still depends on valid external credentials

## Intended evolution

Near-term expansion should preserve the current runtime contracts:
- make workflow registration easier to extend beyond the built-in workflows
- add richer workflow state and resumable approval semantics where longer workflows need them
- keep CLI, HTTP JSON, and SSE paths on the same runtime implementation
- strengthen verifier, reflection, and retry patterns around the existing tool loop
- add more MCP examples in multiple languages

