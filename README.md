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

Packaged examples:

- [examples](./examples): overview of copyable extension examples.
- [examples/extension-workflow](./examples/extension-workflow): a compact
  extension that combines a Python MCP server, a custom read-only agent, a
  skill, and a graph workflow. Smoke-test it with
  `python scripts/validate_extension_workflow.py`.
- [examples/binary-analysis-kit](./examples/binary-analysis-kit): a fuller
  materialized kit that connects an Agent, Skill, containerized MCP helper,
  Workflow, Workflow Template, Team Template, and Policy Rule for binary
  triage. Validate it with `python scripts/validate_binary_analysis_kit.py`.

Deployment files can be checked locally without Docker using `python scripts/validate_deployment_assets.py`.

For end-user installation and deployment commands, see [Installation And Deployment](./docs/install.md). For Chinese instructions, see [安装与部署](./docs/install.zh-CN.md).

## Key features

- Multi-agent runtime with named agent profiles
- Modular resource storage for agents, providers, MCP servers, skills,
  workflows, workflow templates, team templates, policy rules, and kits
- Registry-backed built-in workflows and file-backed workflow templates plus
  runtime-home custom workflow graphs
- Branded CLI startup banner with runtime/workspace summary
- Best-effort terminal title updates in interactive CLI sessions
- Blocking workflow approvals with arrow-key + Enter selection and numeric fallback
- OpenAI-compatible provider integration with fallback support
- stdio-based MCP tool execution
- Cross-language MCP support with built-in Go and Python servers
- Dynamic skill loading from `SKILL.md`
- Complex skills can declare deterministic helper scripts that execute through
  `skill_runner/run_script` as normal approved/audited `exec` tools
- Durable ordinary Agent runs and Workflow runs with reconnectable event
  streams, replay, diffs, export, explicit cancel/retry, and run-scoped
  approvals
- Workflow Studio canvas labels control-node branches, loop bodies, and
  post-control paths, with hover/selection scope frames for ordinary users
- Per-workspace session persistence with a lightweight `session.json` index and
  complete compressed `session.full.json.gz` replay archive
- Workspace confirmation gate: pure chat can run from the default directory, while file reads/writes, `@file` references, command execution, and workflows require a confirmed workspace
- Audit logging and CLI trace/status/session output
- External workspace mode via `--workspace`
- Windows, Linux, and Docker deployment paths
- Docker/Podman is the recommended strong isolation path for generated,
  third-party, write, exec, and network MCP tools
- HTTP JSON API and SSE streaming via `--http`

## Built-in Domain Resources

GoFlow ships with linked starter resources for common vertical domains:

- Agents: `software-engineer`, `security-researcher`,
  `web-security-researcher`, `binary-analyst`, `documentation-specialist`,
  `operations-specialist`, `support-specialist`, and
  `framework-extension-architect`
- Skills: `execution-plan`, `code-writing`, `code-audit`,
  `vulnerability-research`, `web-vulnerability-research`,
  `binary-vulnerability-research`, and `reverse-engineering`
- Kit presets: `multi-domain-agent`, `software-engineering`,
  `agent-framework`, `web-security`, `security-research`, `binary-analysis`,
  `documentation`, `operations-runbook`, and `customer-support`
- Workflow templates and team templates for those domains, including
  `multi-domain-intake-router` as the recommended first-run path. Built-in
  workflow templates are embedded from `internal/agent/templates/workflows/*.yaml`
  and can be overridden from `templates/workflows/*.yaml`.

Use `/kits multi-domain-agent-kit` or `/workflow-templates
multi-domain-intake-router` to inspect how Agent, Skill, Tool, Team, Workflow
Template, Policy Rule, and Kit resources connect.

Recommended first-run paths:

| Goal | Start from | What it gives you |
| --- | --- | --- |
| General onboarding | `multi-domain-agent-kit` | linked agents, skills, tools, teams, policy gates, and the `multi-domain-intake-router` workflow template |
| Software changes | `software-engineering-kit` | planner/fixer/auditor style implementation workflow with review gates |
| Web security review | `web-security-kit` | page and client-asset collection plus evidence-based security review |
| Binary triage | `binary-analysis-kit` | static binary metadata, strings, hex preview, team review, and report handoff |
| Framework extension | `agent-framework-kit` | starter resources for creating new Agent, Skill, Tool, Workflow, Team, Policy, and Kit packages |

