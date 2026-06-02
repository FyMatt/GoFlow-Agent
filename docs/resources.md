# Resources And Settings Guide

This guide explains what the main GoFlow resources do, where they live, and how they fit together in CLI and Web Studio.

Use this guide when you are trying to understand what a resource is and how
resources connect. If you are trying to configure workflow nodes in Studio, go
to [Workflow Graphs](./workflows.md). If you are changing provider, Agent, or
MCP runtime settings, go to [Configuration](./configuration.md).

## Runtime Home And Workspace

GoFlow keeps two roots separate:

- `runtime home`: the GoFlow installation or repo directory. This holds configs, agents, skills, MCP definitions, workflows, templates, schemas, kits, and docs.
- `workspace root`: the target project directory. This is the only root that file tools, `@file` references, generated files, and workflow mutations may touch.

Pure chat can run without a confirmed workspace. Any file task, command task, `@file` reference, or workflow run needs a confirmed workspace.

## Web Studio Starting Path

For new users, start from **Resources -> Starter kits** instead of editing each
resource by hand.

The recommended path is:

1. Choose `multi-domain-agent`.
2. Use **Create linked starter** to materialize the Kit and its referenced
   Agent, Skill, Tool, Workflow, Workflow Template, Team Template, and Policy
   Rule files.
3. Open Workflow Studio and run the recommended workflow, usually
   `multi-domain-intake-router`.
4. Edit individual resources only after the linked starter exists.

The Resources page also shows a resource map:

- Kit packages a scenario.
- Agent controls model, permissions, and tool policy.
- Skill describes reusable expert procedure.
- Tool provides external capability.
- Workflow connects stages, data flow, approvals, and evidence.
- Team Template defines multi-agent handoffs.
- Policy Rule guards quality or risk gates.

Kit cards also show two practical guides:

- **Recommended run path** lists the Agent, Workflow Template, Team Template,
  and primary Skill to use together. Click a resource chip to focus that saved
  resource on the Resources page.
- **Resource chain** shows the normal execution chain after the Kit is
  materialized: Agent -> Skill -> Tool -> Workflow -> Team -> Policy. The
  numbers tell you how many linked resources the Kit contains.

Use **Open workflow** on a Kit card when you want to open the recommended
workflow template directly in Workflow Studio. The template opens as a new
editable graph, so you can inspect node inputs, outputs, approvals, and evidence
rules before saving your own copy.

## Built-in Domain Starters

The built-in kits are meant to be copied, materialized, and customized. They are
not isolated examples; each one references the agents, skills, tools, teams,
workflow templates, and policy rules that make the domain useful.

The repository checks these links with `python scripts/validate_resource_links.py`.
When you add or change a production starter, make sure the Kit and its scaffold
preset reference the same concrete Agent, Skill, Tool, Workflow Template, Team
Template, and Policy Rule names. If an example says to run a workflow, that
workflow or template should also be listed by the Kit so Web Studio can explain
the relationship before the user materializes it.

| Kit | Maturity | Primary agent | Main workflow template | Team template | Typical customization |
| --- | --- | --- | --- | --- | --- |
| `multi-domain-agent-kit` | production-ready | `chat` | `multi-domain-intake-router` | all domain teams | adjust branch selection, branch pruning, max-parallel limits, per-branch tool allowlists, join conflict diagnostics, and final handoff requirements |
| `software-engineering-kit` | production-ready | `software-engineer` | `engineering-parallel-delivery`, `plan-fix-audit`, `software-team-review-gate` | `software-task-team` | adjust coding, test, review, parallel worker delivery, and approval policy |
| `web-security-kit` | production-ready | `web-security-researcher` | `web-research-risk`, `authorized-red-team-validation` | `web-research-team` | tune target intake, evidence collection, risk gates, and authorized red-team validation |
| `security-research-kit` | production-ready | `security-researcher` | `security-audit-evidence-gate`, `authorized-red-team-validation` | `audit-security-team` | tune scope confirmation, disallowed exploitation blockers, severity rules, offensive-validation boundaries, red/blue handoff, and disclosure-safe report shape |
| `binary-analysis-kit` | production-ready | `binary-analyst` | `binary-triage` | `binary-triage-team` | tune static evidence tools, format/section summary, entropy/packing hints, symbol hints, bounded artifact windows, unsafe dynamic analysis blockers, and review gates |
| `documentation-kit` | production-ready | `documentation-specialist` | `docs-review-publish` | `documentation-team` | edit doc style, command accuracy, link integrity, bilingual parity, redaction, publish checklist, and acceptance criteria |
| `operations-runbook-kit` | production-ready | `operations-specialist` | `operations-runbook` | `operations-runbook-team` | customize prechecks, rollback, approval gates, and network-device planning contracts |
| `customer-support-kit` | production-ready | `support-specialist` | `customer-support-triage` | `customer-support-team` | adjust ticket context, product area, customer impact, privacy flags, response policy, escalation rules, and reviewer quorum |
| `agent-framework-kit` | production-ready | `framework-extension-architect` | `agent-framework-extension`, `engineering-parallel-delivery` | `framework-extension-team` | build and materialize a new vertical Agent/Skill/Tool/Workflow/Team/Policy/Kit package |

