# GoFlow Master Plan

> Status: active framework roadmap  
> Updated: 2026-04-27  
> Strategy: framework-first, product-ready by default  
> Scope: agent routing, MCP tooling, skills, workflow orchestration, multi-agent collaboration, visual composition, CLI/HTTP usability, deployment, and second-development ergonomics

## 1. Product Direction

GoFlow's north star is to become a general-purpose Agent framework with a low barrier to entry and a high ceiling for customization.

Developers should be able to:

1. start an Agent out of the box through configuration, with tool calling, memory, planning, approvals, workspace safety, and observable execution already wired together
2. build vertical Agents by defining domain-specific skills, tools, prompts, workflows, policies, and runtime profiles for customer support, software engineering, data analysis, operations, security research, and other domains
3. compose multiple Agents through roles, handoffs, a message bus, shared blackboard state, workflow stages, and explicit collaboration contracts
4. visually orchestrate Agents, skills, tools, and workflows while preserving code/config definitions for version control and second development

GoFlow should therefore be both:

1. a reusable local agent framework that is easy to extend with custom agents, tools, skills, workflows, and transports
2. an out-of-the-box CLI/HTTP product that ordinary users can run against a workspace without first understanding the framework internals
3. a deployable runtime that works on Windows, Linux, and Docker while preserving the runtime-home/workspace boundary
4. a workspace-aware assistant that supports pure chat without a workspace, but requires an explicit workspace before reading, writing, generating project files, running workflows over files, or using workspace-scoped tools
5. a visual Agent Studio, not only a terminal app: HTTP mode should expose CLI-level operational power through a modern workflow-platform UI for workflow design, workspace selection, resource catalogs, approvals, diffs, logs, tokens, settings, and session state
6. a framework that supports both configuration-first use and code-first extension, so simple users can stay in YAML/UI while advanced developers can replace or extend core components

The long-term goal is to let limited LLMs produce higher-quality work through strong runtime scaffolding:

- clear role boundaries
- better tool selection
- explicit plans and handoffs
- repeatable skill workflows
- reusable Agent/Skill/Tool/Workflow composition primitives
- shared blackboard state and message-bus mediated collaboration for multi-agent tasks
- verifiable observations
- safer workspace-scoped execution
- clear operator logs and approvals
- predictable Windows/Linux/Docker startup paths
- explicit workspace selection and workspace-required task gating
- visual workflow orchestration for complex tasks

## 2. Current Implemented Baseline

### Runtime And Agents

- Multi-agent runtime with `chat`, `planner`, `fixer`, and `auditor` profiles.
- `default_agent: chat` is used for startup and post-turn recovery.
- `chat` is intentionally narrowed to `read` and `network`; write/exec work is routed to `fixer`.
- `planner` is read/network oriented and produces executable plans.
- `fixer` owns write/exec implementation work under confirmation policy.
- `auditor` owns review, vulnerability research, reverse engineering, and risk assessment.
- Ordinary chat can route by intent into planner/fixer/auditor roles.
- After a routed normal turn completes, the runtime restores the original agent state when no approval/handoff is pending.
- Tool-call turns suppress streamed prose preludes so the CLI does not repeat "I will read/write..." text before visible tool logs.
- Normal agent and workflow-stage loops request a final no-tool summary when the tool iteration budget is reached.
- Tool validation failures, unavailable tools, and MCP call errors are returned to the model as tool-result observations instead of hard-stopping the agent loop.
- LLM token usage is emitted as stream events when providers report it, so CLI/HTTP consumers can observe prompt/output/cache token accounting independently of audit logging.
- Optional verifier passes can run after completed `fix` and `audit` turns, using a configured verifier agent without tools to check unsupported claims, missing verification, regressions, risks, and next steps.
- Agent runs emit explicit task-stage events for `inspect`, `plan`, `modify`, `verify`, and `summarize`; the latest stage is persisted in session state and shown in `/status` / `/session`.
- `/status` exposes active agent, mode, routing result, workflow state, pending approvals, and MCP health.

### Approval And Continuity

