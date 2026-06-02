# Workflow Graphs

[English](./workflows.md) | [简体中文](./workflows.zh-CN.md)

GoFlow supports two workflow styles:

- built-in workflows such as `plan-fix-audit` and `skill-chain`
- runtime-home workflow graphs under `workflows/<name>/workflow.yaml`

These are exposed through two related but different catalogs:

- `/api/workflow-graphs` lists editable graph files only.
- `/api/workflow-options.workflow_executors` lists runnable workflow entries,
  including editable graphs and legacy compatibility executors such as
  `plan-fix-audit` and `skill-chain`.

If a valid graph is saved with the same name as a legacy executor, that graph
overrides the compatibility executor for future runs. Use this when you want to
customize a built-in workflow without changing Go source.

Use workflow graphs when a task needs named stages, explicit agent assignment, branch selection, or approval boundaries.

Workflow graphs are persisted runtime configuration. They are loaded when you
run them, so editing `workflows/<name>/workflow.yaml` or saving from the HTTP
editor changes the next execution without recompiling GoFlow.

## Create And Run

```text
/new-workflow release-check
/workflow release-check review the auth changes
```

The graph lives in runtime home:

```text
workflows/release-check/workflow.yaml
workflows/release-check/WORKFLOW.md
```

The active workspace is still where file tools read and write. Workflow files are framework configuration, not target-project files.

For all scaffold commands and generated-file verification steps, see [Scaffold Commands](./scaffolds.md).

For end-to-end custom extensions, see:

- `examples/extension-workflow`: a compact Python MCP server, read-only agent,
  custom skill, and graph workflow that composes the custom stage with a
  built-in audit stage.
- `examples/binary-analysis-kit`: a fuller materialized kit whose workflow
  passes data from a team context into a planning skill, through a policy gate,
  and then into either a report or revision branch.

## Visual Workflow Studio

Start HTTP mode:

```bash
go run ./cmd/goflow --workspace /path/to/workspace --http :8080
```

Open:

```text
http://127.0.0.1:8080/workflows
```

`/workflows` opens the same modular Agent Studio used by `/console`, focused on
the workflow canvas. It is intended to feel like a modern workflow platform
rather than a terminal page.

The Studio can:

- list built-in and custom workflow graphs
- create custom workflow graphs
- add stages from a node palette
- drag node types from the palette into the canvas
- drag stages on a canvas
- model visual `start` and `end` markers
- model executable `agent`, `skill`, `tool`, and `custom` nodes
- show visual edges between stages
- create custom edges by selecting a source node and then clicking a target node
- edit stage agent, skill, approval, next strategy, and next-stage edges in a side property panel
- edit optional node metadata such as `node_type`, `tool`, and key/value `params`
- save custom graphs back to `workflows/<name>/workflow.yaml`
- run the selected workflow through the same HTTP workflow endpoint and display stage/token/approval stream events

Workflow Studio has two authoring modes:

- **Simple mode** is the default. It is for ordinary users and keeps the
  authoring surface focused on the canvas, starter templates, task cards,
  resource pickers, quick input/output presets, and visual flow controls.
  Advanced inspector tabs, raw route maps, context contracts, execution-order
  internals, and run/debug panels stay hidden.
- **Expert mode** is persistent and intended for developers or advanced
  operators. It restores the full node library, all inspector tabs, raw params,
  route/case maps, context contracts, input/output maps, execution-order
  preview, and run diagnostics. It does not change the workflow schema; it only
  exposes more of the same graph model.

For faster authoring in the right-side inspector:

- Node metadata cards can apply a built-in default setup or a worked example to
  the current node.
- Advanced behavior guide cards now expose one-click example fillers for the
  matching field.
- Results and evidence now support both `artifacts` and
  `acceptance_criteria`, so quality checks can be authored directly in Studio.
- Results and evidence guide cards can add common report artifacts, evidence
  artifacts, contains checks, and exists checks without hand-writing YAML.

The canvas distinguishes ordinary flow from control semantics:

- control nodes show a compact "Control" summary on the node card
- branch edges are labeled with outcomes such as `Pass`, `Fail`, `True`, or
  `Default`
- loop and for-each body links are drawn as auxiliary body edges, separate from
  the normal "after the loop" path
- selecting or hovering a control node draws a scope frame around the stages it
  controls, so users can see the node's range without reading YAML

Node positions are saved as optional stage metadata:

```yaml
position:
  x: 80
  y: 120
```

Execution ignores `position`; it is only used by the editor.

Visual node metadata can also be saved:

```yaml
node_type: tool
tool: file_tools/write_file
params:
  goal: update project files
```

`start` and `end` nodes are visual-only. `condition`, `switch`, `router`,
`policy_guard`, `quality_gate`, `parallel`, `join`, `for_each`, `loop`, `sub_workflow`,
`checkpoint`, and `input_gate` nodes are control-flow nodes: they do not run
their own LLM stage, but they can choose the next executable path, fan out,
wait for branch completion, repeat an executable body stage, call a nested
workflow, publish input variables, pause for approval, or block the workflow.
For repeat nodes, `params.stage` is the controlled body stage and `next` is the
stage that runs after the repeat exits; these are intentionally different
relationships in both the runtime and the Studio canvas.
Executable body/stage nodes still need an `agent` and `skill`; `tool` and
`params` are included in the stage prompt as metadata.

Built-in workflows are shown for reference but cannot be overwritten or deleted.
Built-in workflow templates are file-backed resources embedded from
`internal/agent/templates/workflows/*.yaml`. Runtime files under
`templates/workflows/*.yaml` can add or override templates by name, and the same
catalog powers CLI `/workflow-templates`, Studio workflow template pickers, and
`/api/resources/workflow-templates`.

The first Studio baseline intentionally keeps the frontend dependency-free:
assets live under `internal/api/web` and are embedded into the Go binary. This
keeps release archives and Docker images self-contained while still allowing the
web UI to grow by adding separate view modules.

## Stage Schema

```yaml
name: release-check
description: Review and improve a change.
stages:
  - name: start
    node_type: start
    next: [triage]
    position: {x: 40, y: 120}
  - name: triage
    node_type: agent
    agent: planner
    skill: execution-plan
    model:
      provider: primary
      max_tokens: 1600
      temperature: 0.1
    approval: false
    outputs:
      plan: result.output
    artifacts:
      - name: implementation-plan
        kind: report
        title: Implementation plan
        ref: result.output
    next_strategy: select
    next: [implement, audit]
    position: {x: 80, y: 120}
  - name: implement
    node_type: tool
    agent: fixer
    skill: code-writing
    tool: file_tools/write_file
    input:
      approved_plan: stages.triage.outputs.plan
    approval: true
    retry:
      max_attempts: 2
    next: [audit]
    position: {x: 360, y: 120}
  - name: audit
    node_type: skill
    agent: auditor
    skill: code-audit
    next: [end]
    approval: false
    position: {x: 640, y: 120}
  - name: end
    node_type: end
    position: {x: 900, y: 120}
```

Fields:

- `name`: workflow name. It must match the folder name used by `/workflow <name>`.
- `description`: optional operator-facing purpose.
- `stages`: ordered stage list. The first stage is the entry point.
- `stage.name`: stage id used by `next` edges and status output.
- `stage.node_type`: optional type. Supported values include `start`, `agent`, `skill`, `tool`, `custom`, `condition`, `switch`, `router`, `policy_guard`, `quality_gate`, `parallel`, `join`, `for_each`, `loop`, `sub_workflow`, `checkpoint`, `input_gate`, `team`, and `end`.
- `stage.agent`: configured agent profile to run the stage.
- `stage.skill`: loaded skill name whose instructions are applied to the stage.
- `stage.tool`: optional tool metadata for a tool-focused stage. For executable
  workflow stages this also narrows the tool schemas exposed to the model for
  that stage. Use `params.tools` or `params.allowed_tools` for a comma-separated
  list when a stage should expose several exact tools.
  The resulting `prompt_budget` event records
  `tool_schema_estimated_saved_tokens`, so operators can see how much schema
  context the stage scope avoided.
- `stage.model`: optional per-stage model route override. `provider` selects a
  configured provider/client, `model` overrides the provider default model,
  `max_tokens` caps that stage response, and `temperature` overrides sampling.
  The Agent profile still owns mode, tool permissions, approval policy, and
  iteration limits. Use this for strong-model decomposition/audit stages and
  cheaper, lower-token worker stages.
- `stage.params`: optional key/value metadata included in the stage prompt.
  For bounded worker stages, set `worker_contract: engineering_v1` to request a
  compact JSON handoff with `summary`, `changed_files`, `evidence`,
  `verification`, `blockers`, and `next_actions`. The runtime extracts those
  keys as stable outputs when the model returns JSON.
- `stage.input`: optional map of input names to references such as `workflow.input`, `stages.plan.outputs.summary`, `stages.plan.outputs.<name>`, or `stages.plan.result.output`.
- `stage.outputs`: optional map of output names to result references such as `result.output`, `result.summary`, `result.findings`, `result.tool_results`, `result.changes`, `result.verification`, or JSON keys returned by the model such as `result.changed_files` and `result.blockers`.
- `stage.context`: optional context and prompt budget contract. `include`,
  `exclude`, and `max_tokens` select and cap upstream context. Expert budget
  fields `prompt_max_tokens`, `request_max_tokens`, `inputs_max_tokens`, and
  `parameters_max_tokens` cap the whole prompt, original request block, mapped
  input block, and parameter block. Truncation uses explicit markers and keeps
  final execution constraints intact.
- `stage.artifacts`: optional replay artifact declarations. Each item supports `name`, `kind`, `title`, `ref`, `summary`, `content`, and `metadata`. `ref`, `summary`, and `content` can use result references such as `result.output`, `result.summary`, `result.findings`, `result.tool_results`, `result.changes`, or `result.verification`.
- `stage.acceptance_criteria`: optional stage acceptance checks. Each item can
  declare `name`, `description`, `ref`, `contains`, `equals`, `exists`, and
  `expected`. The backend evaluates these checks after the stage runs, stores
  pass/fail evidence in `completed_stages[].acceptance`, and emits acceptance
  artifacts for replay.
- Required output contracts can block or route a stage after execution. Set
  `params.contract_required: "true"` or `params.on_contract_fail: block` to
  enforce missing acceptance, required verification/evidence, declared
  artifacts, mapped `outputs`, JSON worker output, required JSON keys, and
  required sections. Contract failures emit `contract_validation_failed` with
  `contract_check`, `contract_checks`, `source_ref`, and `source_refs`, so
  Studio can highlight the exact node and missing output instead of burying the
  reason in raw logs.
- Contract failures can also use a configured model escalation route. Set
  `params.on_contract_fail: escalate` plus one or more of
  `escalate_provider`, `escalate_model`, `escalate_max_tokens`, and
  `escalate_temperature` (aliases `model_escalation_*` and
  `contract_fail_*` are also accepted). The runtime reruns only the failed
  stage with that route, emits `model_escalated` and `stage_retry`, stores
  `model.escalated`, `model.route`, and `model.escalation_ref` metadata, then
  resumes downstream stages in the same workflow run. If no auto-escalation is
  configured, a blocked run with escalation params exposes an operator
  `escalate_model` action.
- `stage.policy`: expression used by `node_type: policy_guard`. It supports the same baseline expression forms as `stage.condition`.
- `stage.condition`: expression used by `node_type: condition`. Supported baseline forms include truthy references, `exists(ref)`, `has(ref)`, `contains(ref, "text")`, `in("text", ref)`, `matches(ref, "regex")`, `any(ref[, "text"])`, `all(ref[, "text"])`, `len(ref) > 0`, `risk_rank(ref) >= 4`, and comparisons such as `ref == "xss"` or `ref >= 2`. References can address typed nested values such as `stages.collect.outputs.payload.risk` and array items such as `stages.collect.outputs.tags.0`.
- `stage.routes`: route map for condition nodes, typically `true: verify` and `false: report-clean`.
- `stage.switch_on`: reference evaluated by `node_type: switch` or `router`.
- `stage.cases`: switch route map. Case names are matched case-insensitively; `default`, `else`, or `*` can be used as fallback.
- `stage.retry.max_attempts`: optional bounded retry count for executable
  stages. Each retry attempt after the first emits a `stage_retry` diagnostic
  event with the stage, attempt source reference, and last failure reason when
  available.