Recommended workflow:

1. Inspect the kit card in Web Studio or run `/kits <kit-name>`.
2. Materialize it with **Create linked starter** or `/new-kit <preset> <name> --materialize`.
3. Review the generated files and restart or reload as instructed.
4. Fork the workflow template when you want to change the graph without
   modifying the embedded default.

If a Kit card looks incomplete, run:

```bash
python scripts/validate_resource_links.py
```

The check reports the exact Kit or scaffold preset that points to a missing
resource.

## Professional Vertical Pack Contract

A vertical domain is considered professional only when its resources encode the
domain rules, not just a prompt. A professional pack should include linked
Agent, Skill, Tool/MCP, Workflow Template, Team Template, Policy Rule, Kit,
docs, and Web copy resources.

Each professional pack should declare:

- supported task types and required inputs
- tool boundaries and risky-action approval gates
- evidence artifacts and acceptance criteria
- quality gates, recovery path, and final report shape
- token strategy, context budgets, and model routes
- Simple mode guidance for ordinary users
- Expert mode fields for developers to inspect and extend the pack

`scripts/validate_resource_links.py` checks production starter links and now
also validates professional-pack metadata where a Kit declares professional
maturity. Keep resources synchronized when changing a domain pack; a workflow
template that requires a new browser, network, device, or analysis tool should
be reflected in the related Agent, Skill, Policy, Team, Kit, and scaffold
preset.

The same validator also prints a domain capability matrix summary. It groups
saved Kits and scaffold presets by domain, tracks the highest maturity level
seen for each domain, and fails when a professional or production-ready pack
drifts between the saved Kit and the starter preset.

Production-ready validation currently requires domain-specific MCP tools,
safety contract terms, tests, config, docs, release/deployment validation, and
evidence that materialized resources expose activation guidance. The current
production-ready packs are `operations-runbook-kit`, `web-security-kit`,
`software-engineering-kit`, `agent-framework-kit`, `security-research-kit`,
`binary-analysis-kit`, `documentation-kit`, `customer-support-kit`, and
`multi-domain-agent-kit`.

The operations pack includes `network_tools` for device work. The planning
tools validate authorized scope, allowed hosts, credential references, command
allowlists, dry-run status, rollback, and audit evidence without connecting to
devices. `network_tools/device_restconf_live_apply` adds a narrow controlled
HTTPS RESTCONF/API live connector, but it only sends a request when the workflow
stage is live-ready, the tool is listed in `allow_live_tools`, the operator has
provided approval/rollback/allowlist/credential evidence, and
`GOFLOW_NETWORK_TOOLS_ENABLE_LIVE=1` is present in the MCP server environment.
Raw SSH and NETCONF apply are still separate future connectors.

Security kits now include both defensive and authorized offensive resources.
`web-vulnerability-research` and `vulnerability-research` cover defensive
evidence collection and remediation analysis. `authorized-red-team-validation`
adds attack-path and exploitability validation under written authorization,
allowed hosts, allowed actions, exclusions, rate limits, non-destructive canary
evidence, blocked-action reporting, and blue-team handoff. It is not a default
unsanctioned attack tool; active validation must remain scoped and approval
gated. The security packs also record disallowed exploitation, missing
authorization, weak evidence, unresolved exploitability, red/blue handoff, and
disclosure-safe reporting as inspectable workflow diagnostics.