- Normal chat and workflows use blocking approval prompts for suspended tool calls.
- Approval choices include approve, deny, and "approve and remember this tool for this session".
- Remembered approvals are scoped to workspace + tool kind + tool name and persisted under `.goflow/session.json`.
- Workflow-scoped auto approval is limited to the same workflow/stage/tool boundary.
- Approved normal-chat tool calls can resume the interrupted LLM tool loop instead of requiring the user to type "continue".
- Workflow resume replays approved tool results into the suspended stage conversation.
- Pressing Escape twice cancels the current agent/workflow operation in the interactive CLI.
- Esc-cancelled turns remember the cancelled request in the current CLI process. A later continuation-only reply such as `继续`, `重试`, or `continue` re-runs that request from the beginning with a clear retry log. Durable mid-stage resume remains a future workflow-run persistence goal.

### MCP Tools

Built-in MCP is stdio-based and supports multiple implementation languages.

Implemented Go MCP servers:

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

Implemented Python MCP tools:

- `python_notes/read_note`
- `python_notes/write_note`
- `python_notes/python_ast_summary`
- `python_notes/json_query`
- `python_notes/binary_file_info`
- `python_notes/binary_strings`
- `python_notes/hex_preview`

Security baseline:

- file, note, source, JSON, search, delete, and binary tools resolve paths under the active workspace root
- path traversal and symlink escapes outside the workspace are rejected
- network tools only accept HTTP(S) where applicable
- destructive file deletion refuses workspace-root deletion and requires explicit recursive intent for directories
- duplicate short tool names no longer silently overwrite routing; ambiguous short names require the qualified `server/tool` form
- MCP discovery rejects unsafe third-party tool metadata before exposure: unsafe names, missing/invalid kind, non-object schemas, open `additionalProperties`, undeclared required fields, and duplicate names from the same server
- enabled MCP servers must constrain startup with `allowed_commands` or `allowed_command_paths`
- MCP child processes no longer inherit the full environment when `env_allowlist` is omitted; only minimal platform variables plus `GOFLOW_WORKSPACE_ROOT` are passed
- `/tools` surfaces compact schema diagnostics and short-name collision warnings for the accepted tool catalog
- `scripts/validate_python_mcp.py` validates the built-in Python MCP server and acts as a template for Python MCP checks
- `isolation: windows_job` is available on Windows as an opt-in MCP lifecycle adapter using Job Object kill-on-close behavior
- `isolation: linux_cgroup` is available on Linux as an opt-in MCP resource-control adapter with cgroup v2 `memory.max`, `pids.max`, and `cpu.max` support

### Second-Development Guides

- `docs/mcp-authoring.md` documents the stdio JSON-RPC contract, workspace-root rules, config shape, diagnostics, and validation checklist.
- `docs/mcp-isolation.md` documents the current isolation boundary, platform adapter tradeoffs, and a staged plan for stronger MCP containment.
- `docs/skill-authoring.md` documents skill metadata, trigger design, tool declarations, output contracts, and validation checklist.
- `examples/extension-workflow` provides an end-to-end extension combining a Python MCP server, custom agent snippet, custom skill, and graph workflow.
- `scripts/validate_extension_workflow.py` smoke-tests the packaged extension example's files, MCP schema, and workspace-summary tool behavior.

### `@` Workspace References

- CLI input supports `@relative/path` references.
- Referenced files are read through `file_tools/read_file`, so the operator sees the same tool-call logs as a normal read.
- Referenced file content and metadata are injected into the prompt before the LLM receives it.
- Path resolution remains workspace-root scoped.
- Interactive CLI input shows workspace file suggestions when `@` is typed and supports `Tab` completion for `@prefix`.
- `@` completion uses prefix matching first and falls back to fuzzy matching when the typed path fragment is not a direct prefix.
- Interactive CLI input supports `Tab` completion for `/` commands and only shows completion/redraw UI for command/reference contexts.
- `/` command completion uses prefix matching first and falls back to fuzzy matching when no direct command prefix matches.
- Interactive CLI input keeps an in-memory prompt history for the current process and supports Up/Down navigation while preserving the current draft line.
- Prompt input consumes common terminal escape sequences such as arrows, Home, End, and Delete so they do not leak as `[A`, `[B`, `[H`, or `[F` text.
- Non-interactive input can still type `@prefix` as a full line to list matching workspace reference suggestions.

### Built-In Skills

Implemented baseline skills:

- `code-writing`
- `code-audit`
- `vulnerability-research`
- `web-vulnerability-research`
- `binary-vulnerability-research`
- `execution-plan`
- `reverse-engineering`

