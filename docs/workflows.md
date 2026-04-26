# Workflow Graphs

GoFlow supports two workflow styles:

- built-in workflows such as `plan-fix-audit` and `skill-chain`
- runtime-home workflow graphs under `workflows/<name>/workflow.yaml`

Use workflow graphs when a task needs named stages, explicit agent assignment, branch selection, or approval boundaries.

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

## Stage Schema

```yaml
name: release-check
description: Review and improve a change.
stages:
  - name: triage
    agent: planner
    skill: execution-plan
    approval: false
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
    approval: false
```

Fields:

- `name`: workflow name. It must match the folder name used by `/workflow <name>`.
- `description`: optional operator-facing purpose.
- `stages`: ordered stage list. The first stage is the entry point.
- `stage.name`: stage id used by `next` edges and status output.
- `stage.agent`: configured agent profile to run the stage.
- `stage.skill`: loaded skill name whose instructions are applied to the stage.
- `stage.approval`: if `true`, pause before entering the stage unless the run is pre-approved.
- `stage.next`: candidate next stage names. If omitted, GoFlow continues to the next stage in file order.
- `stage.next_strategy`: optional branch strategy. Supported values include `select`, `best`, `conditional`, `planner_select`, `planner-select`, `dynamic`, and `graph`.

Agent profile permissions remain the final tool boundary. A stage cannot grant itself write, exec, or network access just by naming a skill.

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