- `stage.on_error`: optional fallback stage list used when an executable stage still fails after retries.
- `stage.approval`: if `true`, pause before entering the stage unless the run is pre-approved.
- `stage.next`: candidate next stage names. If omitted, GoFlow continues to the next stage in file order.
- `stage.next_strategy`: optional branch strategy. Supported values include `select`, `best`, `conditional`, `planner_select`, `planner-select`, `dynamic`, and `graph`.
- `stage.position`: optional visual editor metadata. It does not affect execution.

Agent profile permissions remain the final tool boundary. A stage cannot grant itself write, exec, or network access just by naming a skill.

## Node Configuration Quick Reference

Studio receives the same field metadata from `/api/workflow-options` that the
runtime uses for built-in node types. Use these fields as the primary form
guide:

| Field | Use it for | Common values |
| --- | --- | --- |
| `agent` | Which configured Agent profile runs an executable stage | `planner`, `software-engineer`, `web-security-researcher`, `binary-analyst` |
| `skill` | Which reusable procedure guides the stage | `execution-plan`, `code-writing`, `code-audit`, `web-vulnerability-research` |
| `tool` | Preferred MCP tool metadata for a tool-focused stage | `file_tools/write_file`, `web_tools/fetch_page_assets`, `python_notes/binary_strings` |
| `model` | Optional stage route and output budget override | `provider: primary`, `provider: backup`, `max_tokens: 900` |
| `input` | Map local input names to workflow references or literals | `plan: stages.plan.outputs.plan`, `target: workflow.input` |
| `outputs` | Publish stable names for later stages | `summary: result.summary`, `report: result.output`, `findings: result.findings` |
| `params` | Stable node settings, output contracts, team names, form metadata | `worker_contract: engineering_v1`, `rule: risk_at_least`, `fields: target:string:Target:required` |
| `context` | Selected context and prompt budget controls | `include: [previous.summary]`, `prompt_max_tokens: 4200` |
| `artifacts` | Mark first-class deliverables for replay/UI | `name: audit-report, kind: report, ref: result.output` |
| `acceptance_criteria` | Define checks for quality gates and replay evidence | `name: has-risk, ref: result.output, contains: risk` |
| `approval` | Pause before risky or high-impact stages | `true` for write, exec, external side-effect, or release stages |
| `retry.max_attempts` | Bound automatic retries for executable stages | `2`, `3` |
| `on_error` | Fallback stage names after retries fail | `revise`, `report-failure` |

Reference patterns:

- `workflow.input`: the original workflow request.
- `params.<name>`: a node parameter.
- `stages.<stage>.outputs.<name>`: a named output declared by an upstream stage.
- `stages.<stage>.output_values.<name>`: the typed JSON value when available.
- `stages.<stage>.result.output`: raw upstream output for older graphs.
- `result.output`, `result.summary`, `result.findings`,
  `result.tool_results`, `result.changes`, `result.verification`: current
  executable stage result references for `outputs`, artifacts, and criteria.
- `result.<json_key>`: when a stage returns a JSON object, named keys such as
  `result.changed_files`, `result.blockers`, or `result.next_actions` can be
  published directly.

Practical authoring pattern:

1. Put broad user context in an `input_gate` or `agent` planning node.
2. Publish one or two stable outputs from that node, such as `plan` and
   `scope`.
3. Feed only those named outputs into implementation, audit, or domain
   specialist nodes.
4. Route on structured outputs with `condition`, `switch`, or `policy_guard`.
5. Declare final reports, findings, diffs, or evidence as artifacts so replay
   and Studio do not have to parse prose.

For complex engineering tasks, the built-in `engineering-parallel-delivery`
template applies the same rule at model-routing level: a stronger route
decomposes the task and audits the integrated result, while cheaper worker
routes execute bounded slices in parallel with tight `context.max_tokens` and
`model.max_tokens` budgets. This improves reliability without copying full
history, raw logs, or large file contents into every worker prompt.

Use the quality-first token pattern for complex work:

1. Strong planner stage: decompose the request into independent slices,
   ownership boundaries, acceptance checks, and verification commands.
2. Bounded worker stages: run cheaper routes with `worker_contract:
   engineering_v1`, stage-scoped tools, and prompt budgets.
3. Artifact-first join: persist full worker reports as artifacts but pass only
   summaries, changed files, verification, blockers, and artifact refs forward.
4. Strong aggregate/audit stage: merge worker evidence, detect conflicts, and
   gate on acceptance, verification, and evidence before the final handoff.

Studio keeps this usable for both audiences. Simple mode exposes templates,
task cards, resource pickers, context presets, and run status. Expert mode
shows model routes, raw maps, context include/exclude lists, prompt budget
fields, artifacts, acceptance criteria, and run diagnostics for developers.

## How To Use Each Node Type

The simplest mental model is:

```text
collect input -> run work -> publish outputs -> route/gate -> produce artifact
```

Every executable node normally needs `agent` and `skill`. Control nodes usually
read upstream output through `condition`, `switch_on`, `policy`, `params.ref`,
or `input`, then choose the next node through `routes`, `cases`, or `next`.

### `start` And `end`

Use these as visual markers in Studio. Runtime execution follows stage order
and `next` edges; the markers do not call a model.

```yaml
- name: start
  node_type: start
  next: [collect]
- name: end
  node_type: end
```

Common fields:

- `next`: where the visual start points.
- `position`: canvas coordinates only.

### `input_gate`

Use this when the workflow needs structured user input before it can continue:
target URL, scope, severity, output type, credentials placeholder, release
window, or approval context.

```yaml
- name: collect
  node_type: input_gate
  params:
    manual: true
    prompt: Provide target and scope.
    fields: target:url:Target URL:required,scope:text:Scope:required
    strict: true
  outputs:
    target: result.output.target
    scope: result.output.scope
  next: [plan]
```

Common params:

- `manual: true`: pause and wait for user input.
- `prompt`: text shown to the operator.
- `fields`: compact field list, `name:type:label:required`.
- `fields_json`: richer JSON field schema.
- `required`: comma-separated required field names.
- `strict: true`: reject undeclared submitted keys.

Downstream references:

- `stages.collect.outputs.target`
- `stages.collect.outputs.scope`

### `agent`

Use this for a normal LLM stage where the selected Agent profile matters:
planning, implementation, audit, documentation, support, or domain routing.

```yaml
- name: plan
  node_type: agent
  agent: planner
  skill: execution-plan
  input:
    target: stages.collect.outputs.target
    scope: stages.collect.outputs.scope
  outputs:
    plan: result.output
    summary: result.summary
  next: [implement]
```

Common fields:

- `agent`: configured profile from `/agents`.
- `skill`: loaded skill from `/skills`.
- `input`: named values this stage should focus on.
- `outputs`: stable names for later stages.
- `approval`: set `true` before risky stages.
- `retry.max_attempts`: retry count for transient model/tool failure.
- `on_error`: fallback stage list.

### `skill`

Use this when the reusable procedure matters more than the Agent identity. It
still needs an Agent profile because permissions come from the Agent.

```yaml
- name: audit
  node_type: skill
  agent: auditor
  skill: code-audit
  input:
    changed_files: stages.implement.outputs.changes
  outputs:
    findings: result.findings
    audit_report: result.output
  next: [quality]
```

Good for:

- `code-audit`
- `web-vulnerability-research`
- `binary-vulnerability-research`
- `reverse-engineering`
- `execution-plan`

### `tool`

Use this when the stage should strongly prefer one MCP tool, but remember:
`tool` is guidance and metadata, not a permission override. Agent policy still
decides whether the tool can run and whether approval is required.

```yaml
- name: write-report
  node_type: tool
  agent: fixer
  skill: code-writing
  tool: file_tools/write_file
  input:
    report: stages.audit.outputs.audit_report
  approval: true
  outputs:
    tool_results: result.tool_results
  next: [end]
```

Common tool names:

- `file_tools/read_file`
- `file_tools/search_files`
- `file_tools/write_file`
- `web_tools/web_search`
- `web_tools/fetch_url`
- `web_tools/fetch_page_assets`
- `python_notes/binary_file_info`
- `python_notes/binary_strings`
- `python_notes/hex_preview`

### `team`

Use this to bring in a reusable multi-Agent team template. With
`params.execute: false` or omitted, it publishes team context. With
`params.execute: true`, GoFlow expands roles into executable stages.

```yaml
- name: team
  node_type: team
  params:
    team: software-task-team
    execute: "true"
    approval_preset: software-review
  input:
    goal: workflow.input
  outputs:
    handoff: result.summary
  next: [team-gate]
```

Common params:

- `team`: template name, such as `software-task-team`,
  `web-research-team`, `binary-triage-team`, `framework-extension-team`.
- `execute`: `"true"` to run roles, otherwise publish context only.
- `approval_preset`: optional preset consumed by `team_approval_gate`.

### `condition`

Use this for a true/false branch. It evaluates an expression and follows
`routes.true` or `routes.false`.

```yaml
- name: has-risk
  node_type: condition
  condition: contains(stages.audit.outputs.audit_report, "High")
  routes:
    true: verify
    false: report-clean
```

Common expressions:

- `exists(stages.audit.outputs.findings)`
- `contains(stages.audit.outputs.audit_report, "vulnerability")`
- `len(stages.collect.outputs.targets) > 0`
- `risk_rank(stages.audit.outputs.risk) >= 4`
- `stages.collect.outputs.domain == "web-security"`

### `switch` / `router`

Use this for more than two routes: domain routing, severity routing, or output
type routing.

```yaml
- name: route-domain
  node_type: switch
  switch_on: stages.collect.outputs.domain
  cases:
    software: plan-code
    web-security: web-review
    binary: binary-triage
    docs: docs-update
    default: general-plan
```

Common fields:

- `switch_on`: reference to evaluate.
- `cases`: map from expected value to target stage.
- Always include `default`, `else`, or `*`.

### `policy_guard`

Use this for reusable or named gates. It is better than `condition` when the
rule is shared across workflows, such as risk thresholds or team approvals.

```yaml
- name: risk-gate
  node_type: policy_guard
  params:
    rule: risk_at_least
    ref: stages.audit.outputs.findings
    minimum: high
  routes:
    allow: verify
    deny: report
```

Common rules:

- `expression`: evaluate `policy` or `condition`.
- `ref_truthy`: pass when `params.ref` is truthy.
- `contains`: pass when `params.ref` contains `params.needle`.
- `min_count`: pass when `len(params.ref) >= params.minimum`.
- `risk_at_least`: pass when severity is at least a threshold.
- `team_approval_gate`: pass when team approval packets satisfy a preset.

### `quality_gate`

Use this after work or audit stages to check acceptance criteria, artifacts,
and verification evidence before handoff.

```yaml
- name: quality
  node_type: quality_gate
  input:
    report: stages.audit.outputs.audit_report
  params:
    min_score: "80"
  routes:
    pass: handoff
    fail: revise
```

Upstream executable stages should declare `acceptance_criteria` and
`artifacts` so the quality gate has evidence to evaluate.

### `parallel` And `join`

Use `parallel` when branches can run independently, then converge with `join`.

```yaml
- name: split
  node_type: parallel
  next: [research, audit]
- name: research
  agent: planner
  skill: execution-plan
  next: [join]
- name: audit
  agent: auditor
  skill: code-audit
  next: [join]
- name: join
  node_type: join
  params:
    wait_for: research,audit
  next: [synthesize]
```

Common params:

- `parallel.params.concurrent: true`: opt in to stricter concurrent execution
  only for safe branch shapes.
- `join.params.wait_for`: comma-separated branch names.

### `for_each`

Use this to run one body stage once per item.

```yaml
- name: each-target
  node_type: for_each
  params:
    items_ref: stages.collect.outputs.targets
    stage: review-one
  next: [summarize]
- name: review-one
  agent: auditor
  skill: code-audit
  outputs:
    finding: result.output
```

Common params:

- `items`: comma/newline text or JSON array.
- `items_ref` / `ref`: reference to an upstream list.
- `stage` / `body`: executable stage to repeat.

The body stage receives iteration variables such as `iteration.item` and
`iteration.index`.

### `loop`

Use this for bounded improve-until-done cycles. Always set a max iteration
count.

