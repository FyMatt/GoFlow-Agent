# Skill Authoring

[English](./skill-authoring.md) | [简体中文](./skill-authoring.zh-CN.md)

Skills are reusable workflow instructions stored under `skills/<name>/SKILL.md`.

Use skills when a task needs domain procedure, tool preferences, output structure, or repeatable workflow guidance.

## CLI Scaffolds

Use the built-in scaffold commands for common skill shapes:

```text
/skill-templates
/new-skill <template> <name>
```

Available templates:

- `code-writing`
- `code-audit`
- `web-vulnerability-research`
- `binary-vulnerability-research`
- `execution-plan`

`/new-skill code-audit custom-audit` creates `skills/custom-audit/SKILL.md`, validates it, and reloads skills.

Generated skills include metadata, tool declarations, activation keywords, preferred mode/agent, an example request, and extension notes. See [Scaffold Commands](./scaffolds.md) for the generated-file workflow and verification checklist.

You can also create or edit skills from the HTTP Studio:

```text
http://127.0.0.1:8080/console#catalog
```

The Skill editor writes `skills/<name>/SKILL.md`, validates the generated
frontmatter and instructions with the same parser used at startup, and reloads
skills when hot reload is available. Use the Resource builder beside it when you
want an agent to create a more specialized skill, agent, or tool through the
normal tool-approval path.

## Minimal Shape

Portable Claude/Codex-style skills only need `name`, `description`, and Markdown
instructions. GoFlow derives activation keywords from the name and description
when `activation.keywords` is omitted:

```markdown
---
name: image-workflow
description: Use when the user asks to generate, edit, or evaluate bitmap images.
metadata:
  short-description: Image workflow helper
---

# Image Workflow

Use this skill for image tasks. Keep generated assets and references organized.
```

GoFlow also accepts Claude-style `allowed-tools` / `allowed_tools` frontmatter
and maps common tool names into GoFlow tool metadata and tool-kind envelopes:

```yaml
allowed-tools: Read, Grep, WebFetch, WebSearch
```

The mapped values narrow the selected agent's tool-kind envelope by
intersection. They cannot grant tool kinds the agent did not already have, and
all calls still go through runtime approval policy.

## Complex Skill Scripts

Complex skills can declare deterministic helper scripts in frontmatter instead
of hiding them in prose. Use them for repeatable collection, parsing, or
normalization tasks that belong to the skill package but should still be routed
through normal approval and isolation policy.

Example:

```yaml
scripts:
  - name: collect-assets
    description: Fetch and summarize page assets for review.
    path: scripts/collect_assets.py
    runtime: python
    output: json
    timeout: 30s
    isolation: container
    workspace_mount: ro
    network: disabled
    approval: required
    args_schema:
      type: object
      additionalProperties: false
      properties:
        url:
          type: string
      required: [url]
```

GoFlow validates and exposes these declarations through the normal runtime:

- the script path must stay under `scripts/` in the skill folder
- the runtime, isolation, network, approval, timeout, and output fields are validated
- the arguments schema must stay object-shaped and closed by default
- declared scripts are exposed to the runtime prompt and resource APIs
- matched skills with declared scripts expose `skill_runner/run_script` only when the active agent profile still allows `exec`

Script execution is intentionally a normal tool call, not a hidden runtime
shortcut. The model must call:

```json
{"skill":"my-skill","script":"collect-assets","args":{"url":"https://example.test"}}
```

through `skill_runner/run_script`. That call then goes through the same agent
tool permission checks, approval policy, audit logging, risk metadata, and MCP
server isolation as any other `exec` tool.

The script receives `args` as JSON on stdin and in `GOFLOW_SKILL_ARGS_JSON`.
The result is a JSON object containing the skill name, script name, declared
runtime/output/network metadata, stdout, exit code, duration, timeout status,
and JSON-output validation status when `output: json` is declared.

The `isolation`, `network`, and `workspace_mount` fields document the script's
intent. The real boundary is the MCP server configuration. Use MCP
`isolation: container` for a strong Docker/Podman sandbox; the default local
binary config uses `process_group`, which is a lifecycle boundary rather than a
security sandbox.

This makes it possible to build complex skills with deterministic helper code
without letting `SKILL.md` become an executable code wrapper.

For native GoFlow skills, prefer the richer shape below:

```markdown
---
name: my-skill
description: Do a specific job. Use when the user asks for concrete trigger phrases and contexts.
version: 1.0.0
author: GoFlow
tools:
  - name: file_tools/read_file
    required: true
params:
  - name: target_path
    type: string
    description: Workspace file or directory to inspect.
    required: true
activation:
  keywords: ["my task", "specific trigger"]
  embedding_description: Short semantic description for matching.
mode: audit
preferred_agent: auditor
allowed_tool_kinds: [read]
output_kind: findings
next_skills: [code-writing]
---

## Role

State the specialist role in one paragraph.

## Workflow

1. Inspect only the necessary context.
2. Use the declared tools.
3. Separate evidence from hypotheses.
4. Produce the requested output shape.

## Rules

- Keep file access inside the workspace.
- Do not claim tool results that were not produced.
```

## Required Metadata

Portable fields:

- `name`
- `description`

Native GoFlow fields:

- `version`
- `author`
- `activation.keywords`

Recommended fields:

- `tools`
- `params`
- `mode`
- `preferred_agent`
- `allowed_tool_kinds`
- `output_kind`
- `next_skills`

