# Skills

[English](./skills.md) | [简体中文](./skills.zh-CN.md)

## Overview

Skills are stored as folders under `skills/`, each containing a `SKILL.md` file.

A skill has two parts:
1. YAML frontmatter for structured metadata
2. Markdown instructions for runtime guidance

## Runtime behavior

Skill files are loaded from the runtime home, not from the external workspace.

That means when you run:

```bash
go run ./cmd/goflow --workspace D:/empty-project
```

GoFlow still loads skills from its own `skills/` directory while using `D:/empty-project` as the target workspace for tool operations.

## Supported metadata

Current frontmatter fields include:

- `name`
- `description`
- `version`
- `author`
- `format`
- `tools`
- `allowed-tools` / `allowed_tools`
- `params`
- `scripts`
- `activation`
- `mode`
- `preferred_agent`
- `allowed_tool_kinds`
- `output_kind`
- `next_skills`
- `metadata`

GoFlow accepts native GoFlow skills as well as portable Claude/Codex-style
skills. The portable baseline is a folder with `SKILL.md` whose frontmatter has
`name` and `description`; GoFlow derives activation keywords when
`activation.keywords` is omitted. Native GoFlow skills can add the richer fields
above for explicit matching, workflow composition, tool envelopes, and output
contracts.

## Example

```markdown
---
name: code-audit
description: Review source code for security risks.
version: 1.0.0
author: GoFlow
tools:
  - name: file_tools/read_file
    required: true
params:
  - name: target_path
    type: string
    description: File or directory to inspect
    required: true
activation:
  keywords: ["audit", "审计", "security review"]
  embedding_description: Audit source code for vulnerabilities.
mode: audit
preferred_agent: auditor
allowed_tool_kinds: [read, unknown]
output_kind: findings
next_skills: [code-writing]
metadata:
  domain: security
---

## Workflow

1. Inspect the target
2. Identify risk boundaries
3. Produce a structured report
```

## Validation rules

- skill folders and skill `name` fields may use letters, numbers, spaces, underscores, or hyphens
- `name` and `description` are always required
- folder name and normalized skill name must align for strict native GoFlow skills
- `version`, `author`, and at least one activation keyword are required for strict native GoFlow skills
- portable Claude/Codex-style skills may omit `version`, `author`, and `activation`; basic activation keywords are derived from `name` and `description`
- referenced tools must have non-empty names
- bundled resources under `references/`, `scripts/`, `assets/`, `templates/`, and `agents/` are indexed as skill resources
- declared `scripts` must point under the skill's `scripts/` directory and are executable only through `skill_runner/run_script` when the active agent still allows `exec`

## Bundled resource API

HTTP Studio and external clients can edit complex skill resources without
rewriting `SKILL.md`:

```text
GET    /api/resources/skills/{name}/files
GET    /api/resources/skills/{name}/files/{relative-path}
PUT    /api/resources/skills/{name}/files/{relative-path}
DELETE /api/resources/skills/{name}/files/{relative-path}
```

The file API is scoped to the runtime skill directory and only accepts resource
paths under `references/`, `scripts/`, `assets/`, `templates/`, or `agents/`.
Use `GET/PUT /api/resources/skills/{name}` for the main `SKILL.md` metadata and
instructions. Resource writes reload skills when hot reload is available, so
new files appear in the loaded skill resource manifest.

## Declared helper scripts

Complex skills may declare deterministic helper scripts in `SKILL.md`:

```yaml
scripts:
  - name: collect-assets
    path: scripts/collect_assets.py
    runtime: python
    output: json
    timeout: 30s
    workspace_mount: ro
    approval: required
    args_schema:
      type: object
      additionalProperties: false
      properties:
        url:
          type: string
      required: [url]
```

When such a skill matches, GoFlow can expose `skill_runner/run_script` as an
`exec` tool. The active agent profile and runtime policy still decide whether
that tool is available, whether approval is required, and how the MCP server is
isolated. The script receives JSON arguments on stdin and in
`GOFLOW_SKILL_ARGS_JSON`; the tool returns a JSON payload with stdout, exit
code, duration, timeout status, declared metadata, and JSON-output validation
when requested.

Use `isolation: container` in the `skill_runner` MCP server configuration when
script execution needs a real Docker/Podman sandbox. Script metadata documents
intent, but enforcement comes from the MCP server configuration.

## Matching

The current matcher is keyword-based.

It normalizes separators and case across:
- query text
- skill names
- descriptions
- activation keywords

Scoring rules:
- activation keyword matches are weighted highest
- skill name matches are weighted next
- description matches are weighted lowest
- equal positive scores are resolved deterministically by skill name

The runtime also records the latest match diagnostic in session state. `/status` shows the matched skill, score, keyword hits, and reason so operators can understand why a skill activated.

## Runtime use