```yaml
- name: improve-loop
  node_type: loop
  params:
    stage: revise
    until: contains(previous.raw_output, "ready")
    max_iterations: "3"
  next: [handoff]
- name: revise
  agent: fixer
  skill: code-writing
  approval: true
```

Common params:

- `stage` / `body`: executable body stage.
- `until`: expression checked after each iteration.
- `max_iterations`: hard stop.

### `sub_workflow`

Use this to call another workflow as a nested unit.

```yaml
- name: binary-subflow
  node_type: sub_workflow
  params:
    workflow: binary-triage
    request: stages.collect.outputs.target
  outputs:
    sub_run_id: result.sub_run_id
    summary: result.summary
  next: [handoff]
```

Common params:

- `workflow`: built-in or persisted workflow name.
- `request`: literal or reference passed to the child workflow.

If the child workflow pauses, the parent pauses until the child is resumed.

### `checkpoint`

Use this to insert a human review point without calling a model.

```yaml
- name: approve-release
  node_type: checkpoint
  params:
    prompt: Approve release execution?
  next: [release]
```

Common params:

- `prompt` / `message` / `reason`: text shown to the operator.

## Complete Pattern Example

This graph collects a target URL, fetches web assets, audits the output, gates
on risk, then either verifies findings or produces a clean report:

```yaml
name: web-risk-review
description: Collect a URL, inspect web assets, audit findings, and route by risk.
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields: target:url:Target URL:required,scope:text:Scope:required
      strict: true
    next: [fetch]

  - name: fetch
    node_type: skill
    agent: web-security-researcher
    skill: web-vulnerability-research
    input:
      target_url: stages.collect.outputs.target
      scope: stages.collect.outputs.scope
    outputs:
      evidence: result.output
      summary: result.summary
    artifacts:
      - name: collected-assets
        kind: evidence
        title: Collected web assets
        ref: result.output
    next: [audit]

  - name: audit
    node_type: skill
    agent: security-researcher
    skill: vulnerability-research
    input:
      evidence: stages.fetch.outputs.evidence
    outputs:
      findings: result.findings
      report: result.output
    acceptance_criteria:
      - name: has-scope
        ref: result.output
        contains: scope
    next: [risk-gate]

  - name: risk-gate
    node_type: policy_guard
    params:
      rule: risk_at_least
      ref: stages.audit.outputs.findings
      minimum: medium
    routes:
      allow: verify
      deny: clean-report

  - name: verify
    node_type: agent
    agent: auditor
    skill: code-audit
    input:
      findings: stages.audit.outputs.findings
    approval: true
    outputs:
      verification: result.verification
    next: [handoff]

  - name: clean-report
    node_type: agent
    agent: documentation-specialist
    skill: code-writing
    input:
      report: stages.audit.outputs.report
    outputs:
      report: result.output
    next: [handoff]

  - name: handoff
    node_type: quality_gate
    input:
      audit_report: stages.audit.outputs.report
      verification: stages.verify.outputs.verification
    routes:
      pass: end
      fail: clean-report

  - name: end
    node_type: end
```

## Data Flow And Control Flow

Each completed executable stage produces a structured workflow output:

- `summary`: bounded display summary
- `raw_output`: bounded raw agent output
- `variables`: values declared by `stage.outputs`
- `values`: typed values declared by `stage.outputs` when JSON types can be
  preserved
- `tool_results`: tool observations gathered by the stage
- `findings`, `changes`, and `verification`: structured result fields when available

Downstream executable stages automatically receive prior-stage summaries. Use
`stage.input` when a node needs a specific upstream value.

Workflow runs persist this data for replay and UI inspection. Each completed
stage snapshot includes:

- `inputs`: resolved `stage.input` values that were passed into the stage prompt
- `input_values`: typed resolved `stage.input` values for Studio and replay
- `outputs`: declared `stage.outputs` variables
- `output_values`: typed output values for Studio, replay, and downstream
  mapping
- `attempts`: how many tries were needed for an executable stage
- `node_type`, `skill`, `tool`, and `metadata`: authoring context for Studio
- `artifacts`: declared replay artifacts plus derived outputs such as stage
  text, tool results, findings, changes, and verification records

Workflow run action discovery is available at
`GET /api/workflow-runs/{id}/actions` and is also embedded in
`GET /api/workflow-runs/{id}/replay`. Each action declares `name`, `method`,
`path`, optional `stream_path`, `available`, `durable`, and `reason`. Actions
also include UI/protocol metadata such as `kind`, `supports_background`,
`supports_stream`, `accepts_body`, `requires_body`, `body_schema`, and `risk`
when relevant. Studio should use this instead of inferring buttons from status
strings or action names. For example, `awaiting_input` exposes a durable
`submit_input` action with `kind: manual_input`, `requires_body: true`, and a
body schema for the submitted `inputs` object.

The default `/actions` response remains the legacy action array. Add
`?envelope=1` to receive `meta.schema: "goflow.run_actions"`, status,
`needs_action`, `events_path`, replay/timeline/action paths, a recommended
action, counts, and the same action list in one payload.

Recovery actions are distinct so operators do not need to guess why a workflow
paused. `paused_need_more_budget` exposes `continue_output`, hard workflow
budget pauses expose `approve_budget`, and required contract failures with a
configured escalation route expose `escalate_model` at
`POST /api/workflow-runs/{id}/escalate-model` and
`/escalate-model/stream`. The model escalation action reruns the blocked stage
with the configured escalation model/budget route and continues downstream
stages in the same durable run. Studio surfaces the same route through the run
action list, selected-node inspector, graph chips, and timeline events so the
blocked node and recovery path stay visible.

Graph workflow
`checkpoint`, `manual_approval`, `approval_gate`, and executable stages with
`approval: true` expose a durable `approve_stage` action through
`POST /api/workflow-runs/{id}/approve` and `/approve/stream`. The built-in
`plan-fix-audit` workflow also exposes durable `approve_stage` while paused at
its pre-fix approval boundary, so Studio can approve it after reloading a saved
session.
`awaiting_tool_approval` exposes `approve_tool` / `deny_tool` when the
in-memory model/tool context still exists or the persisted run snapshot contains
enough context to rebuild it. When that context exists, the run also exposes
`approve_all_tools` at
`POST /api/workflow-runs/{id}/approve-tools` and `/approve-tools/stream`. That
action approves the current paused tool call and auto-approves later matching
tool calls only within the same workflow, stage, and tool name. If
`tool_risk_policy.disable_remember_for_unsandboxed_risky_tools` is enabled and
the pending tool is an unsandboxed write/exec/network/unknown-kind tool,
`approve_all_tools` is advertised as unavailable with a policy reason while
one-time `approve_tool` and `deny_tool` remain available. A tool is treated as
sandboxed for this policy only when the runtime
`tool_risk_policy.recognized_sandbox_boundaries` field includes a matching
boundary for that tool kind, for example `container` for broad risky tools or
`linux_netns` for network-kind tools only. If a legacy or corrupted run lacks
resumable context, `/actions` marks approval actions unavailable and Studio
should offer retry or cancel.
If `tool_risk_policy.reject_unsandboxed_risky_tools` is enabled, the same
unsandboxed risky tool calls are rejected before execution instead of entering a
new approval pause. Already-paused legacy runs still expose deny/cancel where
context exists, but approve actions are unavailable with a
`reject_unsandboxed_risky_tools` policy reason.

Control-flow nodes are also persisted as completed stage snapshots. They do not
call an LLM, but their output variables record the decision context:

- `kind`: `condition`, `switch`, `router`, `policy_guard`, `quality_gate`, `parallel`, `join`, `for_each`, `loop`, `sub_workflow`, `checkpoint`, `team`, or `input_gate`
- `route`: selected boolean route or switch case
- `value`: evaluated switch/router value or condition result
- `target`: selected next stage
- `passed`: condition result for condition nodes
- `status`: control-stage status such as `completed` or `blocked`
- `reason`: checkpoint prompt or policy block reason when present

`input_gate` also publishes `stage.params` and resolved `stage.input` values as
output variables, so downstream nodes can reference values such as
`stages.collect.outputs.target`.

Declared artifacts let workflow authors tell Studio which outputs are first-class
deliverables instead of relying only on automatic artifact inference. Declared
artifacts are still stored alongside automatic artifacts in `run.artifacts` and
`run.completed_stages[].artifacts`.

Every workflow artifact also carries lightweight provenance metadata for replay
and evidence graphs. Standard keys include `producer_stage`, `producer_agent`,
`producer_node_type`, `producer_skill`, `producer_tool`, `source_tool`,
`evidence_category`, `validation_status`, `related_files`, and `refs` when the
source data is available. Existing custom `metadata` remains intact, so Studio
can render evidence by producer, category, validation state, related file, or
consumed reference without changing the artifact response shape.

Acceptance criteria make stage quality checks first-class run data. Each check
is evaluated after the stage finishes and is persisted under
`completed_stages[].acceptance` with `status`, `actual`, `expected`, and
`reason`. Replay artifacts also include `kind: acceptance` entries so Studio can
render evidence without parsing prose.

```yaml
stages:
  - name: audit
    agent: auditor
    skill: code-audit
    acceptance_criteria:
      - name: reports-risk
        description: Audit output should mention risk.
        ref: result.output
        contains: risk
        expected: audit includes a risk statement
```

Artifact declaration example:

```yaml
stages:
  - name: audit
    agent: auditor
    skill: code-audit
    outputs:
      audit_text: result.output
      findings: result.findings
    artifacts:
      - name: audit-report
        kind: report
        title: Security audit report
        ref: result.output
        summary: result.summary
        metadata:
          audience: security
      - name: finding-data
        kind: evidence
        title: Structured findings
        ref: result.findings
```

When `input_gate` has `params.manual: true`, `params.wait_for_input: true`, or
`params.mode: manual`, the workflow pauses with status `awaiting_input` until an
operator submits values through the workflow-run input API. The submitted values
are persisted as that input gate's output variables and are available to
downstream nodes.

Manual input gates can expose a form schema through `pending_input_fields`.
Declare simple fields with `params.fields`:

```yaml
params:
  manual: true
  fields: target:string:Target:required,severity:select:Severity
  required: target
  field.severity.options: low,medium,high
  field.severity.default: medium
  field.target.description: Workspace area or target URL to review.
```

Field shorthand uses `name:type:label:required`. Supported baseline types are
`string`, `text`, `number`, `integer`, `boolean`, `select`, `url`, `path`,
`file`, `json`, `object`, and `array`. Field metadata can also be set with
`field.<name>.label`, `field.<name>.type`, `field.<name>.description`,
`field.<name>.required`, `field.<name>.default`, and
`field.<name>.options`. Resume requests are validated against required fields,
basic types, JSON object/array shape, and select options before the workflow
continues. Fields with `multiple: true` accept a JSON array or comma-separated
list and validate each item against declared options. Set `params.strict: true`
to reject undeclared input keys.

HTTP input submissions may send strings, numbers, booleans, arrays, or objects.
The backend validates them against the pending field schema, keeps
string-compatible `outputs` for prompt/reference compatibility, and also
persists typed `output_values` plus downstream `input_values` for Studio forms,
run replay, and later workflow nodes.

For richer Studio forms, declare `params.fields_json`, `params.input_fields`, or
`params.input_schema` as a JSON array, an object with `fields`, or a small
JSON-Schema-style object with `properties`. Supported field metadata includes
`name`, `label`, `type`, `description`, `placeholder`, `group`, `required`,
`default`, `options`, `rows`, `min`, `max`, `pattern`, `multiple`, and
`advanced`. Nested `children` / `fields` are flattened with dot names such as
`credentials.token` and carry the parent label as the `group`. HTTP input
submissions may send strings, numbers, booleans, arrays, or objects; the
backend validates them without losing their JSON types and stores typed
`input_values` / `output_values` alongside string-compatible `inputs` /
`outputs`.

```yaml
params:
  manual: true
  fields_json: |
    {
      "fields": [
        {"name": "target", "type": "url", "label": "Target URL", "required": true},
        {"name": "depth", "type": "integer", "min": "1", "max": "3", "default": "1"},
        {"name": "tags", "type": "select", "options": ["api", "auth", "db"], "multiple": true},
        {"name": "payload", "type": "object", "required": true},
        {"name": "evidence", "type": "textarea", "rows": 6, "pattern": "(?i)evidence"},
        {"name": "credentials", "label": "Credentials", "children": [
          {"name": "token", "type": "password", "required": true}
        ]}
      ]
    }
```

