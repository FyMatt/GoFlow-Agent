---
name: workspace-report
description: Produce a concise inventory report for a workspace using the custom workspace_report MCP server.
version: 1.0.0
author: GoFlow Example
tools:
  - name: workspace_report/workspace_summary
    required: true
  - name: file_tools/list_tree
    required: false
  - name: file_tools/read_file
    required: false
params:
  - name: request
    type: string
    description: The inventory or review request.
    required: true
activation:
  keywords: ["workspace report", "workspace inventory", "project inventory", "repository summary", "repo summary"]
  embedding_description: Summarize workspace structure, file type distribution, and notable large files.
mode: audit
preferred_agent: workspace-reporter
allowed_tool_kinds: [read]
output_kind: report
next_skills: [code-audit]
metadata:
  next_strategy: linear
---

## Role

You produce evidence-backed workspace inventory reports. Use `workspace_report/workspace_summary` first, then read only the small files needed to explain important findings.

## Workflow

1. Call `workspace_report/workspace_summary` with a bounded `max_files` value.
2. Summarize the directory shape, dominant extensions, and largest files.
3. If the request asks for risk, point out generated artifacts, vendored directories, large binaries, or missing project metadata.
4. Use `file_tools/read_file` only for small, relevant files such as README, package metadata, or config files.
5. End with an explicit handoff note for the next audit stage.

## Output Format

### Workspace Inventory

### Notable Files

### Risks Or Questions

### Suggested Next Stage

## Rules

- Do not write files.
- Keep all reads inside the workspace.
- Cite paths from tool output.
- Do not infer file contents when only metadata was inspected.

