# Skills

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
- `tools`
- `params`
- `activation`
- `mode`
- `preferred_agent`
- `allowed_tool_kinds`
- `output_kind`
- `next_skills`
- `metadata`

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
  keywords: ["audit", "瀹¤", "security review"]
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
- folder name and normalized skill name must align
- `name`, `description`, `version`, and `author` are required
- at least one activation keyword is required
- referenced tools must have non-empty names

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

Skill metadata does not override runtime agent authorization. In particular:
- `preferred_agent` is advisory; the runtime may still execute with a different active agent and records mismatches in the audit trail
- `allowed_tool_kinds` on the skill describes the workflow's intended envelope, but actual MCP execution still depends on the current runtime agent profile
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
- `weekly-report`: engineering work summary drafts

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

