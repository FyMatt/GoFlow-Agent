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

| Kit | Primary agent | Main workflow template | Team template | Typical customization |
| --- | --- | --- | --- | --- |
| `multi-domain-agent-kit` | `chat` | `multi-domain-intake-router` | all domain teams | add or remove domains, routing cases, and final handoff requirements |
| `software-engineering-kit` | `software-engineer` | `plan-fix-audit`, `software-team-review-gate` | `software-task-team` | adjust coding, test, review, and approval policy |
| `web-security-kit` | `web-security-researcher` | `web-research-risk` | `web-research-team` | tune target intake, evidence collection, and risk gates |
| `security-research-kit` | `security-researcher` | `security-audit-evidence-gate` | `audit-security-team` | tune scope confirmation, severity rules, and report shape |
| `binary-analysis-kit` | `binary-analyst` | `binary-triage` | `binary-triage-team` | add binary helper tools, output sections, and review gates |
| `documentation-kit` | `documentation-specialist` | `docs-review-publish` | `documentation-team` | edit doc style, publish checklist, and acceptance criteria |
| `operations-runbook-kit` | `operations-specialist` | `operations-runbook` | `operations-runbook-team` | customize prechecks, rollback, and approval gates |
| `customer-support-kit` | `support-specialist` | `customer-support-triage` | `customer-support-team` | adjust intake fields, response policy, and escalation rules |
| `agent-framework-kit` | `framework-extension-architect` | `agent-framework-extension` | `framework-extension-team` | build a new vertical Agent/Skill/Tool/Workflow/Team/Policy/Kit package |

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
- `params`
- `input`
- `outputs`
- `next`
- `routes`
- `cases`
- `condition`
- `switch_on`
- `approval`
- `artifacts`
- `acceptance_criteria`

Workflows are best when each stage has a clear input contract and a clear output contract.

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
