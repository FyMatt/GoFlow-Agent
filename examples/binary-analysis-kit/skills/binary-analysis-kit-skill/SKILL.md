---
name: binary-analysis-kit-skill
description: Linked starter skill for planning, evidence collection, review, and handoff inside a generated kit workflow.
version: 1.0.0
author: GoFlow Studio
format: goflow
mode: chat
preferred_agent: binary-analysis-kit-agent
allowed_tool_kinds:
    - read
output_kind: structured_handoff
priority: 50
max_iterations: 4
tools:
    - name: binary-analysis-kit-helper/binary_file_info
      required: true
    - name: binary-analysis-kit-helper/binary_strings
      required: true
    - name: binary-analysis-kit-helper/hex_preview
      required: false
    - name: binary-analysis-kit-helper/read_text
      required: false
params:
    - name: request
      type: string
      description: User task or workflow stage request.
      required: true
    - name: scope
      type: string
      description: Bounded domain, repository, target, or operating scope.
      required: false
    - name: upstream
      type: string
      description: Compact output from an earlier workflow node.
      required: false
activation:
    keywords:
        - binary-analysis-kit
        - binary-analysis-kit-skill
        - starter-kit
        - workflow-handoff
    embedding_description: Use for generated starter kit workflows that need planning, evidence, review, and concise downstream handoff.
metadata:
    folder: binary-analysis-kit-skill
    format: goflow
    generated_agent: binary-analysis-kit-agent
    generated_policy_rule: binary-analysis-kit-gate
    generated_tool: binary-analysis-kit-helper
    generated_workflow: binary-analysis-kit-workflow
    kit: binary-analysis-kit
---

## Role

Operate as a reusable workflow skill for this kit. Produce compact, structured outputs that can be passed to the next workflow node.

## Workflow

1. Restate the current task, scope, assumptions, and missing inputs.
2. If the request references a binary or opaque artifact, use `binary_file_info` first, then `binary_strings`, and `hex_preview` when offsets or headers need a closer look.
3. Use `read_text` only for analyst notes, reports, or decompiled text that is already UTF-8 text.
4. Emit clear sections for Findings, Decisions, Risks, Next Inputs, and Acceptance Criteria.
5. Keep large raw evidence out of the final answer; reference artifact IDs or file paths instead.
6. When blocked, state the exact missing input or approval required so the workflow can route correctly.