Skill metadata can declare:

- tools
- params
- activation keywords
- preferred mode
- preferred agent
- allowed tool kind envelope
- output kind
- priority / next skills metadata
- explainable match diagnostics persisted in session state and surfaced in `/status`

Skill authoring support:

- `/skill-templates` lists built-in scaffolds for coding, audit, web research, binary research, and planning.
- `/new-skill <template> <name>` creates a validated `skills/<name>/SKILL.md` and reloads skills.
- `/skills` displays activation hints plus recommended follow-up skills from `next_skills`.
- `/workflow skill-chain <request>` executes the matched skill and declared `next_skills`, choosing stage agents from `preferred_agent` or mode while preserving agent permission boundaries.
- `skill-chain` supports conditional next-stage selection with `metadata.next_strategy: select` / `conditional`.
- Conditional `skill-chain` stages can explicitly choose declared follow-ups by outputting `Next skill: ...` or `Next skills: ...`.
- Runtime workflow graph files under `workflows/<name>/workflow.yaml` are executable with `/workflow <name> <request>`.
- Custom workflow graph stages bind an explicit `agent`, `skill`, optional `approval`, optional `next_strategy`, and optional `next` stage list.
- Graph workflows support sequential stages, selected branches, explicit `Next skill: ...` directives, and suspended tool approval/resume.
- HTTP workflow graph management endpoints persist custom workflows under `workflows/<name>/workflow.yaml`.
- HTTP mode serves `/workflows`, a browser workflow editor with draggable stage nodes and optional persisted visual `position` metadata.

### CLI And Operator UX

- Branded startup banner shows runtime/workspace/agent/mode/policy/tool kinds.
- Completed baseline: CLI startup distinguishes explicit `--workspace` from current-directory defaults; pure chat can run before confirmation, while workspace-scoped reads/writes/exec tools are hidden and `@file`, workflow, document/project generation, and command/test requests prompt for `/workspace confirm`.
- Tool logs include tool kind, path/URL summaries, completion summaries, and change/diff previews for writes.
- Write logs use a compact git-like summary with status letters, added/deleted line counts, changed line ranges, byte counts, and colored `diff --goflow` hunks.
- Multi-file write runs add an end-of-turn `[write-summary]` with total files, added/deleted line counts, bytes written, status counts, and compact per-file rows.
- LLM turns add an end-of-turn `[tokens]` summary with aggregated input, output, total, and cached token counts when the backend reports usage.
- Model waits emit operator-visible status lines before initial responses, after tool observations, and before no-tool final summaries.
- High-level task stages render as `[stage] ...` lines so operators can distinguish context gathering, write/exec work, verifier checks, and final summarization.
- `/tools`, `/skills`, `/agents`, `/status`, and `/session` expose runtime state.
- `/agents`, `/skills`, and `/tools` now include compact summary rows, stable grouping, health/policy counts, activation metadata, and warning counts for faster scanning.
- `/skill-templates` and `/new-skill` provide built-in skill scaffolding for second development; generated skills include example requests and extension notes.
- `/new-tool python`, `/new-agent`, and `/new-workflow` create starter scaffolds for MCP servers, agent config snippets, and workflow blueprints, with scaffold validation before writing and operator-facing next-step hints after creation.
- Python MCP tool scaffolds include read and write examples, strict schemas, and workspace-root path scoping so cross-language tool development starts from a safer template.
- `/workflow` without arguments lists built-in workflows plus discovered executable workflow graph files under `workflows/<name>/workflow.yaml`.
- `/trace on|off` toggles extra trace/audit rendering in the CLI without changing runtime behavior.
- Ordinary `@file` reads and tool reads share visible log paths.
- Default config enables a concise verifier pass through the auditor for `fix` and `audit` results; it can be disabled under `verifier.enabled`.

### HTTP Surface