`policy_guard` evaluates `stage.policy` or `stage.condition`. If the rule
passes, it follows `routes.allow` / `routes.true` or the first `next` stage. If
the rule fails, it follows `routes.deny` / `routes.false` or the second `next`
stage. When no deny target exists, the workflow stops with status `blocked`.
For reusable guard configuration, set `params.rule` to a built-in or custom
rule exposed by `/api/workflow-options` under `policy_rules`:

- `expression`: evaluate `stage.policy` or `stage.condition`
- `ref_truthy`: pass when `params.ref` resolves to a truthy value
- `contains`: pass when `params.ref` contains `params.needle`
- `min_count`: pass when `len(params.ref) >= params.minimum`
- `risk_at_least`: pass when a severity reference is at least
  `info`, `low`, `medium`, `high`, or `critical`
- `team_approval_gate`: pass when prior executable team role approval/rejection
  packets satisfy a derived review gate status. Use `params.status` with
  `passed`, `pending`, `blocked`, or `not_blocked`; optional
  `params.approval_preset` / `params.review_preset` /
  `params.quorum_preset`, `params.approval_quorum`,
  `params.approval_roles`, and `params.reject_blocks` override metadata
  inherited from the team node. Set
  `params.wait_for_quorum: true` when a `status: passed` gate should pause for
  operator input instead of routing immediately while the quorum is still
  pending. The pending input form exposes `roles`, `decision`
  (`approve` or `reject`), and `comment`.

Custom policy rules live under `policies/workflow_rules/*.yaml` in the runtime
home. They are loaded into `/api/workflow-options` with `source: custom`,
`custom: true`, their underlying `operator`, and any declared `params` so Studio
can render a node-specific form. Rule names are matched leniently during
execution, so `critical-finding` and `critical_finding` resolve to the same
custom rule, while the saved/API name remains the YAML `name`.

HTTP clients can start from policy-rule presets instead of a blank YAML file:

- `GET /api/resources/policy-rules/scaffolds`
- `GET /api/resources/policy-rules/scaffolds/{preset}?name=<rule-name>`
- `POST /api/resources/policy-rules/scaffolds/{preset}`

The preset create endpoint accepts JSON or YAML with `name`, optional label,
description, reason, `defaults`, and `overwrite`. Presets include
`risk-threshold`, `truthy-reference`, `contains-text`, `minimum-count`,
`team-review-quorum`, and `expression`. The backend validates the generated
rule through the same save path used by `/api/resources/policy-rules/{name}`.
The preset catalog is also file-backed: built-ins are embedded from
`internal/scaffold/templates/policies/scaffolds/presets.yaml`, and runtime homes
can add or override presets with `templates/policies/scaffolds/*.yaml`.
Built-in policy rule metadata in `/api/workflow-options` is also file-backed:
the shipped rule labels, descriptions, operators, and parameter definitions are
embedded from `internal/agent/templates/policy_rules/*.yaml`. User-defined
runtime rules still live under `policies/workflow_rules/*.yaml`.

```yaml
kind: goflow.workflow_policy_rule
version: 2
min_supported_version: 1
name: critical-finding
label: Critical Finding
description: Pass when referenced evidence contains at least high severity.
operator: risk_at_least
defaults:
  ref: stages.audit.outputs.risk
  minimum: high
reason: referenced evidence did not meet high severity
params:
  - name: params.ref
    label: Evidence reference
    type: reference
  - name: params.minimum
    label: Minimum severity
    type: select
    options: [low, medium, high, critical]
```

Policy rule resources still accept the original unversioned YAML shape for
compatibility. Saves now write the v2 envelope shown above, and rules declaring
a future unsupported `version` or `min_supported_version` are rejected with a
clear compatibility error.

`quality_gate` is a dedicated control node for evidence-based workflow routing.
It evaluates prior stage acceptance criteria, verification results, declared
artifact evidence, failed stage statuses, and errored tool results. Passing gates
follow `routes.pass` or the first `next` stage; warning gates follow
`routes.warning` when provided, otherwise the normal pass path; failing gates
follow `routes.fail` or the second `next` stage. If a gate fails and has no fail
route, the workflow stops with status `blocked`.

Useful `quality_gate` params:

- `stage` / `stages`: optional stage name or comma-separated stage names to
  evaluate. Empty means all completed stages.
- `require_acceptance`: fail when no acceptance checks were recorded.
- `require_verification`: fail when no verification results were recorded.
- `require_evidence`: fail when no declared evidence artifacts were recorded.
- `min_score`: fail when the derived quality score is below this number.
- `allow_unknown`: set to `false` to fail on unknown acceptance/verification
  statuses.

The gate output variables include `quality_status`, `score`,
`acceptance_total`, `acceptance_failed`, `verification_total`,
`verification_failed`, `evidence_artifacts`, `failures`, and `warnings`, so
downstream stages can reference values such as `stages.quality.outputs.score`.

```yaml
stages:
  - name: audit
    agent: auditor
    skill: code-audit
    next: [quality]
    acceptance_criteria:
      - name: reports-risk
        ref: result.output
        contains: risk
  - name: quality
    node_type: quality_gate
    params:
      require_acceptance: "true"
      require_verification: "true"
      min_score: "80"
    routes:
      pass: publish
      warning: review
      fail: revise
```

Team review gate policy example:

```yaml
stages:
  - name: team
    node_type: team
    agent: planner
    params:
      team: software-task-team
      execute: true
      approval_preset: software-review
    next: [review-gate]
  - name: review-gate
    node_type: policy_guard
    params:
      rule: team_approval_gate
      team: software-task-team
      approval_preset: software-review
      status: passed
      wait_for_quorum: true
    routes:
      allow: handoff
      deny: revise
```

With `wait_for_quorum: true`, a pending gate pauses the run as
`awaiting_input`. Resume it through the same workflow-run input API used by
manual input gates:

```bash
curl -s -X POST http://127.0.0.1:8080/api/workflow-runs/{run_id}/input \
  -H "Content-Type: application/json" \
  -d '{"inputs":{"roles":"reviewer","decision":"approve","comment":"operator approved reviewer quorum"}}'
```

The backend records the submitted decision as a synthetic completed
`team_manual_approval` stage before re-evaluating the `team_approval_gate`, so
replay, TeamState, and downstream policy decisions all see the same approval
packet.

Supported custom-rule operators currently match the built-in policy operators:
`expression`, `ref_truthy`, `contains`, `min_count`, `risk_at_least`, and
`team_approval_gate`.
`defaults` fill missing stage params before evaluation. For `operator:
expression`, set `expression` to a policy template and use `{{param}}` or
`{{params.param}}` placeholders for default/stage params.

Expression and policy evaluation can use typed `output_values` when available.
For example, `len(stages.collect.outputs.tags) >= 2` counts array entries,
`contains(stages.collect.outputs.tags, "api")` checks array membership, and
`stages.collect.outputs.payload.risk == "high"` reads a nested object field.
Use `in("api", stages.collect.outputs.tags)` for exact membership,
`matches(stages.collect.outputs.payload.risk, "high|critical")` for regex
matching, `any(stages.collect.outputs.tags, "auth")` / `all(...)` for
collection-wide predicates, and `risk_rank(stages.collect.outputs.payload.risk)
>= 4` to compare severity text or numeric risk scores.
String-compatible `outputs` remain available for prompts and older workflow
definitions.

`parallel` fans out to every stage listed in `next`. By default those branches
are scheduled deterministically, which keeps tool approval and workspace writes
predictable. Set `params.concurrent: true` to opt in to goroutine-level branch
execution for direct branches, or for safe linear branch chains, that all flow
into the same `join` node and do not require checkpoint approval. The concurrent
path rejects unsafe shapes such as control nodes inside the branch, repeated or
nested workflow nodes, overlapping branch stages, shared cross-branch agents,
dynamic `next_strategy`, and divergent joins; those shapes fall back to
deterministic scheduling. `POST /api/workflow-graphs/validate` also returns a
`parallel` diagnostics array so Studio can show whether each parallel node is
concurrency-eligible and why a branch is not eligible before the workflow is
run. `join` acts as a barrier. By default it waits for all
direct non-visual predecessor stages that point to the join node. Set
`params.wait_for`, `params.requires`, or `params.branches` to a comma-separated
stage list when you need an explicit barrier.

```yaml
stages:
  - name: split
    node_type: parallel
    params:
      concurrent: true
    next: [research, audit]
  - name: research
    agent: planner
    skill: execution-plan
    next: [join]
  - name: audit
    agent: auditor
    skill: code-audit
    next: [join]
  - name: join
    node_type: join
    params:
      wait_for: research,audit
    next: [report]
```

`for_each` repeats one executable body stage for each item and then publishes
aggregate outputs. The body stage is named with `params.stage` or
`params.body`. Items can be declared with `params.items` as comma/newline text
or a JSON array, or resolved from `params.items_ref` / `params.ref`. Each body
iteration receives prompt parameters such as `iteration.index`,
`iteration.number`, and `iteration.item`. Persisted iteration stages are named
like `process[1]`, `process[2]`; the `for_each` node output includes
`iteration_count`, `items`, `outputs`, and `summaries`.

`loop` / `until` repeats one executable body stage until `params.until` passes
or `params.max_iterations` is reached. The guard is bounded and capped at 20
iterations. `params.until` supports the same baseline expression language as
`condition`, so expressions such as `contains(previous.raw_output, "done")`
can stop the loop after a body result.

`sub_workflow` runs another built-in or persisted workflow and records the
nested run as a completed parent stage. Configure the nested workflow with
`params.workflow`, and optionally set `params.request` to a literal or
reference such as `workflow.input`. The parent stage publishes `sub_workflow`,
`sub_run_id`, `status`, `summary`, and `completed_stages`.

If the nested workflow pauses for approval, tool approval, manual input, or
another nested sub-workflow, the parent run pauses as `awaiting_sub_workflow`
and stores
`pending_sub_workflow_name`, `pending_sub_workflow_run_id`, and
`pending_sub_workflow_status`. Resume or complete the child run first through
its normal run-scoped action, then continue the parent with:

```bash
curl -X POST http://127.0.0.1:8080/api/workflow-runs/<parent-run-id>/resume-sub-workflow
```

Use `/resume-sub-workflow/stream` for a streaming response. Parent replay and
action discovery include the nested child run id so Studio can reconnect to the
child run, collect the pending approval/input, and then resume the parent
without rerunning completed parent stages.

`checkpoint`, `manual_approval`, and `approval_gate` pause when the workflow was
started without pre-approval. The prompt comes from `params.prompt`,
`params.message`, `params.reason`, or a generated default. After approval, the
checkpoint is persisted as a completed control stage and the workflow continues.

`team`, `agent_team`, and `team_template` nodes reference a reusable team
template through `params.team`, `params.team_template`, or `params.template`.
By default the node records a completed stage with team context variables such
as `team`, `title`, `entry_agent`, `recommended_workflow`, `role_names`,
`roles`, `handoffs`, `blackboard`, and `output_contract`. Downstream stages can
map those values through normal `input` references, and the run persistence /
collaboration store sees the team node like any other completed stage.

Set `params.execute: true` to opt in to executable team roles. At runtime the
backend expands the team template into ordinary stages named
`<team-stage>__<role>`, for example `team__planner`,
`team__implementer`, `team__reviewer`, and `team__reporter`. Each role stage
uses the role's configured agent and skill, receives the previous role output
as mapped input, persists normal stage snapshots, and can be referenced by later
nodes such as `stages.team__reporter.outputs.summary`. Role completion also
emits `team_observation` collaboration messages, and the final role emits a
`team_final_handoff` message/blackboard item. Because expanded roles are normal
workflow stages, existing tool approval, retry, replay, and per-agent permission
boundaries remain in force.

