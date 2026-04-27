# Workflow Graphs

GoFlow supports two workflow styles:

- built-in workflows such as `plan-fix-audit` and `skill-chain`
- runtime-home workflow graphs under `workflows/<name>/workflow.yaml`

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

For an end-to-end custom extension, see `examples/extension-workflow`. It includes a custom Python MCP server, a read-only agent snippet, a custom skill, and a graph workflow that composes the custom stage with a built-in audit stage.

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

`start` and `end` nodes are visual-only. They can be used to make a graph easier
to read and to define the first executable path, but they do not run LLM stages.
Executable nodes still need an `agent` and `skill`; `tool` and `params` are
included in the stage prompt as metadata.

Built-in workflows are shown for reference but cannot be overwritten or deleted.

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
    approval: false
    next_strategy: select
    next: [implement, audit]
    position: {x: 80, y: 120}
  - name: implement
    node_type: tool
    agent: fixer
    skill: code-writing
    tool: file_tools/write_file
    approval: true
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
- `stage.node_type`: optional visual type. Supported Studio values are `start`, `agent`, `skill`, `tool`, `custom`, and `end`.
- `stage.agent`: configured agent profile to run the stage.
- `stage.skill`: loaded skill name whose instructions are applied to the stage.
- `stage.tool`: optional tool metadata for a tool-focused stage.
- `stage.params`: optional key/value metadata included in the stage prompt.
- `stage.approval`: if `true`, pause before entering the stage unless the run is pre-approved.
- `stage.next`: candidate next stage names. If omitted, GoFlow continues to the next stage in file order.
- `stage.next_strategy`: optional branch strategy. Supported values include `select`, `best`, `conditional`, `planner_select`, `planner-select`, `dynamic`, and `graph`.
- `stage.position`: optional visual editor metadata. It does not affect execution.

Agent profile permissions remain the final tool boundary. A stage cannot grant itself write, exec, or network access just by naming a skill.

## HTTP Management API

Workflow execution endpoints:

- `POST /api/workflows/{name}`
- `POST /api/workflows/{name}/stream`

Workflow graph management endpoints:

- `GET /api/workflow-graphs`
- `POST /api/workflow-graphs`
- `GET /api/workflow-graphs/{name}`
- `PUT /api/workflow-graphs/{name}`
- `DELETE /api/workflow-graphs/{name}`
- `GET /api/workflow-options`

`/api/workflow-options` returns configured agents and loaded skills for editors.

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

Run it:

```bash
curl -s -X POST http://127.0.0.1:8080/api/workflows/release-check \
  -H "Content-Type: application/json" \
  -d '{"input":"improve CLI diff output"}'
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

- The stage names an agent absent from `configs/agent.yaml`.
- Run `/agents` to inspect configured profiles.

## Authoring Checklist

1. Keep each stage focused on one role.
2. Put write or exec stages behind `approval: true`.
3. Use `next_strategy: select` only when a stage has real alternatives.
4. Keep `next` edges explicit so branch choices stay bounded.
5. Match `agent` to the intended permission boundary.
6. Match `skill` to the intended output shape.
7. Run the workflow once on a small request and inspect `/status` / `/session` during approval pauses.