The binary analysis pack is static by default. It uses
`python_notes/binary_format_summary`, `binary_entropy_map`,
`binary_symbol_hints`, `binary_strings`, `hex_preview`, and
`binary_extract_window` for static evidence, format/section summary,
import/export hints, entropy/packing hints, symbol/function clues, offset
references, and bounded artifacts. Unsafe dynamic analysis stays blocked unless
the operator supplies a sandbox plan, approval, and artifact isolation.

The documentation pack now treats publish readiness as a quality gate. Review
stages call out audience fit, command accuracy, link integrity, bilingual
parity, version/platform notes, redaction, stale references, and publish
approval. `python_notes/markdown_link_check` checks local Markdown links and
anchors without network access, and `redaction_check` catches sensitive example
or incident data before publication.

The customer support pack structures ticket context before drafting. It tracks
product area, customer impact, SLA/priority, privacy flags, legal/billing/
security escalation, customer-facing replies, internal notes, source
attribution, and reviewer quorum. The Workflow Studio graph and selected-node
diagnostics should make privacy, escalation, missing-information, and reviewer
quorum blockers visible before handoff.

The `multi-domain-agent-kit` starter is also engineered for parallel domain
delivery. The recommended workflow first decomposes the request, then activates
only the selected domain workers, then joins, audits, and hands off a compact
report. Simple mode keeps the intake and final outcome visible. Expert mode
adds active branches, branch contracts, branch-selection reason, branch
pruning, max-parallel decisions, per-branch tool allowlists, model routes, cost
attribution, token budgets, artifact refs, branch blockers, missing worker
artifacts, and join conflict diagnostics.

## Memory And Learned Solutions

GoFlow memory is summary-first. The framework keeps full details in files or
artifacts, then injects compact summaries and refs into prompts.

Durable memory paths:

- `.goflow/memory/project.md`: workspace profile and stable operating facts.
- `.goflow/memory/tasks/*.json`: completed task summaries.
- `.goflow/memory/errors.json`: recurring errors, root causes, fixes, and
  verification commands.
- `.goflow/memory/solutions.json`: recurring problem signatures, decisions,
  correct solutions, applicability, invalidation rules, confidence, and usage
  counts.
- `.goflow/index/files.json`: file summaries, hashes, symbols, and usage
  metadata.

When a task records key decisions or reusable lessons, GoFlow derives a compact
solution record. Later searches and prompt retrieval can include a matching
solution block as `content=decision`; the model is instructed to reuse that
decision when it still applies, instead of asking the operator to make the same
choice again. This keeps the framework improving with use while preserving the
token strategy: only the top matching solution summaries are injected, and the
full history stays behind refs. If the current task matches a solution's
`invalid_when` rule, GoFlow records an omission diagnostic and does not inject
or count that solution as reused.

CLI surfaces:

- `/memory solutions`: list learned decisions, correct solutions, verification,
  applicability, invalidation, confidence, and use count.
- `/memory solution retire <id> [--reason <text>]`: keep a stale solution
  auditable while removing it from search and prompt retrieval.
- `/memory solution supersede <id> --superseded-by <id> [--reason <text>]`:
  retire a stale solution and link it to the replacing active solution.
- `/memory solution restore <id>`: make a retired solution active and
  retrievable again.
- `/memory search <query>`: searches project, context, tasks, errors,
  solutions, and file summaries.

Web Studio Memory shows learned solutions directly. Simple mode focuses on the
problem, chosen solution, and verification. Expert mode additionally shows the
solution ID, applicability, invalidation, last-used timestamp, use count,
retired/superseded state, lifecycle actions, and a compact governance graph for
replacement chains.

## File Content Rule

When GoFlow reads workspace text files, it always treats them as UTF-8 text. UTF-8 BOM is allowed and stripped. If the file is not valid UTF-8, use a binary inspection tool instead of `read_file` or `@file`.