## Quick start

### Prerequisites

- Go 1.25+
- Python 3 on `PATH` if you want the `python_notes` MCP server
- Docker or Podman if you want the recommended container sandbox for risky MCP
  tools
- An OpenAI-compatible model endpoint for actual Agent runs
- Provider credentials and model settings, configured either in Web Studio,
  `configs/providers/*.yaml`, or environment variables

### Configure a model provider

GoFlow can start before a model provider is fully configured. This lets new
users open Web Studio, inspect diagnostics, and fill Provider settings from the
Resources or Settings pages. Agent runs will return a setup-required message
until `base_url`, `api_key`, and `model` are configured.

Environment variables remain supported:

```bash
export GOFLOW_BASE_URL=http://localhost:11434/v1
export GOFLOW_API_KEY=local-dev-key
export GOFLOW_MODEL=qwen2.5-coder:7b
export GOFLOW_BACKUP_BASE_URL=$GOFLOW_BASE_URL
export GOFLOW_BACKUP_API_KEY=$GOFLOW_API_KEY
export GOFLOW_BACKUP_MODEL=$GOFLOW_MODEL
```

For a ready-to-edit template, copy `.env.example` and fill in your own values,
or paste the API Key directly into the Provider resource in Web Studio.

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

### Use Web Studio

Open `http://127.0.0.1:8080/console` after starting HTTP mode.
Use `?lang=zh` or `?lang=en` when you want a shareable language-specific link,
for example `http://127.0.0.1:8080/console?lang=zh#workspace`.

For the lowest-friction path:

1. Open **Resources**.
2. Choose the `multi-domain-agent` starter kit.
3. Use **Create linked starter** to materialize its Agent, Skill, Tool,
   Workflow, Workflow Template, Team Template, Policy Rule, and Kit files.
4. Open **Workflow Studio**.
5. Start from `multi-domain-intake-router`, inspect the template cards for node
   mix and linked resources, then run or fork the graph.

Workflow Studio exposes node metadata, examples, input/output references,
approval gates, quality gates, and template composition so users do not need to
guess which fields are valid before editing YAML.

Workflow Studio opens in **Simple** mode by default for ordinary users. Simple
mode keeps the canvas, templates, task cards, resource pickers, and visual flow
controls visible while hiding raw maps, context contracts, execution-order
internals, and run debugging. Turn on **Expert** mode when you want the complete
developer surface: all node types, all inspector tabs, raw params, advanced
routing, context contracts, input/output maps, and run diagnostics. Both modes
save the same workflow schema.

The **Observability** page shows live runtime health, current run focus, model
cost diagnostics, and MCP tool pressure. Use the MCP pressure panel to spot
active tool calls, queued calls, and saturated tool servers before increasing a
workflow's concurrency or retrying a stalled run.

If you are unsure how to configure a node in Studio, or what to put in
`input`, `outputs`, `artifacts`, or `acceptance_criteria`, go straight to
[Workflow Graphs](./docs/workflows.md), especially the node quick reference,
Studio panel guide, and node-type usage sections.

If you are unsure how Agents, Skills, Tools, Workflows, Team Templates, Policy
Rules, and Kits differ or connect, use the
[Resources And Settings Guide](./docs/resources.md).

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
Containerized Python MCP tool presets use the companion
`ghcr.io/fymatt/goflow-agent-mcp-python:<version>` image by default.

## Current status

The current baseline is usable end to end through CLI and Web Studio: resource
management, MCP tools, skills, workflows, durable ordinary Agent runs, durable
Workflow runs, approvals, workspace lifecycle, cost diagnostics, Docker-first
isolation, and CI/release packaging are implemented. Built-in resources now
include connected multi-domain Agent, Skill, Tool, Team, Workflow Template,
Policy Rule, and Kit packages. Ongoing work should be driven by real deployment
feedback, additional domain templates, and measurement-based cost and sandbox
hardening rather than by missing core runtime pieces.

## Common CLI commands

CLI commands use the `/` prefix.

