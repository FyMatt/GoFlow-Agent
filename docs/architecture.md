# Architecture

[English](./architecture.md) | [简体中文](./architecture.zh-CN.md)

## Design principles

GoFlow Agent is built around a few core rules:

1. Keep the runtime centered on small, explicit primitives.
2. Separate agent reasoning, tool execution, session state, and audit logging.
3. Make tool policy visible and enforceable.
4. Keep operator-facing behavior inspectable through the CLI.
5. Separate **runtime home** from **workspace root**.
6. Reuse shared schemas across CLI, HTTP, and future transports instead of forking runtime behavior.
7. Treat Web Studio as the visual implementation of CLI operations. New CLI capabilities should normally ship with matching HTTP/API coverage, browser UI affordances, and parity tests unless there is a documented reason they must remain terminal-only.
8. Read workspace text files as UTF-8. UTF-8 BOM is acceptable and should be stripped; non-UTF-8 bytes should route to binary-inspection tools instead of text readers.

## Runtime home vs workspace root

This is now a first-class architectural concept.

- **runtime home**: the GoFlow project itself, which provides `configs/goflow.yaml`, `skills/`, and built-in MCP server code under `mcp_servers/`
- **workspace root**: the target working directory where tools read/write files and where session state is stored

This separation allows GoFlow to be launched against an arbitrary empty external folder while still loading its own runtime assets from the framework repository.

## Main modules

### `cmd/goflow`

CLI entrypoint.

Key responsibilities:
- resolve runtime home
- parse `--workspace` and track whether the active workspace was explicit or only defaulted from the current directory
- bootstrap the shared runtime app from `internal/app`
- render streaming events in the terminal
- render the branded startup banner in interactive CLI mode
- set the terminal title when supported
- require `/workspace confirm` before workspace-scoped actions when the workspace was only defaulted
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
- hide workspace-scoped read/write/exec tools when the transport marks the workspace as unconfirmed
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

The built-in `plan-fix-audit` and `skill-chain` workflows are registered once and then invoked through the shared workflow runner from both CLI and HTTP paths. Built-in workflow templates are embedded from `internal/agent/templates/workflows/*.yaml`; runtime-home overrides under `templates/workflows/*.yaml` can add or replace reusable blueprints. If a workflow name is not built in, the runner attempts to load `workflows/<name>/workflow.yaml` from runtime home and execute it as a graph-backed workflow.

The runtime intentionally distinguishes editable graph resources from runnable
workflow entries. `/api/workflow-graphs` exposes only persisted graph files for
editing. `/api/workflow-options.workflow_executors` exposes the runnable set:
valid graph workflows plus legacy compatibility executors. A valid graph saved
with the same name as a legacy executor overrides that executor; an invalid
same-name graph blocks the compatibility executor and reports the graph error so
Studio can prompt the operator to fix the file.

HTTP streaming endpoints forward the same `schema.StreamEvent` objects used by the CLI renderer. `/api/run/stream` streams normal agent turns, `/api/workflows/<name>/stream` streams workflow stage execution and ends with a `workflow_result` event, and streamed approval endpoints can resume ordinary or workflow tool loops while preserving prompt-budget, token, task-stage, tool-result, and approval events.

For browser Studio usage, ordinary Agent turns can also run as durable
background runs: `POST /api/run` with `"background": true` creates a persisted
`agent_runs` snapshot, returns a `run_id` immediately, and executes under a
server-owned context rather than the browser request context. Clients reconnect
through `/api/agent-runs/{id}/events/stream?since=<seq>` or `Last-Event-ID`;
events are stored in the session snapshot with the same task-stage, token,
tool, approval, error, and final-message data used by the foreground stream.
Cancellation is explicit through `/api/agent-runs/{id}/cancel`. The ordinary
run resource also exposes `/events`, `/timeline`, `/replay`, `/diffs`, and
`/actions`, plus `/export?format=json|md`, so Studio can render chat history,
reconnectable progress, file-change diffs, and downloadable evidence reports
without parsing a live request body. Tool approvals are bound back to the
originating ordinary run as
`pending_approvals`, and run actions can approve/deny one or all pending tools.
The suspended ordinary tool-loop context is stored in the run snapshot, so a
process that reloads the same session can rebuild the pending approval state
and continue the turn after approval. If an old or damaged run lacks that resume
context, action discovery reports approval actions as unavailable so Studio can
steer the operator to retry or cancel.

