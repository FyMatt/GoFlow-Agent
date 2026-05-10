# Examples

[English](./README.md) | [简体中文](./README.zh-CN.md)

This directory contains copyable examples for GoFlow second development.

Most users should start with the built-in kits in `kits/` or the Web Studio
starter cards. Use this directory when you want a complete file-level example
that can be copied into another runtime home or validated in CI.

## When to use each example

| Example | Use it for |
| --- | --- |
| `extension-workflow` | the smallest end-to-end extension path: custom MCP tool, custom agent, custom skill, and a graph workflow |
| `binary-analysis-kit` | a production-style domain package that links Agent, Skill, Tool, Team, Workflow Template, Policy Rule, and Kit resources |

## `extension-workflow`

A compact extension smoke test. It combines:

- a Python MCP server
- a modular MCP server config
- a narrow read-only agent
- a custom skill
- a workflow graph

Use it when you want to understand the smallest end-to-end extension path.
Keep this example around as the minimal file-level smoke test even if you mainly
use the richer built-in kits.

Validate it before copying:

```bash
python scripts/validate_extension_workflow.py
```

## `binary-analysis-kit`

A fuller materialized kit example. It connects:

- a domain agent
- a GoFlow-native skill
- a Docker/Podman-isolated Python MCP helper
- a workflow
- a workflow template
- a team template
- a policy rule
- a kit manifest

Use it when you want to see how Agent, Skill, Tool, Team, Workflow Template,
Policy Rule, and Kit resources reference each other in a production-style
domain package.

Validate deployment assets and examples from the repo root:

```bash
python scripts/validate_deployment_assets.py
python scripts/validate_extension_workflow.py
python scripts/validate_binary_analysis_kit.py
python scripts/validate_resource_links.py
```

The built-in domain packages in `kits/` cover multi-domain routing, software
engineering, web security, general security research, binary analysis,
documentation, operations, customer support, and framework extension work. They
are the recommended starting point for Web Studio users; the examples here are
kept as inspectable reference bundles.