```text
/help
/skills
/tools
/agents
/teams [name]
/team-state [run-id] [team]
/use <agent>
/mode <chat|plan|audit|fix>
/workspace [status|confirm|clear|use <path>]
/workflow plan-fix-audit [--approve] <request>
/workflow skill-chain <request>
/workflow <custom-name> <request>
/workflow-templates [name]
/workflow-node-metadata [type]
/expression-helpers [name] [--mode <mode>] [--node-type <type>]
/workflow-schemas [name] [--rebuild|--json|--export|--import <path>|--clear]
/status
/cost
/config-diagnostics [--json]
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
/new-provider <name>
/new-workflow <name> [--template <template>]
/kits [name] [--export]
/kits --import <path> [--replace]
/new-kit <preset> <name> [--materialize|--full]
exit
```

`/workspace status` shows whether the active workspace is explicit or still the current-directory default. `/workspace confirm` enables workspace-scoped tools for the current session. `/workspace clear` removes that confirmation. `/workspace use <path>` dynamically rebinds the CLI to another workspace when the runtime is idle. The old workspace session is saved, old MCP child processes are closed, and a fresh workspace-scoped runtime/session/tool policy is loaded. If a tool approval, handoff, workflow pause, or durable run is still active, the command blocks the switch and prints a restart fallback.

`--approve` pre-approves the planner -> fixer stage transition. Without it, GoFlow first asks whether to enter the fixer stage, then separately prompts again if a fixer tool call requires confirmation.

`/new-workflow <name> [--template <template>]` creates `workflows/<name>/workflow.yaml`. The default template is `plan-fix-audit`; richer built-ins include `complex-project-delivery`, `agent-framework-extension`, `software-quality-gate`, `web-research-risk`, `security-audit-evidence-gate`, `parallel-research-review`, `binary-triage`, `docs-review-publish`, `operations-runbook`, `customer-support-triage`, `human-input-security-review`, and `software-team-review-gate`. That file is executable through `/workflow <name> <request>` and can declare named stages with `agent`, `skill`, explicit `input`/`outputs`, replay `artifacts`, control nodes such as `condition`, `policy_guard`, `quality_gate`, `parallel`/`join`, `for_each`, `loop`, and `sub_workflow`, approval gates, and `next` edges.

Use `complex-project-delivery` for broad implementation work that should keep running until the accepted plan is complete: it analyzes requirements, builds a user-confirmed project plan, iterates implementation with verification and review, updates plan state, runs final validation, and emits a completion report.

`plan-fix-audit` and `skill-chain` remain runnable compatibility executors.
Custom graph files are the editable model. In HTTP, `/api/workflow-graphs`
lists editable graph files, while `/api/workflow-options.workflow_executors`
lists every runnable workflow entry for run selectors. Saving a valid graph
with the same name as a compatibility executor overrides that executor.

`/workflow-templates`, `/workflow-node-metadata`, and `/expression-helpers`
expose the workflow authoring catalog from the terminal. They mirror the
Studio-facing workflow template, node metadata, and expression helper APIs so
CLI users can inspect available node types, editable fields, outputs, helper
signatures, examples, and custom metadata paths without opening the browser.
Built-in workflow node metadata is embedded from
`internal/agent/templates/workflow_nodes/*.yaml`.
Built-in expression helper metadata is embedded from
`internal/agent/templates/expression_helpers/*.yaml`, with runtime overrides
available under `metadata/expression_helpers/*.yaml` and related template paths.
Built-in workflow policy rule metadata is embedded from
`internal/agent/templates/policy_rules/*.yaml`; custom executable rules still
live under `policies/workflow_rules/*.yaml`.

`/kits` lists local vertical Agent packages from `kits/<name>/kit.yaml`, and `/kits <name>` shows one manifest's referenced providers, agents, skills, tools, workflows, teams, policies, examples, metadata, and environment warnings. `/kits <name> --export [--format json|yaml] [--include-secrets]` prints a portable kit bundle, and `/kits --import <path> [--replace]` imports one into the current runtime home. `/new-kit <preset> <name>` creates a starter kit manifest; add `--materialize` or `--full` to generate a linked starter bundle with its own agent, skill, Docker/Podman containerized MCP helper, workflow, workflow template, team template, policy rule, and kit manifest. Built-in presets include `multi-domain-agent`, `software-engineering`, `agent-framework`, `web-security`, `security-research`, `binary-analysis`, `documentation`, `operations-runbook`, and `customer-support`. The manifest stays versionable and can later be validated, edited, exported, or imported through Studio resource APIs.