- HTTP JSON and SSE endpoints are implemented.
- Shared runtime bootstrap is reused by CLI and HTTP.
- Session inspection and approval endpoints exist.
- Workflow graph listing, create/update/delete, option discovery, and visual editing endpoints are implemented.
- HTTP SSE endpoints now forward the same `schema.StreamEvent` semantics used by the CLI for normal turns, workflow runs, and streamed approval resumes, including task stages, tool results, approvals, token usage, and final `workflow_result` events.
- HTTP now has a modular embedded frontend baseline under `internal/api/web`: `/console` and `/workflows` serve a modern Agent Studio shell with overview, workflow Studio, agent-selectable playground, workspace, approval, resource catalog, observability, and settings areas.
- The Studio supports Chinese/English language switching for the main navigation and core workflow/playground surfaces.
- The workflow Studio baseline uses a Dify-style canvas: workflow list, draggable node palette, start/end markers, agent/skill/tool/custom node metadata, visual edges, click-to-connect custom links, node parameter editing, save/delete, and run preview backed by persisted workflow graph YAML.
- HTTP has supporting frontend APIs for runtime inventory (`/api/runtime`), active-agent selection (`/api/runtime/agent`), workspace file suggestions (`/api/workspace-files`), update policy metadata (`/api/update-policy`), and approve-and-remember actions.
- Resource editing baseline exists for skills: Studio can create/update `skills/<name>/SKILL.md` through validated `/api/resources/skills/{name}` saves, then hot reload skills when the manager supports reload.

### Deployment

- Windows local startup is supported through `run-goflow.example.cmd`.
- Linux local startup is supported through direct `go run ./cmd/goflow` or compiled binaries.
- `--config` allows selecting an alternate runtime config, which lets Docker use container-specific MCP commands without overwriting development config.
- `configs/agent.docker.yaml` runs compiled Go MCP server binaries plus the Python MCP server under `/app`, with `/workspace` as the target workspace.
- `configs/agent.binary.yaml` supports archive-based Windows/Linux launches by taking compiled MCP binary paths from launcher-provided environment variables; release launchers now default backup provider variables from primary provider values, and direct execution from `bin/goflow` resolves runtime home to the archive root.
- Docker deployment files exist: `Dockerfile`, `.dockerignore`, `docker-compose.yml`, and `docs/deployment.md`.
- Release packaging files exist: `scripts/build_release_assets.py`, `docs/release.md`, and `.github/workflows/release.yml`.
- `.env.example` contains placeholders rather than credentials.
- `.github/workflows/ci.yml` runs Linux Go/Python smoke tests plus Docker image build and HTTP startup checks.
- `scripts/validate_deployment_assets.py` statically validates Docker/deployment files without requiring Docker locally.
- HTTP mode handles Ctrl+C/SIGTERM through graceful server shutdown instead of hanging inside `ListenAndServe`.

## 3. Current Gaps And Risks

### Execution Loop Quality

- Completed baseline: normal agent loops and workflow-stage loops now disable tools and ask for a final summary when tool budget is reached.
- Completed baseline: streamed prose preludes are suppressed on successful tool-call turns to reduce repeated "I will ..." output before tool logs.
- Completed baseline: prompts now explicitly tell agents to call tools directly and continue from observations instead of restating the same plan.
- Completed baseline: tool argument schema errors, unknown tools, policy denials, and MCP call errors are fed back as observations so the model can recover or summarize without losing context.
- Completed baseline: malformed tool-call JSON retries use structured recovery rules, suppress repeated prose, require complete arguments, and tell the model to stop calling tools if arguments cannot be reconstructed safely.
- The runtime needs stronger "enough context, now act" nudges for implementation requests.
- Completed CLI baseline: the runtime distinguishes pure chat from workspace-required tasks when the CLI workspace was only defaulted. If the request needs file generation, file reads/writes, command execution, `@file`, or workflow stages over project files, the CLI stops and asks the operator to confirm the workspace.
- Completed HTTP baseline: workspace lifecycle APIs and UI can display, confirm, clear, and request restart for workspace selection. Remaining gap is dynamic workspace rebinding and a platform-aware folder picker where the host environment can support one safely.

### CLI Interactivity

- Completed baseline: `@` reference suggestions, `/` command completion, cursor-key editing, in-memory prompt history navigation, and fuzzy completion are implemented for the interactive CLI.
- Approval and diff output are improved, with git-like write summaries, colored hunks, and multi-file write batching summaries.
- Completed baseline: long LLM waits now emit visible status lines before model responses, tool-result follow-ups, and final-summary requests.
- Completed baseline: `/workspace status`, `/workspace confirm`, `/workspace clear`, and `/workspace use <path>` expose CLI workspace state. Runtime rebinding remains conservative: selecting a different path tells the operator to restart with `--workspace <path>` so MCP servers are rebound safely.
- Workspace selection UX is still incomplete for dynamic switching. The product should eventually offer a platform-aware folder picker where supported, plus a text fallback in terminals or headless environments.