When a skill matches, it can influence:
- system prompt context
- reported output kind
- operator-facing structured sections
- preferred-agent diagnostics
- `/status` skill-match diagnostics
- `/skills` follow-up skill hints from `next_skills`
- the workflow and session information later surfaced through CLI or HTTP inspection
- the matched-skill prompt with a manifest of bundled skill resources
- declared helper scripts through `skill_runner/run_script`, subject to the active agent's `exec` permission and normal approval policy

Skill metadata does not override runtime agent authorization. In particular:
- `preferred_agent` is advisory; the runtime may still execute with a different active agent and records mismatches in the audit trail
- `allowed_tool_kinds` on the skill narrows the selected agent's tool-kind envelope by intersection; it cannot grant tool kinds the agent did not already have
- the active agent's `tool_policy`, `allowed_tool_kinds`, and `allowed_tools` remain the final gate for tool execution

## Built-in baseline skills

The default runtime includes baseline skills for common framework usage:

- `code-writing`: focused implementation, refactoring, bug fixing, and verification
- `code-audit`: evidence-backed source review and security/engineering findings
- `vulnerability-research`: attack-surface and exploitability investigation with optional web advisory context
- `web-vulnerability-research`: fetch a target page plus JavaScript/CSS assets and inspect client-side vulnerability signals
- `binary-vulnerability-research`: triage binaries and reversing artifacts with Python binary metadata, strings, and hex preview tools
- `execution-plan`: implementation plans designed for later fixer/auditor handoff
- `reverse-engineering`: static reverse-engineering assessment from workspace artifacts

These skills are intentionally linked through `next_skills` and metadata such
as `recommended_workflow` and `recommended_team`. For example,
`web-vulnerability-research` can hand off to `vulnerability-research` or
`code-audit`, while `reverse-engineering` can hand off to
`binary-vulnerability-research`. Web Studio should surface those links as
suggested follow-up skills instead of treating each skill as an isolated prompt.

For complex domains, keep `SKILL.md` as the concise workflow guide and put
deterministic helpers under `scripts/`, larger references under `references/`,
and reusable output templates under `templates/`. Declared scripts still run
only through `skill_runner/run_script` and remain subject to the active Agent's
`exec` permission, approval policy, audit logging, and MCP isolation.

## CLI scaffolds

Use scaffold commands to create a validated starter skill:

```text
/skill-templates
/new-skill <template> <name>
```

Example:

```text
/new-skill code-audit custom-audit
```

This creates `skills/custom-audit/SKILL.md`, validates it, reloads skills, and makes it visible in `/skills`.
Skill scaffold templates are YAML-backed. Built-ins are embedded from
`internal/scaffold/templates/skills/scaffolds/templates.yaml`; runtime homes can
add or override templates with `templates/skills/scaffolds/*.yaml`.

## Skill-chain workflow

Run the matched skill and its declared `next_skills` with:

```text
/workflow skill-chain <request>
```

Each stage chooses an agent from the skill's `preferred_agent` first, then from the skill `mode`. The runtime still enforces the selected agent profile's permissions, so a skill cannot grant itself write, exec, or network access.

By default, all `next_skills` run in their listed order. A skill can request conditional branching with:

```yaml
metadata:
  next_strategy: select
```

Supported conditional aliases are `select`, `best`, `conditional`, `planner_select`, and `planner-select`. In that mode GoFlow scores candidate next skills against the original request plus the current stage output and runs only the best positive match.

Planner-authored stage outputs can override scoring with an explicit directive, limited to the current skill's declared candidates:

```text
Next skill: code-audit
Next skills: code-writing, code-audit
```

## Custom workflow graphs

For reusable multi-stage flows with explicit agents, create a runtime-home workflow graph:

```text
/new-workflow release-check
/workflow release-check review this change
```

The graph file lives at `workflows/release-check/workflow.yaml` and can declare stage `agent`, `skill`, optional `approval`, optional `next_strategy`, and optional `next` stage edges. Stage skills still use normal skill metadata and instructions, and the selected agent profile remains the final permission boundary.

## Authoring guidance

- keep descriptions concrete
- declare only the tools the workflow truly needs
- specify the intended mode when it shapes the output style
- use `preferred_agent` when a workflow is clearly best handled by one runtime profile
- define `output_kind` when downstream consumers may want predictable result categories
- avoid claiming behaviors that the available MCP tools cannot support

## Using skills with external workspaces

A practical pattern is:

1. keep reusable domain behavior in `skills/`
2. start GoFlow against a fresh external workspace
3. let the skill drive MCP tools against that workspace

This makes it possible to build domain-specific agents on top of the same framework by combining:
- runtime agent profiles
- custom `SKILL.md` definitions
- custom MCP tools (in Go, Python, or other languages)

For a practical skill template and validation checklist, see [Skill Authoring](./skill-authoring.md).