When `binary-analysis` is materialized, the generated helper is a static binary
triage MCP server with `binary_file_info`, `binary_strings`, and `hex_preview`.
Use ordinary paths such as `sample.bin` for binary inputs; `@file` references
are reserved for UTF-8 text content. That helper is template-backed as well:
the embedded template is
`internal/scaffold/templates/tools/python/binary-analysis-server.py.tmpl`, with
runtime overrides at
`templates/tools/python/binary-analysis-server.py.tmpl`.

Tool scaffold presets are file-backed. Built-ins are embedded from
`internal/scaffold/templates/tools/scaffolds/presets.yaml`; a runtime can add or
override presets with YAML files under `templates/tools/scaffolds/*.yaml`.
The generated Python MCP tool server and its modular MCP config are
file-backed too: built-ins live under
`internal/scaffold/templates/tools/python/server.py.tmpl` and
`internal/scaffold/templates/tools/python/config.yaml.tmpl`, with runtime
overrides at `templates/tools/python/server.py.tmpl` and
`templates/tools/python/config.yaml.tmpl`. `/new-tool python` and Studio's
default Python tool resource use the same renderer.

Kit scaffold presets are file-backed. Built-ins are embedded from
`internal/scaffold/templates/kits/scaffolds/presets.yaml`, and a runtime can add
or override presets with YAML files under `templates/kits/scaffolds/*.yaml`.
Materialized kit resources are file-backed too: the default `kit.yaml`,
`agent.yaml`, `SKILL.md`, container MCP config, workflow, workflow template,
team template, policy rule, and workflow note templates are embedded from
`internal/scaffold/templates/kits/materialized/generic/*.tmpl`. Override them
per preset with `templates/kits/materialized/<preset>/*.tmpl`, or override the
generic fallback with `templates/kits/materialized/generic/*.tmpl`.

Team templates are file-backed as well. Built-ins are embedded from
`internal/agent/templates/teams/*.yaml`; runtime homes can add new reusable
teams or override shipped teams with `templates/teams/*.yaml`. The same catalog
is used by `/teams`, workflow `team` node validation, Kit materialization, and
Studio resource forms.

`/workflow-schemas` lists observed workflow output schemas captured from
completed runs. `/workflow-schemas <name>` shows stage output fields and types,
`--json` prints the list or detail as JSON for scripts, `--export` prints a
versioned bundle compatible with the HTTP import API, `--import <path>` imports
that bundle or a raw schema JSON file, `--rebuild` rebuilds the catalog from
retained run history, and `--clear` removes one schema or all schemas. The same
catalog powers Studio expression suggestions and reusable schema resources under
`schemas/workflows/*.json`. That directory is optional and normally starts
empty; built-in workflow templates are embedded separately from
`internal/agent/templates/workflows/*.yaml`.

Custom `policy_guard` rules can be stored as YAML under `policies/workflow_rules/*.yaml`; they appear in `/api/workflow-options` alongside built-in guard rules for Studio forms and workflow execution. Policy rule scaffold presets are also YAML-backed: built-ins live in `internal/scaffold/templates/policies/scaffolds/presets.yaml`, and runtime homes can add or override presets with `templates/policies/scaffolds/*.yaml`.

`/new-agent <name>`, `/new-provider <name>`, and the Studio tool builder create modular runtime config files under `configs/agents/`, `configs/providers/`, and `configs/mcp_servers/`. These files are loaded on startup, so custom agents, model providers, and MCP servers can stay versionable without bloating the main `configs/goflow.yaml`. The default Agent and Provider scaffolds are file-backed too: built-ins live under `internal/scaffold/templates/config/agents/default.yaml.tmpl` and `internal/scaffold/templates/config/providers/default.yaml.tmpl`, and runtime homes can override them with `templates/config/agents/default.yaml.tmpl` and `templates/config/providers/default.yaml.tmpl`.

`/skill-templates` and `/new-skill <template> <name>` use file-backed Skill scaffold templates. Built-ins are embedded from `internal/scaffold/templates/skills/scaffolds/templates.yaml`, and runtime homes can add or override templates with `templates/skills/scaffolds/*.yaml`.

`/new-team <preset> <name>` and `/api/resources/team-templates/scaffolds` use file-backed Team scaffold presets. Built-ins are embedded from `internal/scaffold/templates/teams/scaffolds/presets.yaml`, with runtime overrides in `templates/teams/scaffolds/*.yaml`.