## Core Settings

The main runtime config is `configs/goflow.yaml`.

### `agent`

Controls the default agent runtime.

- `name`: banner label and default runtime name.
- `max_iterations`: maximum reasoning or tool loop count.
- `timeout`: overall runtime timeout.

### `default_agent`

The agent used when the user does not switch to another profile.

### `skill`

Controls skill discovery.

- `directory`: base folder for `SKILL.md` packs.
- `match_threshold`: how strict skill matching should be.
- `hot_reload`: whether skill files reload automatically.

### `audit`

Controls audit visibility.

- `enabled`: turn audit logging on or off.
- `redact_content`: hide or keep sensitive content.
- `show_trace_in_cli`: print trace details in the terminal.

### `verifier`

Routes verification work to a separate agent/model if configured.

- `enabled`: enable the verifier pass.
- `agent`: agent profile used for verification.
- `modes`: which runtime modes use the verifier.
- `max_tokens`: verifier budget.

### `cost_control`

Cost-control helpers.

- `router.enabled`: use a cheaper model for request routing.
- `router.provider`, `router.model`: optional override target.
- `router.max_tokens`: keep classification short.
- `summarizer.enabled`: summarize long tool loops or noisy context.
- `summarizer.provider`, `summarizer.model`: optional override target.
- `summarizer.max_tokens`: summary budget.

### `tool_risk_policy`

Controls approvals for risky tools.

- `require_approval_for_unsandboxed_risky_tools`: require approval.
- `disable_remember_for_unsandboxed_risky_tools`: do not persist approvals.
- `reject_unsandboxed_risky_tools`: hard-reject unsafe tools.

### `session`

Controls history persistence.

- `max_history`: how much conversation history to keep.

### `log`

Controls console formatting.

- `level`: log verbosity.
- `format`: plain text or structured output.

## Resource Types

### Providers

Path: `configs/providers/*.yaml`

Use this for model endpoints.

`base_url`, `api_key`, and `model` are required before a Provider can serve
Agent runs, but they can be blank during first startup so Web Studio can open
and guide setup. `fallback_provider` must still point to an existing Provider
when it is set.

Provider resources saved from Web Studio are written to this YAML path. Literal
API keys are stored in plain text, so versioned Provider files should prefer
environment references such as `${GOFLOW_API_KEY}` for shared or release use.

Typical fields:

- `provider`
- `base_url`
- `api_key`
- `model`
- `fallback_provider`
- `timeout`
- `temperature`
- `max_tokens`
- `retry_count`
- `retry_backoff`

Use providers when you want separate primary and backup model behavior.

### Agents

Path: `configs/agents/*.yaml`

Use this for role-specific behavior.

Typical fields:

- `name`
- `mode`
- `provider`
- `model`
- `system_prompt`
- `tool_policy`
- `allowed_tool_kinds`
- `allowed_tools`
- `max_iterations`
- `timeout`

Use agents to separate planner, fixer, auditor, support, security, and domain specialist behavior.

### Skills

Path: `skills/<skill-name>/SKILL.md`

Use this for reusable procedures, expertise, and tool guidance.

Common subfolders:

- `agents/`: helper prompts or agent snippets
- `assets/`: reference files or example data
- `references/`: supporting docs
- `scripts/`: deterministic helper scripts
- `templates/`: output templates

Skill content should explain:

- what the skill does
- when to use it
- which tools it expects
- what output shape it should produce
- whether it can call helper scripts

### MCP Tools / Servers

Path: `configs/mcp_servers/*.yaml`

Use this for file tools, web tools, Python helpers, or custom tool servers.

Typical fields:

- `command`
- `args`
- `env`
- `enabled`
- `workdir`
- `isolation`
- `isolation_options`
- `allowed_commands`

Use `isolation: container` for stronger sandboxing when the tool can write, execute, or access network resources.

### Workflows

Path: `workflows/<name>/workflow.yaml`

Use this for executable graphs.

Common stage fields:

- `name`
- `node_type`
- `agent`
- `skill`
- `tool`
- `model`
- `params`
- `input`
- `outputs`
- `context`
- `next`
- `routes`
- `cases`
- `condition`
- `switch_on`
- `approval`
- `artifacts`
- `acceptance_criteria`