Executable team roles also support lightweight structured collaboration packets.
When a role output contains explicit lines such as `Decision: ...`,
`Critique: ...`, `Question: ...`, `Unresolved: ...`, `Risk: ...`,
`Approval: ...`, or `Rejection: ...`, the backend records matching
`team_decision`, `team_critique`, `team_unresolved_question`, `team_risk`,
`team_approval`, or `team_rejection` messages and blackboard entries.
Chinese labels such as `决定: ...`, `审查意见: ...`, `待确认: ...`, and
`风险: ...` are also recognized. `/api/team-state` exposes these as
`decisions`, `critiques`, `questions`, `risks`, `approvals`, and `rejections`
arrays in addition to the raw `messages` and `blackboard`.

For stricter skill templates, a role can emit JSON directly or inside a fenced
`json` block. Supported keys are `team_packets` / `packets`, plus grouped
`decisions`, `critiques`, `questions`, `unresolved_questions`, `risks`,
`approvals`, and `rejections`.
Packet objects support `kind`, `type`, `subject`, `content`, `summary`, `text`,
`status`, and `items`.

```json
{
  "team_packets": [
    {"kind": "decision", "content": "Use the staged implementation path.", "status": "accepted"},
    {"kind": "question", "content": "Who approves the release window?"}
  ],
  "risks": [
    {"content": "Verification has not covered rollback yet."}
  ]
}
```

```yaml
stages:
  - name: team
    node_type: team
    agent: planner
    params:
      team: software-task-team
      execute: true
    next: [handoff]
  - name: handoff
    agent: chat
    skill: execution-plan
    input:
      final_handoff: stages.team__reporter.outputs.summary
```

Example:

```yaml
stages:
  - name: crawl
    agent: auditor
    skill: web-vulnerability-research
    outputs:
      page_source: result.output
    next: [audit]
  - name: audit
    agent: auditor
    skill: code-audit
    input:
      source: stages.crawl.outputs.page_source
    outputs:
      audit_text: result.output
    next: [has-vuln]
  - name: has-vuln
    node_type: condition
    condition: contains(stages.audit.outputs.audit_text, "vulnerability")
    routes:
      true: verify
      false: report-clean
  - name: verify
    agent: auditor
    skill: code-audit
    approval: true
    next: [end]
  - name: report-clean
    agent: chat
    skill: report-writing
    next: [end]
  - name: end
    node_type: end
```

Policy guard example:

```yaml
stages:
  - name: audit
    agent: auditor
    skill: code-audit
    outputs:
      risk: result.output
    next: [guard]
  - name: guard
    node_type: policy_guard
    policy: contains(stages.audit.outputs.risk, "critical")
    routes:
      allow: verify-critical
      deny: report
    params:
      reason: critical finding was not present
  - name: verify-critical
    agent: auditor
    skill: code-audit
    approval: true
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
```

Named policy rule example:

```yaml
stages:
  - name: audit
    agent: auditor
    skill: code-audit
    outputs:
      risk: result.output
    next: [guard]
  - name: guard
    node_type: policy_guard
    params:
      rule: risk_at_least
      ref: stages.audit.outputs.risk
      minimum: high
    routes:
      allow: verify
      deny: report
  - name: verify
    agent: auditor
    skill: code-audit
  - name: report
    agent: planner
    skill: execution-plan
```

Parallel and join example:

```yaml
stages:
  - name: split
    node_type: parallel
    next: [research, audit]
  - name: research
    agent: planner
    skill: execution-plan
    outputs:
      research_output: result.output
    next: [join]
  - name: audit
    agent: auditor
    skill: code-audit
    outputs:
      audit_output: result.output
    next: [join]
  - name: join
    node_type: join
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
    input:
      research: stages.research.outputs.research_output
      audit: stages.audit.outputs.audit_output
```

For-each example:

```yaml
stages:
  - name: each-target
    node_type: for_each
    params:
      stage: process-target
      items: auth,billing,admin
    next: [report]
  - name: process-target
    agent: auditor
    skill: code-audit
  - name: report
    agent: planner
    skill: execution-plan
    input:
      iteration_outputs: stages.each-target.outputs.outputs
```

Loop/until example:

```yaml
stages:
  - name: refine
    node_type: loop
    params:
      stage: review
      max_iterations: 3
      until: contains(previous.raw_output, "done")
    next: [report]
  - name: review
    agent: auditor
    skill: code-audit
  - name: report
    agent: planner
    skill: execution-plan
    input:
      review_outputs: stages.refine.outputs.outputs
```

Sub-workflow example:

```yaml
stages:
  - name: child
    node_type: sub_workflow
    params:
      workflow: web-research-risk
      request: workflow.input
    next: [report]
  - name: report
    agent: planner
    skill: execution-plan
    input:
      child_summary: stages.child.outputs.summary
      child_run: stages.child.outputs.sub_run_id
```

Input gate example:

```yaml
stages:
  - name: collect
    node_type: input_gate
    params:
      target: auth
      report_format: markdown
    next: [audit]
  - name: audit
    agent: auditor
    skill: code-audit
    input:
      target_area: stages.collect.outputs.target
      format: stages.collect.outputs.report_format
```

Manual input gate example:

```yaml
stages:
  - name: collect
    node_type: input_gate
    params:
      manual: true
      fields: target,severity
      prompt: Provide target and severity before the audit starts.
    next: [audit]
  - name: audit
    agent: auditor
    skill: code-audit
    input:
      target: stages.collect.outputs.target
      severity: stages.collect.outputs.severity
```

Checkpoint example:

```yaml
stages:
  - name: checkpoint
    node_type: checkpoint
    params:
      prompt: Review collected evidence before continuing.
    next: [implement]
  - name: implement
    agent: fixer
    skill: code-writing
```

## HTTP Management API

Workflow execution endpoints:

- `POST /api/workflows/{name}`
- `POST /api/workflows/{name}/stream`

Workflow graph management endpoints:

- `GET /api/workflow-graphs`
- `POST /api/workflow-graphs`
- `POST /api/workflow-graphs/validate`
- `POST /api/workflow-graphs/validate-expression`
- `POST /api/workflow-graphs/import`
- `GET /api/workflow-graphs/{name}`
- `PUT /api/workflow-graphs/{name}`
- `DELETE /api/workflow-graphs/{name}`
- `GET /api/workflow-graphs/{name}/export?format=yaml`
- `POST /api/workflow-graphs/{name}/validate`
- `POST /api/workflow-graphs/{name}/validate-expression`
- `GET /api/workflow-options`
- `GET /api/workflow-expression-functions`
- `GET /api/workflow-templates`
- `GET /api/workflow-templates/{name}`
- `GET /api/resources/workflow-templates`
- `GET /api/resources/workflow-templates/{name}`
- `POST /api/resources/workflow-templates/validate`
- `POST /api/resources/workflow-templates/{name}/validate`
- `PUT /api/resources/workflow-templates/{name}`
- `DELETE /api/resources/workflow-templates/{name}`
- `POST /api/resources/workflow-templates/{name}/capture`
- `POST /api/resources/workflow-templates/{name}/fork`
- `GET /api/resources/workflow-node-metadata`
- `GET /api/resources/workflow-node-metadata/{type}`
- `POST /api/resources/workflow-node-metadata/validate`
- `POST /api/resources/workflow-node-metadata/{type}/validate`
- `PUT /api/resources/workflow-node-metadata/{type}`
- `DELETE /api/resources/workflow-node-metadata/{type}`
- `GET /api/resources/expression-helpers`
- `GET /api/resources/expression-helpers/{name}`
- `POST /api/resources/expression-helpers/validate`
- `POST /api/resources/expression-helpers/{name}/validate`
- `PUT /api/resources/expression-helpers/{name}`
- `DELETE /api/resources/expression-helpers/{name}`
- `GET /api/team-templates`
- `GET /api/team-templates/{name}`
- `GET /api/resources/team-templates`
- `GET /api/resources/team-templates/{name}`
- `POST /api/resources/team-templates/validate`
- `POST /api/resources/team-templates/{name}/validate`
- `PUT /api/resources/team-templates/{name}`
- `DELETE /api/resources/team-templates/{name}`
- `GET /api/resources/team-templates/scaffolds`
- `GET /api/resources/team-templates/scaffolds/{preset}`
- `POST /api/resources/team-templates/scaffolds/{preset}`
- `GET /api/team-state`
- `GET /api/workflow-schemas`
- `GET /api/workflow-schemas/{name}`
- `GET /api/workflow-schemas/export`
- `GET /api/workflow-schemas/{name}/export`
- `POST /api/workflow-schemas/import`
- `POST /api/workflow-schemas`
- `POST /api/workflow-schemas/rebuild`
- `DELETE /api/workflow-schemas`
- `DELETE /api/workflow-schemas/{name}`

`/api/workflow-options` returns configured agents, loaded skills, tool names,
runnable `workflow_executors`, supported node types, workflow template
summaries, policy guard rules, expression helper functions, and team template
summaries for editors. Studio should use `workflow_executors` when it needs a
"run this workflow" selector, and `/api/workflow-graphs` when it needs an
"edit this graph file" selector.

`/api/workflow-expression-functions` returns the expression helper catalog on
its own, including function signatures, return types, argument counts, argument
metadata, insertion templates, and examples. Studio can optionally filter it
with `?mode=condition` or `?node_type=policy_guard` when rendering a node field.
The CLI equivalent is `/expression-helpers [name] [--mode <mode>]
[--node-type <type>]`; `/workflow-expression-functions` is accepted as a
descriptive alias for scripts and users who prefer the HTTP resource name.
The built-in helper catalog is file-backed and embedded from
`internal/agent/templates/expression_helpers/*.yaml`, so contributors can review
and extend editor guidance without reading Go source. Runtime homes can still
override helper metadata with the custom metadata paths described below.

Workflow node metadata is also discoverable from the CLI with
`/workflow-node-metadata [type]`. The list view groups built-in and custom node
metadata by category and highlights control/visual/custom nodes, while the
detail view shows editable fields, common outputs, hints, warnings, examples,
and any custom metadata file path.

`/api/workflow-templates` returns reusable graph blueprints such as
`multi-domain-intake-router`, `complex-project-delivery`,
`engineering-parallel-delivery`, `task-decomposition-plan`, `plan-implement-audit`, `plan-fix-audit`,
`software-quality-gate`, `web-research-risk`,
`security-audit-evidence-gate`, `parallel-research-review`, `binary-triage`,
`docs-review-publish`, `operations-runbook`, `customer-support-triage`,
`human-input-security-review`, and `software-team-review-gate`. The item
endpoint returns the full graph document so a Studio can clone it into
`/api/workflow-graphs/{name}`.

Template list items also include composition metadata for visual pickers:
`node_types`, `agents`, `skills`, `tools`, `team_templates`, `policy_rules`,
`has_control_flow`, `has_data_flow`, `has_approval`, and
`has_quality_gate`. Studio uses these fields to show what a template creates
before the user applies it, instead of forcing users to open the full YAML.

All shipped templates are expected to be runnable delivery blueprints rather
than loose examples. Built-ins must validate as workflow graphs, include an
explicit end node, emit report or evidence artifacts, declare acceptance
criteria, route through a `quality_gate` or `policy_guard`, and finish with a
user-facing report, handoff, publish summary, or materialized workflow draft.
This keeps simple starters useful while still making them safe foundations for
larger tasks.

`multi-domain-intake-router` is the recommended built-in entry template for new
users and broad deployments. It starts with a structured intake form, uses a
strong planner route to decompose the request, activates only the selected
domain workers, joins their compact reports, audits cross-domain coverage and
token discipline, then produces a quality-gated final handoff. The selectable
selected domain workers are the only branches that run; pruned branches stay
visible for diagnosis without spending tokens or calling tools. The selectable
branches cover software engineering, web security, general security research,
binary analysis, documentation, operations, customer support,
platform/framework extension work, and a general fallback. Expert fields expose
`active_branches_ref`, `wait_for_ref`, branch-selection reason, pruned branches,
per-branch tool allowlists, model routes, context budgets, branch contracts,
cost attribution, artifact refs, join conflict diagnostics, missing worker
artifacts, and linked team template refs so developers can extend the
orchestration without making ordinary users edit raw maps.

`complex-project-delivery` is the recommended template for broad implementation
work that should continue until the accepted requirements are satisfied. It
turns the user request into requirement analysis and a project plan, pauses for
plan confirmation, then runs an implementation/verification/review/plan-update
loop until the iteration output declares `PROJECT_COMPLETE`. After the loop it
runs final validation and produces a completion report with delivered
requirements, changed files, verification evidence, residual risks, and artifact
references.