Running `/workflow` without arguments prints usage plus discovered custom workflows.

`/cost` shows the session's prompt budget and token usage history, including
average/max estimated prompt size, cacheable/non-cacheable context, top agents,
provider-reported token totals, cached-token hit rate, prompt-prefix reuse, and
optimization hints. It uses already-recorded runtime diagnostics and does not
call the model.

`/config-diagnostics` reload-validates the saved `configs/goflow.yaml` plus
modular agent/provider/MCP/workflow resource files, summarizes warnings/errors,
and hides optional empty extension directories from the primary text list.
Use `/config-diagnostics --json` for the full machine-readable envelope that
matches `GET /api/config/diagnostics`. HTTP clients can request
`/api/config/diagnostics?include_optional=0` to hide optional non-actionable
info items in JSON responses.

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

Session persistence is split for Web Studio performance: `.goflow/session.json`
is a compact index for status polling, while `.goflow/session.full.json.gz`
stores the complete compressed run history used for detailed replay.

Current endpoints:
- `GET /api/runtime`
- `GET /api/runtime/cost`
- `GET /api/help`
- `GET /api/workspace`
- `POST /api/workspace/confirm`
- `POST /api/workspace/clear`
- `POST /api/workspace/select`
- `POST /api/run`
- `POST /api/run/stream`
- `GET /api/runs`
- `GET /api/session`
- `GET /api/session-artifacts`
- `GET /api/session-artifacts/{id}`
- `POST /api/workflows/{name}`
- `POST /api/workflows/{name}/stream`
- `GET /api/workflow-runs`
- `GET /api/workflow-runs/{id}`
- `GET /api/workflow-runs/{id}/actions`
- `GET /api/workflow-runs/{id}/replay`
- `POST /api/workflow-runs/{id}/approve`
- `POST /api/workflow-runs/{id}/approve/stream`
- `POST /api/workflow-runs/{id}/approve-tools`
- `POST /api/workflow-runs/{id}/approve-tools/stream`
- `GET /api/workflow-graphs`
- `POST /api/workflow-graphs`
- `POST /api/workflow-graphs/validate`
- `POST /api/workflow-graphs/import`
- `GET /api/workflow-graphs/{name}`
- `PUT /api/workflow-graphs/{name}`
- `DELETE /api/workflow-graphs/{name}`
- `GET /api/workflow-graphs/{name}/export`
- `POST /api/workflow-graphs/{name}/validate`
- `GET /api/workflow-options`
- `GET /api/workflow-templates`
- `GET /api/workflow-templates/{name}`
- `GET /api/team-templates`
- `GET /api/team-templates/{name}`
- `GET /api/team-state`
- `GET /api/resources/skills`, `GET/PUT /api/resources/skills/{name}`
- `GET/PUT/DELETE /api/resources/skills/{name}/files/...`
- `GET /api/resources/agents`, `GET/PUT/DELETE /api/resources/agents/{name}`
- `GET /api/resources/providers`, `GET/PUT/DELETE /api/resources/providers/{name}`
- `GET /api/resources/tools`, `GET/PUT/DELETE /api/resources/tools/{name}`
- `GET /api/resources/policy-rules`, `GET/PUT/DELETE /api/resources/policy-rules/{name}`
- `GET /api/resources/team-templates`, `GET/PUT/DELETE /api/resources/team-templates/{name}`
- `POST /api/approvals/{callID}/approve`
- `POST /api/approvals/{callID}/approve/stream`
- `POST /api/approvals/{callID}/deny`
- `POST /api/approvals/{callID}/deny/stream`
- `POST /api/approvals/approve-all`

HTTP mode also serves a basic workspace page at `GET /workspace`. It shows the
current workspace root, whether it is confirmed, and actions for confirm, clear,
or selecting another path. Packaged HTTP mode supports dynamic workspace rebind
when execution is idle; fallback responses include explicit restart guidance and
blocker codes so browser clients can explain why a switch is unavailable.

