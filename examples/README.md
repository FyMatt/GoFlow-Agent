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
| `custom-resource-pack` | an unknown domain with Kit routing, workflow execution and real file read/write/readback, without core changes |

Run `python scripts/resource_pack_acceptance.py --binary <current-goflow-binary>` for the custom-pack mechanism check. Its deterministic loopback Provider is not a real-model content-quality certification.

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

Validate deployment assets and examples from the repo root:

```bash
python scripts/validate_deployment_assets.py
python scripts/validate_extension_workflow.py
python scripts/validate_resource_links.py
```

The built-in domain packages in `kits/` cover multi-domain routing, software
engineering, web security, general security research, binary analysis,
documentation, operations, customer support, and framework extension work. They
are the recommended starting point for Web Studio users. This directory keeps
only the minimal extension example that is independently exercised in CI.