### MCP Extensibility

- Completed baseline: third-party MCP discovery now validates safe names, explicit capability kinds, strict object schemas, duplicate server-local tool names, command allowlists, and minimal default environment inheritance.
- Completed baseline: MCP regression coverage now pins duplicate server-local tool rejection, ambiguous short-name routing errors, strict schema trailing-data rejection, symlink-parent write rejection, workspace-root delete refusal, recursive directory delete requirements, and list-tree symlink escape skipping.
- Completed baseline: MCP server config supports `isolation: process_group`, which starts child servers in a separate process group on supported platforms for cleaner lifecycle boundaries.
- Completed baseline: MCP server config supports `isolation: windows_job` on Windows for Job Object kill-on-close lifecycle isolation.
- Remaining gap: `network_disabled` is advisory, and `process_group` / `windows_job` / `linux_cgroup` are lifecycle or resource-control isolation rather than cross-platform network, filesystem, privilege, or container sandboxes.
- Web asset fetching is source-oriented only; intrusive scanning is intentionally out of scope.
- Binary tools are static triage helpers, not disassemblers/debuggers.

### Skill And Workflow Orchestration

- Skill matching is still keyword-first, but the selected score and reason are now explainable.
- Skills can declare `next_skills`, `/skills` displays them, and `skill-chain` can execute them with linear, metadata-selected, or explicit planner-output branching.
- Completed baseline: skill, Python MCP tool, agent snippet, and executable workflow blueprint scaffold commands include richer generated examples, next-step hints, validation guards, and authoring docs.
- Custom workflow graph execution has a tested baseline, invalid graph files now surface validation errors instead of being reported as unknown workflows, validation edge cases are covered by tests, `docs/workflows.md` documents branch selection, stage approval, approval-resume examples, HTTP graph management APIs, and the visual editor.
- Complex-task workflow support needs a richer execution model: durable per-stage state, explicit stage inputs/outputs, artifacts, retries, cancellation, manual checkpoints, longer pause/resume windows, nested or reusable sub-workflows, and better run history.
- The visual workflow Studio is a baseline authoring surface. It still needs durable run monitoring, stage-level logs, approval handling, diff review, branch visualization, reusable templates, and workflow run replay/history.

### Multi-Agent Collaboration

- Current multi-agent behavior is mostly routed single-agent execution: `chat`, `planner`, `fixer`, `auditor`, and optional verifier roles can hand off or be selected by workflows, but they do not yet collaborate through a first-class team runtime.
- Remaining gap: define a message bus so agents can send typed messages, requests, observations, critiques, and final handoff packets without losing provenance.
- Remaining gap: define a shared blackboard for durable task state, evidence, intermediate artifacts, decisions, open questions, and stage outputs across agents and workflow runs.
- Remaining gap: define collaboration patterns such as planner -> implementer -> tester -> auditor, researcher -> analyst -> reporter, and operator-supervised review loops as reusable configuration templates.
- Remaining gap: expose multi-agent state in CLI/HTTP so users can see which agent owns a task, which messages were exchanged, what blackboard entries changed, and what decisions remain unresolved.

### HTTP Console And API Parity

- HTTP mode exposes core run, stream, session, approval, workflow execution, workflow graph management, workspace lifecycle APIs, a basic workspace page, and the modular Agent Studio baseline.
- Remaining gap: HTTP should reach CLI feature parity. It should expose first-class endpoints and UI for `/help`, `/agents`, `/skills`, `/tools`, `/status`, `/session`, `/trace`, approval choices including remember/approve-all scopes, `@file` attachment behavior, token usage, task stages, write diffs, workflow runs, and session persistence.
- Completed baseline: HTTP has `/api/workspace`, `/api/workspace/confirm`, `/api/workspace/clear`, `/api/workspace/select`, workspace-required JSON/SSE failures, and a basic `/workspace` page. Dynamic workspace rebinding still requires restart so MCP servers are rebound safely.
- Completed baseline: HTTP provides a unified browser Studio similar to modern agent/workflow platforms, with overview, visual workflow builder, agent-selectable playground, workspace picker, approval inbox, tool/skill/agent catalog, status/session visibility, settings, update-policy guidance, direct validated skill editing, and a resource-builder path for agents/tools that opens safe scaffold/edit requests in Playground.
- Remaining gap: the Studio still needs deeper workflow run history, replayable stage logs, first-class diff viewer, direct validated visual config editing for agents/MCP tools/providers, richer onboarding, reusable workflow templates, import/export, and more polished Dify-like interaction details such as edge handles with branch labels, node-type-specific forms, and canvas zoom/pan.
- The HTTP UI must preserve the same workspace safety boundary as CLI. UI convenience must not let browser actions read or write outside the selected workspace root.