`GET /api/runtime` includes a `cost` block with the latest prompt budget,
bounded prompt-budget history, average/max estimated input size, cacheable
prefix average, non-cacheable context average, prompt-prefix hash counts, stored
token usage samples, per-agent/per-mode/per-stage trend rows, and compact
recommendations for likely avoidable context in Studio cost diagnostics.
Recommendations include machine-readable `measurement`, `action`,
`requires_config`, and `expected_savings_kind` fields so Studio can distinguish
observed waste from optional configuration changes such as low-cost router or
summarizer routes.
`GET /api/runtime/cost` returns the same cost block alone for lightweight
polling.
Large compacted tool observations are stored as session artifacts with refs like
`goflow://session-artifacts/<id>`; include that ref in a later CLI/HTTP request
to rehydrate the artifact content into prompt context only when needed.
The same endpoint includes a `verifier` block showing the effective verifier
agent/provider/model route, so deployments can send low-risk post-run checks to
a cheaper model without changing the main fixer or auditor profile.
Optional `cost_control.router` and `cost_control.summarizer` routes are exposed
as `auxiliary_models`; they can move unmatched intent classification and
iteration-budget final summaries to cheaper configured providers.

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
- `prompt_budget` for pre-call estimated prompt cost by system prompt, compacted session history, messages, tool schemas, matched skill, filtered tool count, and stable prompt-prefix/cache diagnostics
- `token_usage` for provider-reported prompt/output/cache token counts
- `workflow_result` for final or paused workflow state from `/api/workflows/{name}/stream` and streamed approval resumes

The streamed approval endpoints resume ordinary chat or workflow execution when the approved or denied call has resumable context.

HTTP mode also serves the embedded Agent Studio at `GET /console` and
`GET /workflows`. The Studio is a modern visual workflow surface with a
workflow list, node palette, draggable stage canvas, property panel, run
preview, approvals, resource catalog, workspace controls, observability, and
settings/update guidance. The workflow APIs expose reusable graph templates
and multi-agent team templates for Studio scaffolding. The Playground can choose a specific agent before a
run, the resource catalog includes a validated Skill editor, and the main UI supports English and Chinese. Custom workflow graphs are persisted under
`workflows/<name>/workflow.yaml`. See [Workflow Graphs](./docs/workflows.md).

The Settings page shows the release update strategy without contacting GitHub
automatically. Users explicitly click the update check button, which calls
`POST /api/update-policy/check` and compares the running build with the
configured release feed.
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

File, note, source-inspection, and binary-inspection tools resolve every path under the active workspace root and reject path traversal or symlink escapes. Text file reads are decoded as UTF-8, UTF-8 BOM is accepted and stripped, and non-UTF-8 bytes are rejected for text tools so binary inspection tools can handle them instead. If the workspace was only defaulted from the current directory, GoFlow hides workspace-scoped read/write/exec tools from the model until you confirm it. Write operations render git-like CLI summaries with status letters, added/deleted line counts, changed line ranges, byte counts, and compact colored diff hunks. In the interactive CLI, type `@` to show workspace file suggestions, then type a prefix and press `Tab` to complete a unique match or show candidates. Workspace file suggestions skip dependency/build directories such as `.venv`, `node_modules`, `.git`, `__pycache__`, `vendor`, `dist`, and `build` before result limits are applied, and filename fragments work as contains searches. You can reference workspace files inline with `@relative/path.ext`; GoFlow reads the file through the normal `file_tools/read_file` path, prints the same tool logs, and attaches the file content before the request reaches the agent. In non-interactive input, type `@prefix` as a full line to list matching references.

Workspace switching is intentionally conservative. The CLI `/workspace use
<path>` and packaged HTTP `/api/workspace/select` can dynamically rebind only
while execution is idle. `/api/workspace` and `/api/runtime` expose
`switch_mode`, blockers, action hints, and restart guidance so clients can show a
folder picker, submit the selected path, and explain when a restart is safer.

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

### Explore a linked kit

```text
/kits binary-analysis-kit
/workflow-templates binary-triage
```

For a copyable materialized version with its own Agent, Skill, MCP helper,
Workflow, Team Template, and Policy Rule, see
[examples/binary-analysis-kit](./examples/binary-analysis-kit).

### Triage binary artifacts

```text
/workflow skill-chain analyze sample.bin for suspicious imports, strings, and likely attack surfaces
```

Use a plain workspace path for raw binaries. `@file` references are reserved for
UTF-8 text content.

## Documentation

- [Installation And Deployment](./docs/install.md)
- [Architecture](./docs/architecture.md)
- [Configuration](./docs/configuration.md)
- [Resources And Settings Guide](./docs/resources.md)
- [Deployment](./docs/deployment.md)
- [Release Packaging](./docs/release.md)
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