`version`, `author`, and `activation.keywords` are required only for strict
native GoFlow skills. Imported Claude/Codex-style skills can omit them; GoFlow
will mark the skill as `claude-compatible`, `codex-compatible`, or
`portable-skill` and derive basic activation keywords from `name` and
`description`.

Supported modes:

- `chat`
- `plan`
- `fix`
- `audit`

`preferred_agent` is advisory. Runtime agent permissions still decide whether a tool may run.

`next_skills` declares recommended follow-up skills for workflow composition. It is shown in `/skills` and can be used by future orchestration layers.

`next_skills` is executable through the built-in workflow:

```text
/workflow skill-chain <request>
```

`skill-chain` matches the first skill, follows `next_skills`, and selects each stage's agent from `preferred_agent` or `mode`. The selected agent profile still controls final tool permissions.

By default, all declared `next_skills` run in order. For conditional branching, add metadata:

```yaml
metadata:
  next_strategy: select
```

With `select`, `best`, `conditional`, or `planner_select`, GoFlow scores the candidate `next_skills` against the original request plus the current stage output and runs only the best positive match.

The stage output may also choose explicitly:

```text
Next skill: code-audit
```

or:

```text
Next skills: code-writing, code-audit
```

Explicit choices are limited to the current skill's declared `next_skills`; they cannot jump to arbitrary undeclared skills.

## Graph-Backed Workflows

For workflows that need named stages, branching, or explicit agent assignment, create a runtime-home workflow blueprint:

```text
/new-workflow release-check
/workflow release-check review the auth changes
```

The generated `workflows/release-check/workflow.yaml` is executable. Its stage schema is:

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
  - name: audit
    agent: auditor
    skill: code-audit
```

Rules:

- `agent` must name a configured agent profile.
- `skill` must name an existing loaded skill.
- `approval: true` pauses before that stage unless the workflow invocation pre-approves it.
- `next` names candidate stage names, not arbitrary skills.
- `next_strategy: select` lets the current stage choose a declared next stage through `Next skill: ...` / `Next skills: ...` or by matching its output against candidate stage and skill names.
- Agent profile permissions still decide which tools may run.

For full branch-selection, stage-approval, and approval-resume examples, see [Workflow Graphs](./workflows.md).

## Trigger Design

Good descriptions and keywords are concrete:

- include user phrases in English and Chinese when needed
- include domain terms, not generic words only
- include the task outcome, such as "produce findings" or "make code changes"

Avoid broad keywords like `help`, `analyze`, or `project` unless the skill is intentionally general.

## Tool Declarations

Declare only tools the workflow naturally needs.

Examples:

- coding skill: `file_tools/list_tree`, `search_files`, `read_file`, `write_file`, optional `delete_file`
- web vulnerability skill: `web_tools/fetch_page_assets`, optional `fetch_url`, optional `web_search`
- binary vulnerability skill: `python_notes/binary_file_info`, `binary_strings`, optional `hex_preview`

Tool declarations document intent. They do not override the active agent profile.

## Workflow Quality Rules

Keep `SKILL.md` concise and procedural:

- define the first tool to use when obvious
- define what evidence to collect
- define how to classify uncertainty
- define the final answer format
- avoid long tutorials or generic background

Use separate reference files only when the skill needs bulky domain material.

## Complex Skill Layout

Skills can be full directories, not only one prompt file:

```text
skills/domain-review/
  SKILL.md
  references/
    policy.md
    schemas.md
  scripts/
    collect_context.py
  assets/
    report-template.md
  agents/
    openai.yaml
```

GoFlow indexes bundled files under `references/`, `scripts/`, `assets/`,
`templates/`, and `agents/` and exposes that resource manifest on the loaded
skill and in the matched-skill prompt. Keep `SKILL.md` as the routing and
workflow guide, then move large domain knowledge, deterministic scripts, and
templates into bundled resources.

HTTP Studio can manage those bundled files through the skill resource file API:

```text
GET    /api/resources/skills/{name}/files
GET    /api/resources/skills/{name}/files/{relative-path}
PUT    /api/resources/skills/{name}/files/{relative-path}
DELETE /api/resources/skills/{name}/files/{relative-path}
```

`relative-path` must stay inside one of the resource directories:
`references/`, `scripts/`, `assets/`, `templates/`, or `agents/`. `SKILL.md`
itself is managed only by `GET/PUT /api/resources/skills/{name}` so the skill's
main metadata and instructions continue to be validated as one document.

Example write request:

```json
{"content":"# Review Policy\n\nEscalate high-risk findings with evidence."}
```

The file API rejects absolute paths, `..` traversal, backslash paths, symlinks,
directories, and resource paths outside the skill folder.

## Built-In Skill Patterns

Use these built-in skills as templates:

- `skills/code-writing/SKILL.md`
- `skills/code-audit/SKILL.md`
- `skills/execution-plan/SKILL.md`
- `skills/web-vulnerability-research/SKILL.md`
- `skills/binary-vulnerability-research/SKILL.md`

The CLI scaffold templates generate the same metadata shape and are a good starting point when creating a new skill.

## Validation Checklist

1. Folder name matches normalized `name`.
2. `description` says when to use the skill.
3. `activation.keywords` include realistic user phrases.
4. Required tools exist in `/tools`.
5. `mode` and `preferred_agent` match the intended permission boundary.
6. Output format is explicit enough for downstream workflow stages.
7. The skill does not ask the agent to use unavailable tools.