Workflows are best when each stage has a clear input contract and a clear output contract.
For complex engineering workflows, also declare a context budget contract:
`context.max_tokens` for selected upstream context and expert fields
`context.prompt_max_tokens`, `context.request_max_tokens`,
`context.inputs_max_tokens`, and `context.parameters_max_tokens` for the full
stage prompt envelope. Use `params.worker_contract: engineering_v1` on bounded
worker stages so downstream nodes can consume stable `summary`,
`changed_files`, `evidence`, `verification`, `blockers`, and `next_actions`
instead of raw prose.

### Workflow Templates

Path: `templates/workflows/*.yaml`

Use this for reusable workflow blueprints.
GoFlow ships built-in workflow templates from embedded YAML files under
`internal/agent/templates/workflows/*.yaml`; runtime files in
`templates/workflows/*.yaml` add new templates or override shipped templates by
name.

Templates are ideal when you want a starter graph for:

- planning
- software implementation
- parallel engineering delivery with stage-level model routing
- bounded plan-implement-audit delivery
- web security review
- binary triage
- documentation delivery
- operations runbooks
- support handoff

Built-in workflow templates are quality-gated delivery starters. They include
acceptance criteria, replay artifacts, a quality or policy gate, and a final
report/handoff-style output so users can run them directly or fork them without
first repairing the graph.

The software delivery templates are also token-budgeted engineering examples:
stronger routes handle decomposition, aggregation, audit, and recovery; lower
cost worker routes execute bounded slices with stage-scoped tools, worker JSON
contracts, and artifact-first handoffs.

Workflow resources can already coordinate serious work without relying on one
large prompt. A graph can collect structured inputs, route to selected
branches, run multi-agent team stages, cap worker context, publish artifacts,
join only the active branches, evaluate policy or quality gates, pause for
approval or input, retry failed stages, continue incomplete model output, and
resume durable runs from Studio or the API. Studio shows stage status, budget
pressure, contract failures, quality failures, artifacts, branch/join
diagnostics, and execution-readiness blockers on the graph and selected node.

The current resource workflow boundary is intentionally conservative for real
external changes. Templates may plan, dry-run, produce operator runbooks, and
prepare evidence for live work, but GoFlow does not claim unmanaged SSH,
NETCONF, RESTCONF, cloud, or device mutation is safe just because a workflow
node says "execute". A true live connector should be added only with an
operator-owned target inventory, credential reference strategy, authorization
scope, rollback storage, command or target allowlists, audit evidence, and
approval policy.

For workflow stages that may become live later, use `stage.execution`:

- `mode: planning`, `dry_run`, `manual`, or `disabled` records the execution
  boundary without blocking normal graph execution.
- `mode: live` requires readiness evidence before the stage can call a model or
  tool. Missing approval, authorized scope, rollback, credential ref,
  allowlist, required params, or live-tool constraints blocks the stage with
  `contract_check=execution_readiness`.
- Workflow Studio surfaces the execution mode, readiness state, risk, boundary,
  missing items, and source reference so users can locate the exact node and
  field that stopped the run.

### Team Templates

Path: `templates/teams/*.yaml`

Use this for reusable collaboration shapes.
GoFlow ships built-in team templates from embedded YAML files under
`internal/agent/templates/teams/*.yaml`; runtime files in `templates/teams/`
add new teams or override built-ins by name.

Typical fields:

- `role_templates`
- `handoffs`
- `blackboard_templates`
- `quorum_presets`
- `output_contract`

Built-in team templates include role responsibilities, produced artifacts,
handoff artifacts, shared blackboard references, and output contracts so `team`
workflow nodes can be expanded into auditable multi-agent stages.

Team templates define how multiple agents coordinate before or inside a workflow.

### Policy Rules

Path: `policies/workflow_rules/*.yaml`

Use this for reusable gates that are easier to share than inline conditions.
Create starter rules with `/new-policy-rule <preset> <name>` or
`POST /api/resources/policy-rules/scaffolds/{preset}`. Built-in scaffold
presets are embedded from
`internal/scaffold/templates/policies/scaffolds/presets.yaml`; runtime homes can
add or override presets with `templates/policies/scaffolds/*.yaml`.