`engineering-parallel-delivery` is the recommended template for complex
engineering work where quality should come from orchestration rather than one
large prompt. It uses a strong planning route to decompose the task into owned
work slices, fans out bounded worker stages on cheaper routes, joins their
reports, audits the integrated result, and keeps both `model.max_tokens` and
`context.max_tokens` tight so workers exchange summaries and artifact refs
instead of raw logs or full history. Warnings or failures route through a
compact resolver pass before the final handoff.

`plan-implement-audit` is the recommended template for a bounded delivery task
that still needs a full handoff: clarify, plan, implement, verify, audit,
quality gate, and final report.

`task-decomposition-plan` is the recommended pre-flight template for very
complex tasks. It asks the planner to produce a workflow graph candidate with
phases, dependencies, required access, suggested agents/skills/tools, expected
artifacts, risk labels, acceptance criteria, checkpoints, and failure modes;
then it routes that candidate through an auditor review, a `quality_gate`, and a
materialized workflow draft or clarification gate.

Custom workflow templates are stored under `templates/workflows/<name>.yaml`
and are included in `/api/workflow-templates`. A custom file with the same name
as an embedded template overrides that template everywhere it is listed,
validated, forked, or used to create a workflow. Studio can manage templates through
`/api/resources/workflow-templates`: `GET` lists custom template resources,
`PUT /api/resources/workflow-templates/{name}` saves a validated template,
`POST /api/resources/workflow-templates/validate` or
`POST /api/resources/workflow-templates/{name}/validate` validates and
normalizes an unsaved draft without writing a template file, `DELETE` removes
it, and `POST /api/resources/workflow-templates/{name}/capture`
saves an existing workflow graph as a reusable template. `POST
/api/resources/workflow-templates/{name}/fork` copies a built-in or custom
template into `templates/workflows/<name>.yaml` so it can be edited, diffed, and
versioned like any other resource:

```bash
curl -s -X POST http://127.0.0.1:8080/api/resources/workflow-templates/my-security-review/fork \
  -H "Content-Type: application/json" \
  -d '{"source":"human-input-security-review","title":"My Security Review"}'
```

Saved custom templates use a versioned v2 envelope:

```yaml
kind: goflow.workflow_template_resource
version: 2
min_supported_version: 1
name: team-review
title: Team Review
category: custom
graph:
  name: team-review
  stages:
    - name: plan
      agent: planner
      skill: execution-plan
```

Legacy template files that contain a bare workflow graph (`name`,
`description`, and `stages` at the top level) are still loaded and reported with
`migrated_from_version: 1`. Future unsupported template resource versions are
rejected instead of being silently interpreted.

`/api/team-templates` returns reusable multi-agent collaboration patterns such
as `software-task-team`, `audit-security-team`, `web-research-team`,
`binary-triage-team`, `documentation-team`, `operations-runbook-team`, and
`customer-support-team`, plus the second-development
`framework-extension-team`. The item endpoint returns role mappings, handoffs,
shared blackboard slots, output contracts, and a recommended workflow template.
These templates are used for Studio discovery, scaffolding, `team` nodes, and
the multi-domain starter route. A `team` node records the selected template as
workflow context and can optionally expand into executable role stages when
`params.execute: true` is set.

Custom team templates are stored under `templates/teams/<name>.yaml` and are
merged with built-ins. A custom template with the same name overrides the
built-in for `/api/team-templates`, `/api/workflow-options.team_templates`,
workflow validation, executable `team` node expansion, and TeamState summaries.
Studio can manage custom team templates through
`/api/resources/team-templates`: `GET` lists custom template resources,
`POST /api/resources/team-templates/{name}/validate` or
`POST /api/resources/team-templates/validate` validates and normalizes a
template without writing it, `PUT /api/resources/team-templates/{name}` saves a
validated template, and `DELETE` removes it. Validation failures still return a
JSON body with `valid:false` and structured `issues`, so Studio forms can render
field-level errors before saving instead of parsing plain HTTP error text.

Studio can also create a custom team template from backend presets:

```bash
curl http://127.0.0.1:8080/api/resources/team-templates/scaffolds
curl "http://127.0.0.1:8080/api/resources/team-templates/scaffolds/software-review?name=my-review-team"
curl -s -X POST http://127.0.0.1:8080/api/resources/team-templates/scaffolds/software-review \
  -H "Content-Type: application/json" \
  -d '{"name":"my-review-team","title":"My Review Team"}'
```

Available scaffold presets are `software-review`, `security-review`,
`web-research`, `binary-triage`, `documentation`, `operations-runbook`, and
`customer-support`. Each preset clones the corresponding built-in team into a
versioned custom file under `templates/teams/<name>.yaml`, preserves
role/handoff/blackboard/output contracts, and adds a reusable quorum preset
when the base team does not define one. Existing built-in or custom names are
protected by default; pass `overwrite=1` or body field `"overwrite": true` to
create a custom override.

Saved templates use a versioned v2 envelope:

```yaml
kind: goflow.team_template_resource
version: 2
min_supported_version: 1
name: custom-review-team
title: Custom Review Team
category: custom
recommended_workflow: plan-fix-audit
recommended_entry_agent: planner
role_templates:
  - name: planner
    agent: planner
    skill: execution-plan
    responsibilities:
      - create the review plan
    produces:
      - plan
  - name: reviewer
    agent: auditor
    skill: code-audit
    consumes:
      - plan
    produces:
      - findings
handoffs:
  - from: planner
    to: reviewer
    kind: review_request
blackboard_templates:
  - kind: decision
    title: Decisions
    owner_role: planner
quorum_presets:
  - name: review-quorum
    title: Reviewer quorum
    required: 1
    roles: [reviewer]
    reject_blocks: true
    default: true
output_contract:
  - findings
```

Future unsupported team template resource versions are rejected clearly instead
of being silently interpreted.

Workflow node editor metadata can be customized without recompiling GoFlow.
Place YAML files under `metadata/workflow_nodes/*.yaml` or
`templates/workflow_nodes/*.yaml`. These files may override built-in node
labels, descriptions, field help, output descriptions, tags, hints, warnings,
examples, and default stage snippets for existing node types. They cannot create
new executable node types; unknown `type` values are ignored by
`/api/workflow-options`. Studio can manage the preferred
`metadata/workflow_nodes/*.yaml` resources through
`/api/resources/workflow-node-metadata`. It can preflight a node metadata
document with `POST /api/resources/workflow-node-metadata/validate` or
`POST /api/resources/workflow-node-metadata/{type}/validate`; validation returns
`valid:true` with a normalized document or `valid:false` with structured
`issues`, and it does not write the metadata file.
Built-in node metadata is embedded from
`internal/agent/templates/workflow_nodes/*.yaml`, so node fields, outputs,
hints, warnings, examples, and default stage snippets are reviewable as YAML.

```yaml
kind: goflow.workflow_node_metadata
version: 1
type: policy_guard
label: Review Gate
description: Custom Studio copy for review-gate policy nodes.
tags: [review, gate]
hints:
  - Use approval_preset when the selected team defines a reusable quorum.
warnings:
  - A deny route should point to a revision or stop path.
fields:
  - name: params.rule
    label: Guard rule
    description: Select a built-in or custom policy rule.
  - name: params.approval_preset
    label: Approval preset
    type: text
    description: Team quorum preset name.
outputs:
  - name: value
    description: Evaluated gate status.
examples:
  - title: Team approval gate
    description: Branch after a reusable team review preset.
    stage:
      name: review_gate
      node_type: policy_guard
      params:
        rule: team_approval_gate
        approval_preset: software-review
```

Expression helper metadata can also be customized. Place YAML files under
`metadata/expression_helpers/*.yaml`, `metadata/workflow_expressions/*.yaml`,
`templates/expression_helpers/*.yaml`, or
`templates/workflow_expressions/*.yaml`. These resources only change editor
metadata for built-in helpers; expression execution still comes from GoFlow's
implemented expression engine. Studio can manage the preferred
`metadata/expression_helpers/*.yaml` resources through
`/api/resources/expression-helpers`. It can preflight helper metadata with
`POST /api/resources/expression-helpers/validate` or
`POST /api/resources/expression-helpers/{name}/validate`; validation confirms
the helper exists, normalizes the envelope, and returns structured issues
without writing the metadata file.

```yaml
kind: goflow.workflow_expression_function
version: 1
name: risk_rank
label: Severity Rank
description: Custom Studio explanation for ranking severity-like values.
hints:
  - Use numeric comparisons such as risk_rank(ref) >= 4.
warnings:
  - Empty values rank as zero.
args:
  - name: value
    label: Severity value
    description: Text or numeric risk value.
examples:
  - risk_rank(stages.audit.outputs.severity) >= 4
```

`/api/team-state?run_id=<id>&team=<name>` returns the derived live team view for
Studio panels: active owner, selected template, team handoff messages,
blackboard entries, structured decisions/critiques/questions/risks, unresolved
decisions/items, pending approvals, and pending input state. The CLI equivalent
is `/team-state [run-id] [team]`.

Team blackboard entries can be resolved or reopened without replacing the whole
entry:

- `POST /api/collaboration/blackboard/{id}/resolve`
- `POST /api/collaboration/blackboard/{id}/reopen`
- `POST /api/collaboration/blackboard/{id}/assign`
- `POST /api/collaboration/blackboard/{id}/escalate`

These endpoints accept an optional JSON body with `note`, `content`, and
`metadata`. `assign` also accepts `assigned_agent`, `assigned_to`,
`owner_agent`, `owner_role`, and `agent_id`; `escalate` accepts `escalate_to`,
`severity`, and `priority`. They preserve existing provenance fields and
metadata, update status/ownership, and update `/api/team-state` so resolved
questions/risks leave `unresolved_items`, assignments appear in `assignments`,
and escalated items appear in `escalations` while still remaining available in
the typed history arrays.

Executable team JSON packets can also drive handoff and ownership metadata.
Packet objects may use `kind: "handoff"`, `kind: "assignment"`,
`kind: "escalation"`, `kind: "approval"`, or `kind: "rejection"` and can include
`to_agent`, `to_role`, `owner_agent`, `owner_role`, `assigned_agent`,
`escalate_to`, `severity`, `priority`, `sla`, `sla_minutes`, `due_at`,
`deadline`, `escalate_after`, and `metadata`. Numeric
`sla_minutes` values are normalized into metadata and generate a `due_at`
timestamp when no explicit `due_at` or `deadline` is provided. When `to_role` or
`owner_role` names a role in the active team
template, the backend resolves it to that role's configured agent for
collaboration messages, blackboard ownership, active-owner calculation, and
Studio handoff panels. During executable team expansion, `handoff.to_role`,
`owner_role`, `to_agent`, `assigned_agent`, or `escalate_to` can also choose the
next downstream team role from the current role's candidate list. If no explicit
handoff target matches, execution keeps the template's default role order.

Executable `team` nodes can also promote ordinary risk, question, or critique
packets into escalations through stage params inherited by each generated role
stage:

- `escalate_severity`: minimum severity that should escalate, for example
  `high` or `critical`.
- `escalate_priority`: minimum priority that should escalate, for example
  `p1`, `p0`, `high`, or `critical`.
- `escalate_kinds`: optional comma-separated packet kinds to evaluate. Defaults
  to `team_risk`, `team_unresolved_question`, and `team_critique`.
- `escalate_to` / `escalate_role`: target agent or team role for promoted
  escalations.
- `escalation_sla`, `escalation_sla_minutes`, `escalation_due_at`,
  `escalation_deadline`, and `escalation_after`: default escalation SLA fields.

When a packet meets the configured threshold, the backend records it as
`team_escalation`, adds `escalation_policy: true` and an `escalation_reason`, and
uses the same target fields for collaboration messages, blackboard ownership,
TeamState escalation panels, and conditional next-role routing.

Executable `team` nodes can also carry review gate metadata:

- `approval_preset`, `review_preset`, `quorum_preset`, or `preset`: select a
  reusable quorum preset declared by the team template. If a selected team has a
  default preset and no explicit preset is supplied, executable team role stages
  inherit the default preset.
- `approval_quorum`: number of approval packets required for the gate.
- `approval_roles`: comma-separated roles that are allowed to count toward the
  quorum.