### Documentation

- README and docs now describe most implemented behavior, including end-user install paths for release archives, published Docker images, Docker Compose, source checkout usage, HTTP API smoke tests, workspace/session locations, checksums, and SBOM.
- Chinese and English installation documentation should remain synchronized as features stabilize.
- Authoring guides still need more examples for custom agent/tool/skill/workflow combinations.
- Documentation should add a clear user-facing workspace model: no-workspace chat, current-directory default workspace, selecting/switching workspaces, and which actions require workspace confirmation.

### Deployment And Packaging

- Completed baseline: `--config` selects runtime config files for different deployment environments.
- Completed baseline: Docker uses compiled Go MCP binaries rather than `go run`, reducing runtime image requirements.
- Completed baseline: Docker build and HTTP startup are covered by CI smoke tests.
- Completed baseline: Linux resource-control isolation is available through explicit `isolation: linux_cgroup` config when the host has a writable cgroup v2 parent.
- Completed baseline: release workflow builds Windows/Linux archives, publishes GHCR Docker images on version tags, emits `SHA256SUMS` for artifact integrity checks, generates `SBOM.spdx.json` for SPDX dependency disclosure, signs release files with Cosign/Sigstore keyless `.sigstore.json` bundles, and signs pushed Docker image digests. This was verified on the `v0.1.3` tag-triggered release.

## 4. Roadmap

### Phase A: Stabilize Agent Routing And Approvals

Goal: make normal chat feel reliable while keeping role permissions narrow.

Priority:

1. Keep `chat` read/network only in the shipped config.
2. Ensure write-intent requests route to `fixer` before write tools are exposed.
3. Ensure audit/reverse/security requests route to `auditor`.
4. Keep post-turn recovery back to chat unless a handoff, workflow, or approval is pending.
5. Add regression tests for Chinese and English routing phrases.

Success criteria:

- `chat` does not need write/exec permissions for ordinary code-edit requests.
- Users can ask naturally in Chinese or English and land on the correct agent.
- Tool approvals resume the current task instead of requiring a manual "continue".

### Phase B: Make Tooling Easy To Extend

Goal: users can add tools in Go, Python, or another language with minimal framework archaeology.

Priority:

1. Completed: add a "new MCP server" guide with protocol examples and workspace-root rules.
2. Completed: add a Python MCP test harness and examples for request/response validation.
3. Completed: add namespace collision warnings for duplicate short tool names.
4. Completed: add tool schema validation diagnostics to `/tools`.
5. Completed baseline: reject malformed third-party MCP tool metadata during discovery.
6. Continue: keep every file-oriented tool workspace scoped by default and expand regression coverage for third-party tools.

Success criteria:

- A developer can copy a small MCP template, add one tool, and see it in `/tools`.
- Bad schemas and unsafe path behavior fail early with clear errors.

### Phase C: Improve Skill Authoring And Orchestration

Goal: skills become reusable workflow modules, not just prompt fragments.

Priority:

1. Completed: add a skill authoring guide with required metadata, examples, and validation notes.
2. Completed: add built-in scaffolds for common skill types: coding, audit, web research, binary research, planning.
3. Completed baseline: expose `next_skills` as recommended follow-ups and execute them through `/workflow skill-chain`; continue with planner-selected conditional stages.
4. Completed: improve skill matching with scoring explanations in `/status`.
5. Completed baseline: make generated `workflows/<name>/workflow.yaml` files executable through `/workflow <name>`.
6. Completed: add tests for web and binary skill activation, routing, and declared tool metadata.
7. Add workflow run persistence for complex tasks: run id, stage state, stage outputs, artifacts, approvals, retries, cancellation, and replayable history.
8. Add reusable workflow templates for common complex flows: plan -> implement -> test -> audit, web research -> evidence collection -> risk report, binary triage -> strings/imports -> vulnerability assessment, docs generation -> review -> publish.
9. Add nested or reusable sub-workflow support only after the stage state model is stable.