Typical fields:

- `name`
- `label`
- `operator`
- `description`
- `params`

Use policy rules for approval gates, risk gates, acceptance gates, and reusable control logic.
Built-in policy rule metadata shown by `/api/workflow-options` is embedded from
`internal/agent/templates/policy_rules/*.yaml`. That metadata defines labels,
descriptions, operators, and editable parameters for built-in guards. Runtime
custom rules under `policies/workflow_rules/*.yaml` add executable user-defined
rules with `source: custom`.

### Workflow Node Metadata

Paths:

- `templates/workflow_nodes/*.yaml`
- `metadata/workflow_nodes/*.yaml`

Use this to extend the workflow editor with extra labels, hints, examples, defaults, and fields for a node type.
GoFlow ships the built-in node catalog from embedded YAML files under
`internal/agent/templates/workflow_nodes/*.yaml`; runtime metadata files add
or override editor-facing details for those executable node types.

This is what powers the Studio node palette and inspector help.
Studio labels built-in metadata as built-in instead of showing embedded source
paths. Runtime override files are shown with a short override path so operators
can find the editable file without confusing it with a required node parameter.

### Expression Helper Metadata

Paths:

- `templates/expression_helpers/*.yaml`
- `templates/workflow_expressions/*.yaml`
- `metadata/expression_helpers/*.yaml`
- `metadata/workflow_expressions/*.yaml`

Use this to extend expression suggestions, signatures, examples, and help text for workflow fields.
Built-in helper metadata is embedded from
`internal/agent/templates/expression_helpers/*.yaml`; runtime metadata files
override editor-facing labels, signatures, args, examples, hints, and warnings.
Studio uses built-in labels for shipped helpers and only shows a short override
path for user-editable runtime helper files.

### Workflow Schema Resources

Path: `schemas/workflows/*.json`

Use this to persist observed output schemas from completed workflow runs.

This directory is optional and normally starts empty. Files here are reusable
workflow schema resources saved through Studio or the HTTP API, not built-in
workflow templates.

The active session catalog is still populated from retained workflow runs and
imports. Saved schema resources help later stages and editor hints understand
what upstream output looks like, and can be re-activated into the live session
catalog when needed.

### Kits

Path: `kits/<kit-name>/kit.yaml`

Use this for a versionable bundle of linked resources.

A kit can reference:

- agents
- providers
- skills
- tools
- workflows
- workflow templates
- team templates
- policy rules
- example bundles

Use kits when you want a domain starter pack that can be imported, exported, or scaffolded in one move.

The Web Studio Kit list shows both counts and concrete reference names through
`provider_refs`, `agent_refs`, `skill_refs`, `tool_refs`,
`workflow_template_refs`, `team_template_refs`, and `policy_rule_refs`. This is
how users can see what a saved Kit will actually activate before editing or
exporting it.

## How The Pieces Connect

Think of the stack like this:

1. A provider talks to the model.
2. An agent chooses the prompt shape and tool policy.
3. A skill gives the agent a repeatable domain procedure.
4. A tool performs the concrete action.
5. A workflow chains the stages together.
6. A team template coordinates multiple roles.
7. A kit packages the whole thing for reuse.

## CLI And Web Usage

The CLI and Web Studio use the same backend resource APIs.

- Use CLI commands when you prefer text workflows.
- Use Web Studio when you want visual editing, validation, resource browsing, and drag-and-drop workflow authoring.
- Saving a resource from either side updates the same file-backed resource in runtime home.

## Practical Starting Points

If you are new, start with these in order:

1. `configs/goflow.yaml`
2. `configs/agents/*.yaml`
3. `skills/<skill>/SKILL.md`
4. `configs/mcp_servers/*.yaml`
5. `templates/workflows/*.yaml`
6. `templates/teams/*.yaml`
7. `kits/<kit>/kit.yaml`

If you need a production starter for a domain, begin with a kit and then customize the agent, skill, tool, and workflow pieces that it references.