- `reject_blocks`: when true, any counted rejection packet blocks the gate even
  if the approval quorum is met.

Team template `quorum_presets` let Studio and workflow authors reuse review
rules without copying raw quorum fields into every node. A preset can define
`required`, `roles`, `reject_blocks`, and `default`. Explicit node params still
win, so a workflow can start from a preset and override only one field.

`/api/team-state` derives an `approval_gate` summary with `required`,
`approved`, `rejected`, `pending`, `approvers`, `rejectors`, and `status`
(`pending`, `passed`, or `blocked`). This is an observable gate summary for
Studio and downstream policy nodes. It does not by itself pause execution. To
pause for missing review quorum, add a `policy_guard` node with
`params.rule: team_approval_gate`, `params.status: passed`, and
`params.wait_for_quorum: true`; the run then waits at `awaiting_input` until an
operator submits the missing role decision.

Save a workflow graph:

```bash
curl -s -X PUT http://127.0.0.1:8080/api/workflow-graphs/release-check \
  -H "Content-Type: application/json" \
  -d '{
    "name": "release-check",
    "description": "Plan, implement, and audit a change.",
    "stages": [
      {"name":"plan","agent":"planner","skill":"execution-plan","next":["implement"],"position":{"x":80,"y":120}},
      {"name":"implement","agent":"fixer","skill":"code-writing","approval":true,"next":["audit"],"position":{"x":360,"y":120}},
      {"name":"audit","agent":"auditor","skill":"code-audit","position":{"x":640,"y":120}}
    ]
  }'
```

Validate before saving:

```bash
curl -s -X POST http://127.0.0.1:8080/api/workflow-graphs/validate \
  -H "Content-Type: application/json" \
  -d '{"name":"draft","stages":[{"name":"plan","agent":"planner","skill":"execution-plan"}]}'
```

The validation response is structured for Studio overlays:

```json
{"valid":true,"name":"draft","stages":1}
```

Warnings do not make `valid` false. For complex drafts, validation also emits
quality-design warnings when the graph lacks acceptance criteria, verifier or
review stages, `quality_gate` nodes, declared artifacts, output contracts, or
human approval/checkpoint paths. Studio should display these as authoring
guidance while still allowing the user to save structurally valid workflows.

For `parallel` / `fan_out` nodes, validation includes concurrency diagnostics:

```json
{
  "valid": true,
  "name": "parallel-check",
  "stages": 4,
  "parallel": [
    {
      "stage": "split",
      "enabled": true,
      "eligible": true,
      "branch_count": 2,
      "join": "join",
      "branches": [
        {"start": "research", "eligible": true, "stages": ["research"], "agents": ["planner"], "join": "join"},
        {"start": "audit", "eligible": true, "stages": ["audit"], "agents": ["auditor"], "join": "join"}
      ]
    }
  ]
}
```

Validate a workflow reference, condition, switch expression, policy expression,
or free-form expression for node forms and edge labels:

```bash
curl -s -X POST http://127.0.0.1:8080/api/workflow-graphs/validate-expression \
  -H "Content-Type: application/json" \
  -d '{
    "mode": "condition",
    "expression": "len(stages.collect.outputs.tags) >= 2",
    "workflow": {
      "name": "draft",
      "stages": [
        {
          "name": "collect",
          "type": "input_gate",
          "outputs": {
            "tags": "array",
            "payload": "object"
          }
        }
      ]
    }
  }'
```

For a saved graph, use
`POST /api/workflow-graphs/{name}/validate-expression` and omit `workflow` from
the request body. The request accepts `expression` plus `mode`; `reference`
forces `mode: reference`. Supported modes are `reference`, `condition`,
`policy`, `switch`, and `expression`. Pass `run_id` when Studio is editing or
debugging a previous run; the backend then uses persisted stage `output_values`
to infer nested object keys, array item paths, and more precise suggestion
types. The response includes `valid`, `value_type`, resolved `references`,
`issues`, and Studio completion `suggestions`:

The validator understands the same control-flow helper functions used by the
runtime: `contains`, `in`, `matches`, `any`, `all`, `exists`, `has`, `len`, and
`risk_rank`. It checks function arity, reports unsupported function names, and
extracts references from nested arguments such as
`risk_rank(stages.collect.outputs.payload.risk) >= 4`.

```json
{
  "valid": true,
  "mode": "condition",
  "expression": "len(stages.collect.outputs.tags) >= 2",
  "value_type": "boolean",
  "references": [
    {
      "expression": "stages.collect.outputs.tags",
      "valid": true,
      "type": "array",
      "stage": "collect",
      "output": "tags"
    }
  ],
  "suggestions": [
    {
      "reference": "stages.collect.outputs.payload",
      "type": "object",
      "stage": "collect",
      "source": "stage_output"
    },
    {
      "reference": "stages.collect.outputs.payload.risk",
      "type": "string",
      "stage": "collect",
      "source": "run_output"
    }
  ]
}
```

Every completed workflow run also updates a session-persisted workflow schema
catalog. The catalog merges observed `output_values` by workflow, stage, and
output name, keeps nested object fields and array item shapes, and is used by
expression validation automatically when no `run_id` is supplied. Use the schema
endpoints when Studio needs to inspect or refresh those observed shapes:

```bash
curl http://127.0.0.1:8080/api/workflow-schemas
curl http://127.0.0.1:8080/api/workflow-schemas/release-check
curl http://127.0.0.1:8080/api/workflow-schemas/export
curl http://127.0.0.1:8080/api/workflow-schemas/release-check/export
curl -X POST http://127.0.0.1:8080/api/workflow-schemas/rebuild
curl -X DELETE http://127.0.0.1:8080/api/workflow-schemas
curl -X DELETE http://127.0.0.1:8080/api/workflow-schemas/release-check
```

Schema export returns a versioned bundle:

```json
{
  "kind": "goflow.workflow_schemas",
  "version": 2,
  "min_supported_version": 1,
  "schemas": [{"workflow": "release-check", "stages": {}}]
}
```

Import a shared schema bundle with merge semantics by default:

```bash
curl -s -X POST http://127.0.0.1:8080/api/workflow-schemas/import \
  -H "Content-Type: application/json" \
  --data-binary @workflow-schemas.json
```

Use `?merge=false` or body field `"merge": false` to replace an existing schema
for the same workflow instead of merging observed fields.
The importer still accepts v1 bundles, raw schema arrays, and a single bare
schema object for compatibility. Bundles with a future unsupported `version` or
`min_supported_version` return `400` with a clear compatibility error.

Reusable schema resources are stored under `schemas/workflows/<name>.json` and
are separate from the transient session catalog. Studio can manage them through:

- `GET /api/resources/workflow-schemas`
- `GET /api/resources/workflow-schemas/{name}`
- `POST /api/resources/workflow-schemas/validate`
- `POST /api/resources/workflow-schemas/{name}/validate`
- `PUT /api/resources/workflow-schemas/{name}`
- `DELETE /api/resources/workflow-schemas/{name}`
- `POST /api/resources/workflow-schemas/{name}/capture`
- `POST /api/resources/workflow-schemas/{name}/activate`

`capture` saves the current session catalog entry for a workflow as a reusable
resource. `activate` imports the saved resource back into the active session
catalog; it supports the same merge semantics through `?merge=false` or body
field `"merge": false`. The `validate` endpoints preflight a schema draft with
the same normalization rules without writing `schemas/workflows/<name>.json` or
importing it into the active session catalog. Resource files are saved as
`kind: goflow.workflow_schema_resource`, `version: 2`, and
`min_supported_version: 1`; legacy bare schema JSON is migrated on load/save and
future unsupported resource versions are rejected. This directory is optional
and does not back the built-in workflow templates; those ship separately from
`internal/agent/templates/workflows/*.yaml`.

Import or export graph files:

```bash
curl -s -X POST http://127.0.0.1:8080/api/workflow-graphs/import \
  -H "Content-Type: application/yaml" \
  --data-binary @workflow.yaml

curl -s http://127.0.0.1:8080/api/workflow-graphs/release-check/export?format=yaml
curl -s http://127.0.0.1:8080/api/workflow-graphs/release-check/export?format=json
```

Run it:

```bash
curl -s -X POST http://127.0.0.1:8080/api/workflows/release-check \
  -H "Content-Type: application/json" \
  -d '{"input":"improve CLI diff output"}'
```

HTTP workflow requests accept an optional `approve` flag. It defaults to `true`
to preserve the current Studio behavior. Pass `false` when the UI should honor
stage approval gates and `checkpoint` nodes:

```bash
curl -s -X POST http://127.0.0.1:8080/api/workflows/release-check \
  -H "Content-Type: application/json" \
  -d '{"input":"improve CLI diff output","approve":false}'
```

Inspect recent workflow runs:

```bash
curl http://127.0.0.1:8080/api/workflow-runs
```

By default this endpoint returns the legacy newest-first run array. When a
query filter is supplied, or when `envelope=1` is passed, it returns:

```json
{
  "runs": [],
  "filters": {},
  "counts": {
    "total": 0,
    "matched": 0,
    "returned": 0,
    "active": 0,
    "terminal": 0,
    "needs_action": 0,
    "errors": 0
  }
}
```

Supported collection filters are `workflow` / `name`, `status`, `q` /
`search`, `retry_of`, `limit`, `active=1`, `terminal=1`, `needs_action=1`, and
`errors_only=1`. This is intended for Studio run-history lists, badges, and
resume panels without forcing the frontend to download and count every run
itself:

```bash
curl "http://127.0.0.1:8080/api/workflow-runs?envelope=1&active=1"
curl "http://127.0.0.1:8080/api/workflow-runs?workflow=plan-fix-audit&status=completed&limit=20"
curl "http://127.0.0.1:8080/api/workflow-runs?needs_action=1&q=approval"
```

For a mixed Studio history view, use the unified run collection instead:

```bash
curl "http://127.0.0.1:8080/api/runs?type=workflow&needs_action=1"
curl "http://127.0.0.1:8080/api/runs?action=submit_input"
curl "http://127.0.0.1:8080/api/runs?terminal=1&limit=20"
```

`GET /api/runs` always returns an envelope with both ordinary Agent and
workflow runs, sorted newest-first by update time. Each item includes the
appropriate replay, action, diff, and reconnectable event-stream paths. Workflow
items also include a derived `quality` summary with the run quality status,
score, acceptance/verification pass-fail counts, evidence artifact count, errors,
pending action count, unmet criteria, and failed validations. Filters include
`type=agent|workflow`, `agent`, `mode`, `workflow`, `status`, `tool`, `q` /
`search`, `action` / `action_name`, `retry_of`, `limit`, `active=1`,
`terminal=1`, `needs_action=1`, and `errors_only=1`. The response also includes
`facets` for run types, statuses, agents, modes, workflows, tools, and
currently available actions so Studio can render filter chips/dropdowns without
downloading separate run collections.

Inspect one run, its replay events, artifacts, and stage snapshots:

```bash
curl http://127.0.0.1:8080/api/workflow-runs/{run_id}
curl http://127.0.0.1:8080/api/workflow-runs/{run_id}/events
curl http://127.0.0.1:8080/api/workflow-runs/{run_id}/artifacts
curl http://127.0.0.1:8080/api/workflow-runs/{run_id}/stages/{stage_name}
curl http://127.0.0.1:8080/api/workflow-runs/{run_id}/timeline
curl http://127.0.0.1:8080/api/workflow-runs/{run_id}/replay
curl http://127.0.0.1:8080/api/workflow-runs/{run_id}/evidence
curl http://127.0.0.1:8080/api/workflow-runs/{run_id}/navigation
curl http://127.0.0.1:8080/api/workflow-runs/{run_id}/diffs
curl "http://127.0.0.1:8080/api/workflow-runs/{run_id}/export?format=json"
curl "http://127.0.0.1:8080/api/workflow-runs/{run_id}/export?format=md&include_content=1"
```

The `replay` endpoint returns one bundle containing the run snapshot, stage
snapshots, events, artifacts, a compact timeline, parsed write/delete diffs,
available actions, frontend-ready stage navigation, total/filtered counts, a
derived `quality` summary, collaboration messages, blackboard entries, and
derived team state. It is intended for Studio run-history and replay panels so
the frontend does not need to manually join every collection.

