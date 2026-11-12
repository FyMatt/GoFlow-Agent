# Skill Authoring

A Skill packages focused instructions, references, templates, deterministic scripts, and acceptance rules. GoFlow follows common `SKILL.md` conventions and can also consume repository `AGENTS.md` and `CLAUDE.md` instructions.

## Minimal Shape

```markdown
---
name: source-verification
description: Verify internet claims against independent sources.
---

# Source Verification

Use when a task needs claim-to-source evidence.

## Steps

1. Record the claim and scope.
2. Collect authoritative and independent sources.
3. Report agreement, conflict, date, and limitations.

## Acceptance

- Every important claim maps to a source.
- Conflicts and uncertainty remain explicit.
```

Keep the trigger specific. Put long references in `references/`, reusable document inputs in `templates/`, and deterministic helpers in `scripts/`.

## Declared Scripts

Scripts must be declared by name and remain under the Skill directory:

```yaml
scripts:
  - name: compile-evidence
    path: scripts/compile_evidence.py
    runtime: python
    output: json
    timeout: 30s
    approval: required
    args_schema:
      type: object
      additionalProperties: false
      properties:
        input:
          type: string
      required: [input]
```

Execution uses `skill_runner/run_script`, fixed schemas, agent permissions, inline approval, audit logging, task workspace scope, and the MCP native isolation policy. Script metadata describes intent; it cannot grant permissions. Third-party scripts use an MCP connection configured with `auto` or `native_strong`.

## Quality

- State exact inputs, outputs, and acceptance evidence.
- Prefer deterministic code for parsing, file transforms, validation, and export.
- Do not hide network, write, exec, or host changes in prose.
- Do not embed API keys, SSH keys, passwords, or personal data.
- Return usable artifacts and validation, not only narrative.

See [Skills](./skills.md), [MCP](./mcp.md), and [resources](./resources.md).