Success criteria:

- New skills are predictable to create.
- Skill matches are explainable.
- Complex tasks can be decomposed through plan -> execute -> audit style flows without custom code each time for linear skill chains or graph-backed workflows.

### Phase C2: Add Multi-Agent Collaboration Primitives

Goal: move from role routing to configurable Agent teams.

Priority:

1. Define the core collaboration model: agent role, task ownership, typed message, handoff packet, shared blackboard entry, and decision record.
2. Add an internal message bus that supports agent-to-agent messages with provenance, timestamps, run ids, stage ids, and visible operator logs.
3. Add a shared blackboard persisted under the session/workflow run scope, with APIs for reading/writing evidence, artifacts, plans, unresolved questions, and final decisions.
4. Add reusable team templates: software task team, audit/security team, web research team, binary triage team, documentation team, and operations/runbook team.
5. Let workflows invoke teams as stages once durable workflow state exists, while preserving tool permission boundaries per agent.
6. Expose team state in CLI and HTTP: active owner, message timeline, blackboard changes, pending approvals, unresolved decisions, and final handoff summaries.

Success criteria:

- Developers can define a vertical multi-agent team through config before writing custom Go code.
- Agent collaboration is observable and resumable, not hidden inside a single LLM prompt.
- Shared state improves long-task quality without weakening workspace or tool approval boundaries.

### Phase D: Strengthen Reasoning And Verification

Goal: raise result quality by making the runtime enforce better reasoning loops.

Priority:

1. Completed baseline: refine prompts to reduce repeated plan text and force observation-driven progression.
2. Completed baseline: add optional verifier passes for code changes, audits, and vulnerability research through configured `verifier` settings.
3. Completed baseline: add better recovery after invalid tool calls and partial tool failures.
4. Completed baseline: track task stages explicitly in stream/session output: inspect, plan, modify, verify, summarize.
5. Completed baseline: add final-summary enforcement when iteration budget is close to exhausted.

Success criteria:

- Fewer repeated responses.
- Fewer unfinished tool loops.
- Clearer final answers after tool use.
- Better quality on long coding and security-analysis tasks.

### Phase E: Product UX Polish

Goal: make the CLI pleasant without hiding framework state.

Priority:

1. Completed baseline: improve git-like write logs with status, line ranges, added/deleted counts, compact colored hunks, and multi-file write summaries.
2. Completed baseline: add progress indicators for long LLM calls and pending tool-call generation.
3. Completed baseline: show per-turn LLM token usage totals in the CLI when usage is available from the provider.
4. Completed baseline: improve command output layout for `/skills`, `/tools`, and `/agents` with summaries, grouping, and warning counts.
5. Completed baseline: add optional history/fuzzy completion for interactive prompt input.
6. Completed baseline: keep HTTP/SSE behavior aligned with CLI stream events for normal turns, workflow runs, and approval resumes.
7. Completed CLI baseline: add workspace-required task gating to CLI; pure chat can continue with an unconfirmed current-directory default, but file/project/workflow operations prompt for confirmation.
8. Completed CLI baseline: add `/workspace status|confirm|clear|use <path>`; dynamic workspace switching and platform-aware folder picker remain future work.

Success criteria:

- Operators can tell exactly what the agent is doing.
- File modifications are readable before and after approval.
- CLI defaults feel usable without custom configuration.
- Operators cannot accidentally run file-affecting tasks against the wrong workspace.

### Phase F: Cross-Platform Deployment

Goal: make GoFlow easy to run on Windows, Linux, and Docker without changing framework internals.

Priority:

1. Completed baseline: support Windows local startup with `run-goflow.example.cmd`.
2. Completed baseline: support Linux local startup through documented shell commands.
3. Completed baseline: add `--config` so deployments can select environment-specific runtime config.
4. Completed baseline: add Dockerfile, docker-compose, and Docker-specific runtime config using compiled MCP binaries.
5. Completed baseline: add CI smoke tests for Linux and Docker image build.
6. Completed baseline: add Linux resource-control isolation behind explicit opt-in config where available.
7. Completed baseline: add release archives and GHCR image publishing workflow for version tags.