The replay `quality` object is intentionally compact for dashboards and review
drawers:

```json
{
  "status": "failed",
  "score": 35,
  "acceptance_total": 2,
  "acceptance_passed": 1,
  "acceptance_failed": 1,
  "verification_total": 2,
  "verification_failed": 1,
  "evidence_artifacts": 5,
  "unmet_criteria": [{"stage": "verify", "name": "risk evidence"}],
  "failed_validations": [{"stage": "verify", "kind": "risk-review"}]
}
```

Use `quality.status` for high-level badges (`passed`, `failed`, `warning`,
`needs_action`, `running`, `cancelled`, or `unverified`) and `quality.score` for
compact health meters. Detailed stage evidence remains in
`completed_stages[].acceptance`, `completed_stages[].result.verification`, and
the run artifact list.

The `evidence` endpoint returns a graph-oriented view for provenance and
quality panels. It contains stage, artifact, check, file, and reference nodes;
typed edges such as `produced`, `checks`, `evidenced_by`, `related_file`, and
`references`; quality counts; failed criteria; validation issues; and warnings.
Use it when Studio needs an evidence graph, quality-gate result display, or
artifact provenance drawer without re-deriving relationships from raw replay
JSON. It accepts `stage`, `kind=check|artifact|stage|evidence`, `artifact_kind`,
`status`, `q`, and `limit` filters.

The `navigation` endpoint returns only the derived stage sidebar data from the
same replay model. Each stage item includes a stable `anchor`, previous/next
stage names, detail/events/artifacts/diffs/replay paths, status, node metadata,
and badge counts for events, artifacts, diffs, errors, and pending action state.
Use it for lightweight route changes, stage drawers, or restoring a selected
stage after a browser refresh.

The `diffs` endpoint returns only parsed file-change entries from write/delete
tool results and diff/patch artifacts. Each item includes stage, agent, tool,
path, status code (`A`, `M`, `D`, or `=`), line deltas, changed ranges, a
bounded `diff_preview`, and a ready-to-render `patch` string. It accepts the
same filters as replay, including `stage`, `tool_name`, `q`, `limit`, and
`kind=diff`.

The `export` endpoint returns the same replay bundle as formatted JSON or a
readable Markdown report. It supports the same filters as `replay`, so Studio
can export a full run or only one stage, event type, or artifact class. Markdown
includes a `Quality` section before the stage list and omits large artifact
content by default; pass `include_content=1` when the user explicitly asks for a
full evidence bundle.

Replay endpoints accept filters for drill-down panels:

```bash
curl "http://127.0.0.1:8080/api/workflow-runs/{run_id}/events?stage=plan&type=task_stage&limit=20"
curl "http://127.0.0.1:8080/api/workflow-runs/{run_id}/timeline?stage=plan&kind=event"
curl "http://127.0.0.1:8080/api/workflow-runs/{run_id}/replay?stage=plan&kind=artifact&artifact_kind=output"
curl "http://127.0.0.1:8080/api/workflow-runs/{run_id}/evidence?stage=verify&kind=check&status=failed"
curl "http://127.0.0.1:8080/api/workflow-runs/{run_id}/navigation?anchor=stage:plan"
```

Supported query fields are `stage`, `kind` (`event`, `stage`, or `artifact`),
`type` / `event_type`, `artifact_kind`, `status`, `agent_id`, `tool_name`,
`q` / `search`, `limit`, `errors_only`, `needs_action`, `suspended`, and
`include_content`. `focus_stage` and `anchor=stage:<name>` are aliases for
selecting the current navigation stage. Timeline content is omitted by default
so Studio can render large runs quickly; pass `include_content=true` when a
detail drawer needs the full text.

Resume a run paused at a manual `input_gate`:

```bash
curl -s -X POST http://127.0.0.1:8080/api/workflow-runs/{run_id}/input \
  -H "Content-Type: application/json" \
  -d '{"inputs":{"target":"auth","severity":"high"}}'
```

If the pending gate declares `pending_input_fields`, missing required values,
invalid numbers/booleans/JSON/URLs, or values outside a select field's options
return `400 Bad Request` and the run remains paused at `awaiting_input`.

Streaming resume is also available:

```text
POST /api/workflow-runs/{run_id}/input/stream
```

## Linear Workflow

This graph always runs plan, implement, then audit:

```yaml
name: linear-change
description: Plan, implement, and review a code change.
stages:
  - name: plan
    agent: planner
    skill: execution-plan
  - name: implement
    agent: fixer
    skill: code-writing
    approval: true
  - name: audit
    agent: auditor
    skill: code-audit
```

Run it:

```text
/workflow linear-change add validation around config loading
```

Because `implement.approval` is true, the workflow pauses before the fixer stage. If approved, fixer runs and any write tool calls may still require separate tool approval.

## Branch Selection

This graph lets triage choose whether the request needs implementation or audit only:

```yaml
name: triage-change
description: Decide whether to implement or audit.
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    next_strategy: select
    next: [implement, audit]
  - name: implement
    agent: fixer
    skill: code-writing
    approval: true
    next: [audit]
  - name: audit
    agent: auditor
    skill: code-audit
```

With `next_strategy: select`, GoFlow first looks for explicit stage choices in the triage output:

```text
Next skill: implement
```

or:

```text
Next skills: implement, audit
```

The directive can name either a candidate stage (`implement`) or that stage's skill (`code-writing`). It cannot jump to undeclared stages.

If no explicit directive is present, GoFlow scores candidate stage and skill names against the original request plus the current stage output and chooses the best positive match.

## Stage Approval vs Tool Approval

There are two separate approval boundaries.

Stage approval is configured in the graph:

```yaml
- name: implement
  agent: fixer
  skill: code-writing
  approval: true
```

This pauses before the stage starts. It is useful before write or exec capable agents run.

Tool approval comes from the selected agent profile and tool policy. For example, a fixer stage may start after stage approval, then pause again when the model calls `file_tools/write_file`.

When a tool call is approved, GoFlow resumes the suspended stage by replaying:

- the original stage prompt
- the assistant tool-call context
- the approved tool result

Then the same stage continues and the workflow proceeds to the next stage.

## Execution Readiness Contract

`stage.execution` declares whether a stage is planning-only, a dry run, a manual handoff, disabled, or a live action candidate. This is separate from `approval: true`: approval pauses a stage before it starts, while the execution contract records the target boundary and readiness evidence needed before a live action can run.

Supported fields:

- `mode`: `planning`, `dry_run`, `manual`, `disabled`, or `live`.
- `risk_level`: `low`, `medium`, `high`, or `critical`.
- `boundary`: short operator-readable scope such as `workspace-draft`, `lab`, or `production`.
- `requires_approval`, `requires_authorized_scope`, `requires_rollback`, `requires_credential_ref`, `requires_allowlist`: explicit readiness requirements.
- `allow_live_tools`: the exact tools that may be used by a live stage.
- `required_params`: extra stage params that must be present before live execution.

Example:

```yaml
- name: apply-change
  node_type: tool
  agent: fixer
  skill: code-writing
  tool: network_ops/apply_config
  approval: true
  execution:
    mode: live
    risk_level: high
    boundary: production-router-change
    requires_authorized_scope: true
    requires_rollback: true
    requires_credential_ref: true
    requires_allowlist: true
    allow_live_tools: [network_ops/apply_config]
    required_params: [change_ticket]
  params:
    authorized_scope: router-edge-01
    rollback_plan: artifacts://rollback-plan
    credential_ref: vault://network/prod/operator
    allowed_devices: router-edge-01
    change_ticket: CHG-12345
```

For `mode: live`, GoFlow blocks before model or tool execution when readiness evidence is missing. The blocked stage is persisted with `contract_check=execution_readiness`, `contract_failed=true`, `execution.mode`, `execution.ready=false`, `execution.missing`, `execution.risk_level`, `execution.boundary`, and `source_ref`. The stream also emits `execution_readiness_blocked`, so Workflow Studio can show the failing node, hover chips, selected-node diagnostics, and the exact missing requirement.

`planning`, `dry_run`, `manual`, and `disabled` do not trigger live-readiness blocking. They still publish execution metadata so operators can see the declared boundary and understand that a template is simulating or handing off instead of applying an external change.

Legacy params such as `execution_mode`, `execution.risk_level`, `authorized_scope`, `rollback_plan`, `credential_ref`, `allowed_hosts`, `allowed_commands`, `allow_live_tools`, and `required_params` are still read for compatibility. Prefer the structured `stage.execution` block for new workflows.

For the built-in controlled RESTCONF/API connector, a live stage must declare
both workflow-level readiness and tool-level allowlists:

```yaml
- name: apply-restconf-change
  node_type: tool
  agent: operations-specialist
  skill: execution-plan
  tool: network_tools/device_restconf_live_apply
  approval: true
  execution:
    mode: live
    risk_level: high
    boundary: production-restconf-change
    requires_approval: true
    requires_authorized_scope: true
    requires_rollback: true
    requires_credential_ref: true
    requires_allowlist: true
    allow_live_tools: [network_tools/device_restconf_live_apply]
    required_params: [approval_ref, change_ticket, allowed_hosts, allowed_paths]
  params:
    host: router-edge-01.example.com
    endpoint: https://router-edge-01.example.com/restconf/data/native/interface
    method: PATCH
    credential_ref: env:GOFLOW_RESTCONF_TOKEN
    approval_ref: approval-123
    change_ticket: CHG-12345
    authorized_scope: true
    allowed_hosts: [router-edge-01.example.com]
    allowed_methods: [PATCH]
    allowed_paths: [/restconf/data/native/*]
    payload:
      interface:
        description: approved maintenance update
    rollback_plan:
      - restore previous interface description from backup artifact
    dry_run_confirmed: true
    change_approved: true
```

The MCP server still refuses to send the HTTPS request unless
`GOFLOW_NETWORK_TOOLS_ENABLE_LIVE=1` is present in that server environment. When
the switch is absent, the tool returns a blocked audit payload rather than
pretending success. Secrets are resolved only from `env:`/`env://` credential
refs inside the MCP server and are not included in tool results.

## Approval Resume Example

Example flow:

```text
/workflow triage-change improve CLI diff output
```

1. `triage` runs with `planner`.
2. It selects `implement`.
3. `implement` has `approval: true`, so the CLI asks whether to enter the stage.
4. The fixer calls `file_tools/write_file`.
5. The CLI asks for tool approval.
6. After approval, the fixer receives the tool result and finishes.
7. The workflow follows `next: [audit]`.
8. `audit` runs with `auditor`.

During pauses:

- `/status` shows current workflow, next stage, pending call id, tool, agent, and stage.
- `/session` shows persisted workflow and pending approval snapshots.
- `Esc` twice cancels the active operation in interactive CLI mode.
- After cancellation, typing only `继续`, `重试`, or `continue` re-runs the cancelled request from the beginning.

## Common Errors

`unknown workflow: <name>`:

- No built-in workflow matched.
- No `workflows/<name>/workflow.yaml` exists in runtime home.

`invalid workflow graph "<name>": ...`:

- The YAML file exists but failed validation.
- Typical causes are missing `name`, missing `stages`, unknown `next` stage, missing `agent`, or missing `skill`.

`workflow stage <stage> references unknown skill <skill>`:

- The stage names a skill that is not loaded from `skills/<name>/SKILL.md`.
- Run `/skills` to inspect loaded skill names.

`workflow agent not configured: <agent>`:

- The stage names an agent absent from the merged runtime agent config (`configs/agents/*.yaml` plus any `agents:` entries in `configs/goflow.yaml`).
- Run `/agents` to inspect configured profiles.

## Authoring Checklist

1. Keep each stage focused on one role.
2. Put write or exec stages behind `approval: true`.
3. Mark planning, dry-run, manual, and live boundaries with `stage.execution`.
4. For `mode: live`, provide approval, authorized scope, rollback, credential, allowlist, and live-tool constraints before expecting the stage to run.
5. Use `next_strategy: select` only when a stage has real alternatives.
6. Keep `next` edges explicit so branch choices stay bounded.
7. Match `agent` to the intended permission boundary.
8. Match `skill` to the intended output shape.
9. Run the workflow once on a small request and inspect `/status`, `/session`, and Workflow Studio runtime diagnostics during approval or readiness pauses.