Before each provider call, the agent runner emits a `prompt_budget` event with
an approximate token breakdown for system prompt, messages, tool schemas, and
matched skill context. The same call path filters model-visible tool schemas by
agent policy and matched skill declarations, while the executor keeps enforcing
the policy if the model guesses a hidden tool. The budget event also carries
stable hashes for the system prompt, visible tool schemas, skill context, and
combined prompt prefix, plus an estimated cacheable-prefix token count. These
fields let CLI/HTTP consumers reason about provider prompt-cache behavior
without GoFlow storing provider KV tensors. Large tool results are kept in full
for logs/replay/result objects, but the prompt observation sent back to the next
LLM call is compacted with head/tail context and original-size metadata once it
crosses the large-output threshold.
Those large observations are also stored as bounded session artifacts and
assigned `goflow://session-artifacts/<id>` refs. CLI and HTTP request expansion
can rehydrate those refs back into prompt context on demand, while
`/api/session-artifacts` exposes list/detail access for Studio panels.

Session prompt/tool history remains stored in session state, but system prompt
construction only carries a bounded recent slice and truncates oversized history
items with compact markers. This keeps long-running sessions from growing model
input linearly while preserving the durable session snapshot for inspection.
Session state also keeps a bounded `prompt_budget_history`, and `/api/runtime`
derives a compact `cost` diagnostic block for Studio dashboards and API clients.

HTTP mode exposes the same workspace confirmation concept as the CLI through
`/api/workspace`, `/api/workspace/confirm`, `/api/workspace/clear`,
`/api/workspace/select`, and a small browser page at `/workspace`. If a
workspace-scoped request arrives while the workspace is unconfirmed, JSON
endpoints return `409 workspace_required` and SSE endpoints emit an error event
instead of exposing read/write/exec tools.

`GET /api/workspace` is also the browser contract for workspace lifecycle UI.
It returns the workspace snapshot together with a capability matrix and action
hints. Pure chat is marked available without workspace confirmation, while
file, command, workflow, tool, and `@file` operations are marked
workspace-required. Confirming or clearing the current workspace can happen in
process. Selecting the same workspace confirms it. Selecting a different
workspace returns a restart-required response with suggested `--workspace`
arguments because MCP processes, session approvals, and workspace-scoped tool
policy are initialized against the startup workspace root.
The same capability matrix and action hints are mirrored in `/api/runtime` as
`workspace_capabilities` and `workspace_actions`, allowing Studio dashboards to
refresh runtime state and workspace UI affordances through one inventory call.

HTTP mode also exposes workflow graph management endpoints plus the embedded
Agent Studio at `/console` and `/workflows`. The Studio is implemented as
modular static assets under `internal/api/web` and is embedded into the Go
binary, so release archives and Docker images remain self-contained. Its
workflow canvas persists the same YAML graph files used by the CLI runner,
including optional visual `position` metadata for dragged stage nodes. Runtime
execution ignores visual-only `start` / `end` nodes and visual `position`
metadata, while executable nodes continue to use explicit stage order, `next`
edges, branch strategy, agent selection, and skill selection. Optional
`node_type`, `tool`, and `params` metadata help the Studio represent richer
agent/skill/tool nodes and are included in executable stage prompts where
applicable.

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
- Custom workflow graph files can be managed through `GET/PUT/DELETE /api/workflow-graphs/{name}` or edited visually at `/workflows`.
- For graph schema and examples, see `docs/workflows.md`.
- `/session` renders persisted workflow, pending approvals, pending handoff, and last-routing sections for operator inspection
- ordinary chat can also persist a pending plan -> execution handoff so natural-language confirmations resume into the target executable agent instead of restarting planner output
- when a suspended fixer tool call is approved, resume replays the original assistant/tool-call context plus the approved tool result back into the fixer loop
- auditor reviews the completed implementation
- the original active agent is restored after workflow execution
- bounded workflow summaries are added to session history

## Collaboration state

The session store now includes two collaboration primitives that are independent of any specific frontend:

- collaboration messages: a bounded, newest-first timeline with id, timestamp, run id, stage, source agent, target agent, kind, subject, content, and metadata
- blackboard entries: shared durable notes with scope, run id, stage, owning agent, kind, title, content, status, tags, and metadata

HTTP exposes these through:

- `GET /api/collaboration/messages`
- `POST /api/collaboration/messages`
- `GET /api/collaboration/blackboard`
- `POST /api/collaboration/blackboard`
- `GET /api/collaboration/blackboard/{id}`
- `PUT /api/collaboration/blackboard/{id}`
- `DELETE /api/collaboration/blackboard/{id}`
- `GET /api/team-templates`
- `GET /api/team-templates/{name}`
- `GET /api/resources/team-templates`
- `GET /api/resources/team-templates/{name}`
- `PUT /api/resources/team-templates/{name}`
- `DELETE /api/resources/team-templates/{name}`
- `GET /api/team-state`

Both collections are persisted in `.goflow/session.json` with the rest of the session snapshot. This gives future team workflows and the Web Studio a stable place to record handoffs, observations, decisions, unresolved questions, and evidence without scraping natural-language stage output.

Workflow execution writes to these stores automatically:

- run start creates a `workflow_started` message and a blackboard `request`
- each completed stage creates a `stage_completed` message and a `stage_output` blackboard entry
- completed `team` stages also create structured `team_state`, `team_role`, `handoff`, and template blackboard slots plus `team_handoff` messages
- structured findings create `findings` blackboard entries
- pending approval creates an `approval_required` message and an open `approval` blackboard entry
- completion creates a `workflow_finished` message and a `final_summary` blackboard entry
- failure or cancellation creates an issue-style blackboard entry

Team templates are a reusable collaboration catalog. Built-ins are embedded
from `internal/agent/templates/teams/*.yaml` and include software-task,
audit/security, web research, binary triage, documentation, operations/runbook,
support, and framework-extension teams. Custom templates are versioned YAML
resources under `templates/teams/<name>.yaml` and are merged with built-ins; a
custom template with the same name overrides the built-in for workflow options,
validation, execution, and TeamState. Each template exposes role-to-agent/skill
mappings, expected handoffs, shared blackboard slots, output contracts, and a
recommended workflow template. They can be referenced by `team` workflow nodes,
which record the selected team as stage context and can expand to executable role stages when
`params.execute: true` is set. The templates are visible through `/teams`,
`/teams <name>`, `/api/team-templates`, `/api/resources/team-templates`, and
`/api/workflow-options`. Live team state is visible through
`/team-state [run-id] [team]` and `GET /api/team-state`, which derive active
owner, handoffs, blackboard entries, unresolved items, and pending
approval/input state from the session snapshot.

## Vertical kits

Vertical Agent kits package reusable domain setups without compiling Go code.
The backend stores kit manifests at `kits/<name>/kit.yaml` with the resource
kind `goflow.kit`. A kit can reference configured providers and agents, skills,
MCP tools, workflow graphs, workflow templates, team templates, policy rules,
required environment variables, examples, tags, and free-form metadata.

HTTP exposes the kit catalog through:

- `GET /api/kits`
- `GET /api/kits/{name}`
- `GET /api/resources/kits`
- `GET /api/resources/kits/{name}`
- `PUT /api/resources/kits/{name}`
- `DELETE /api/resources/kits/{name}`
- `GET /api/resources/kits/{name}/validate`
- `GET /api/resources/kits/scaffolds`
- `GET /api/resources/kits/scaffolds/{preset}`
- `POST /api/resources/kits/scaffolds/{preset}`
- `GET /api/resources/kits/{name}/export`
- `POST /api/resources/kits/import`

Validation is compatibility-oriented rather than activation-oriented: missing
agents, providers, skills, workflows, templates, and policy rules are errors;
unseen tools and unset required environment variables are warnings. This lets a
team version-control a vertical kit before every optional MCP server or secret
is present, while Studio can still show clear readiness guidance.

Kit scaffolding is a backend preset layer for Studio and the CLI
`/new-kit <preset> <name>` command.
The current presets cover software engineering, web security, broader security
research, binary analysis, documentation, operations runbooks, and customer
support. A scaffold creates only the kit manifest; it references existing
agents, skills, tools, workflows, workflow templates, team templates, and
policy rules rather than silently generating every dependency. This keeps the
resource graph inspectable and lets users export a full bundle after they add
or customize the referenced resources.

Kit import/export is a portable resource bundle layer rather than a binary
archive format. The exported `goflow.kit_bundle` document contains the kit
manifest and available modular resource documents for referenced providers,
agents, skills, tools, workflow graphs, workflow templates, team templates, and
policy rules. Provider API keys are redacted unless `include_secrets=1` is
requested. Import writes those documents back to runtime-home resource folders
and returns saved/skipped/error rows plus kit validation. Runtime registries for
providers, agents, and MCP tools are still built during bootstrap, so Studio
should surface the import response's restart hint when those resource types are
included.

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