Success criteria:

- Developers can run the same runtime on Windows and Linux.
- Docker users can start HTTP mode with one documented command.
- Containerized MCP servers use explicit commands and workspace mounts.
- Deployment config does not require broad host filesystem access.

### Phase G: HTTP Console And Visual Operations

Goal: make HTTP mode a full operator surface, not just a JSON/SSE transport.

Priority:

1. Define a UI/API parity matrix against CLI commands and stream events.
2. Completed baseline: add workspace lifecycle APIs: current workspace, select workspace with restart-required response, confirm current directory, clear workspace, and workspace-required error responses for unsafe operations.
3. Completed baseline: add a modular Agent Studio frontend at `/console` and `/workflows`, with overview, visual workflow Studio, agent-selectable playground, workspace, approvals, resource catalog, observability, settings modules, and Chinese/English language switching.
4. Completed baseline: make HTTP approval behavior match CLI for approve, deny, approve-and-remember, and streamed resume. Continue with workflow-scoped approve-all UX.
5. Completed baseline: add `@file` reference support and workspace file suggestions to HTTP requests with the same workspace-root checks and visible read-style stream events. Continue with richer attachment chips and preview UX.
6. Completed baseline for skills: add visual skill creation/update with generated `SKILL.md` validation and hot reload. Continue with visual config editing for providers, agents, MCP tools, and update settings, with validation before writing YAML or scripts.
7. Add workflow run persistence and Studio run history: run id, stage logs, artifacts, approvals, retries, cancellation, replay, and diff review.
8. Continue polished workflow authoring interactions: edge handles, branch labels, richer node templates, validation overlays, import/export, template gallery, zoom/pan, and better run-state overlays on the canvas.
9. Add tests that compare CLI-visible behavior and HTTP/SSE behavior for approvals, workflow runs, token usage, task stages, write diffs, and workspace-required failures.

Success criteria:

- A user can operate GoFlow from a modern browser Studio without losing CLI-level visibility or safety.
- The visual workflow page can author workflows, persist them as YAML, and inspect live runs.
- Workspace selection is explicit, visible, and enforced before file-affecting operations.

### Phase H: Vertical Agent Kits And Visual Composition

Goal: make GoFlow useful as a platform for domain-specific Agent products.

Priority:

1. Define a packaged vertical kit format that can include agents, prompts, skills, tools, workflows, policies, UI metadata, examples, and validation tests.
2. Add kit scaffolding commands and HTTP UI flows for creating customer support, software engineering, data analysis, operations, security, and documentation Agents.
3. Support both code-state and config-state editing: YAML files stay versionable, while HTTP pages can edit and validate the same definitions.
4. Add import/export for Agent/Skill/Tool/Workflow kits so teams can share reusable vertical solutions.
5. Add compatibility checks that validate kit-defined tools, skills, agents, workflow graphs, required environment variables, and workspace permissions before activation.

Success criteria:

- A developer can create a vertical Agent package without learning the entire framework internals.
- A team can operate and customize the same package from the browser or from version-controlled files.
- Framework extension remains modular enough for Go, Python, and external MCP tools.

## 5. Immediate Next Actions

1. Harden the new HTTP Agent Studio baseline: add visual config editing, richer onboarding, workflow templates, edge-handle authoring, run history, diff review, and frontend regression tests.
2. Strengthen complex-task workflows with durable run state, stage artifacts, retries, cancellation, checkpoints, and replayable run history.
3. Define the multi-agent collaboration primitives: message bus, shared blackboard, handoff packets, team templates, and visible team state.
4. Keep Linux cgroup deployment docs and tests aligned with real-world cgroup provisioning requirements.
5. Strengthen implementation-request nudges so agents act once enough context has been gathered.

## 6. Execution Principles

1. Keep workspace boundaries strict.
2. Prefer role-appropriate routing over broad permissions.
3. Treat tool results as observations and preserve them through resume paths.
4. Add tests for lifecycle and routing bugs before expanding features.
5. Keep skills concise and evidence-oriented.
6. Document implemented behavior only after it is verified.

