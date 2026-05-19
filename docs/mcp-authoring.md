# MCP Tool Authoring

[English](./mcp-authoring.md) | [简体中文](./mcp-authoring.zh-CN.md)

This guide describes how to add a custom MCP server to GoFlow.

## CLI Scaffold

Create a Python MCP server starter with:

```text
/new-tool python <name>
```

The scaffold is written to `mcp_servers/<name>.py`. It includes stdio JSON-RPC handling, `tools/list`, `tools/call`, and workspace path checking against `GOFLOW_WORKSPACE_ROOT`.

GoFlow validates the generated scaffold before writing it. The generated file must contain the workspace boundary, JSON-RPC tool methods, and schema `additionalProperties` guards.

The generated Python server includes `ping`, `read_text`, and `write_text` examples so both read and write tool shapes are visible. Its path helper follows the built-in Python MCP safety pattern: it resolves relative and absolute inputs under `GOFLOW_WORKSPACE_ROOT`, rejects traversal, rejects symlink escapes for existing paths and write parents, returns `relative_path` metadata, bounds text reads, and reports tool-level failures as `is_error` results instead of crashing the JSON-RPC loop. See [Scaffold Commands](./scaffolds.md) for the generated-file workflow, config snippet, and verification checklist.

## HTTP And Studio Scaffold Presets

Browser Studio and API clients can create the same kind of Python MCP starter
without shell access:

```text
GET /api/resources/tools/scaffolds
GET /api/resources/tools/scaffolds/{preset}?name=<tool-name>
POST /api/resources/tools/scaffolds/{preset}
```

Available presets are:

- `python-local`: local Python stdio server using `isolation: process_group`
- `python-container-readonly`: Docker/Podman server with read-only workspace
  access and network disabled
- `python-container-writer`: Docker/Podman server with write-capable workspace
  access and network disabled
- `python-container-network`: Docker/Podman server with read-only workspace
  access and explicit network egress

Preset responses include Studio-facing guidance fields:

- `capabilities`: what the generated server is meant to demonstrate
- `safety_guards`: concrete backend/runtime checks included in the generated
  code and config
- `generated_paths`: files the scaffold will write under the runtime home
- `activation_steps`: operator steps before the new MCP server is active
- `recommendations`: permission and approval guidance for the selected preset
- `default_image` and `default_isolation_options`: container preset defaults
  suitable for list-card previews before a named document is generated

The `POST` body may be JSON or YAML and can set `name`, `description`, `image`,
`runtime`, `workspace_mount`, `network`, `overwrite`, and extra
`isolation_options`. When `image` is omitted, release builds default to the
matching `ghcr.io/fymatt/goflow-agent-mcp-python:<version>` image tag, while
development builds fall back to `latest`. Container presets generate
`mcp_servers/<name>.py` and a matching
`configs/mcp_servers/<name>.yaml` that mounts only that generated tool file into
the container through `tool_source`, `tool_target`, and `tool_mount: ro`. The
generated server uses the same workspace-root and symlink-escape checks as the
CLI scaffold. The new server is visible as a saved resource immediately and
becomes active after restart or a future MCP rebootstrap.

Container presets also generate hardened defaults for resource limits,
read-only rootfs, `no_new_privileges`, `cap_drop: all`, tmpfs scratch mounts,
`init: "true"`, `ipc: none`, and `userns: auto`. Override `userns` with
`nomap`, `keep-id`, or remove it when the selected Docker/Podman runtime does
not support automatic user namespaces.

`POST /api/resources/tools/{name}/validate` and `PUT
/api/resources/tools/{name}` reuse the same MCP server config checks as runtime
startup. Studio/API clients therefore get early validation for missing startup
allowlists, invalid `isolation_options`, and unsupported isolation values such
as `windows_appcontainer` before any MCP module is saved. Validation responses
include field-level issue metadata such as `isolation`,
`isolation_options.network`, or `allowed_commands` plus stable issue codes, so
forms can highlight the exact invalid MCP field. A failed `PUT` returns the
same validation envelope with HTTP 400 rather than a plain-text-only error.

For a full extension that wires a custom Python MCP server into a custom agent and workflow graph, see `examples/extension-workflow`.

Validate that example with:

```bash
python scripts/validate_extension_workflow.py
```

## Contract

GoFlow currently supports stdio JSON-RPC MCP servers. A server must support:

- `tools/list`
- `tools/call`

Each response is one JSON object per line.

### `tools/list`

Return:

```json
{
  "tools": [
    {
      "name": "read_example",
      "description": "Read an example file from the workspace.",
      "kind": "read",
      "input_schema": {
        "type": "object",
        "properties": {
          "path": {"type": "string"}
        },
        "required": ["path"],
        "additionalProperties": false
      }
    }
  ]
}
```

GoFlow validates advertised tool metadata during discovery. A malformed tool is rejected before it is exposed to an agent.

Tool names must use only ASCII letters, digits, `_`, or `-`, and must not include `/`, whitespace, or control characters.

Tool kinds are required and must be one of:

- `read`
- `write`
- `exec`
- `network`
- `unknown`

`input_schema` is required and must be a strict object schema:

- top-level `"type": "object"`
- a top-level `"properties"` object, even when empty
- top-level `"additionalProperties": false`
- every `required` field must be declared in `properties`

### `tools/call`

Input params:

```json
{
  "name": "read_example",
  "arguments": {"path": "README.md"}
}
```

Return:

```json
{
  "content": "{\"path\":\"README.md\",\"content\":\"...\"}",
  "is_error": false
}
```

`content` should be a string. For structured results, encode JSON into that string so the CLI can summarize paths, URLs, bytes, lines, and diff previews.

## Workspace Boundary

Every file-oriented tool must treat `GOFLOW_WORKSPACE_ROOT` as the only filesystem root.

Required behavior:

- Resolve relative paths under `GOFLOW_WORKSPACE_ROOT`.
- Reject `..` traversal outside the workspace.
- Reject symlink escapes outside the workspace.
- Reject deleting the workspace root.
- Keep reads and binary previews bounded.
- Make destructive operations explicit in the schema, such as `recursive: true`.

GoFlow inspects discovered schemas for path-like inputs such as `path`, `file`,
`directory`, `folder`, and `workdir`. The inspection walks nested `properties`,
array `items`, `oneOf` / `anyOf` / `allOf`, `definitions`, and `$defs` so batch
or object-shaped tools are still flagged when a nested field carries a path.
Matching tools are annotated with `risk.workspace_scoped_inputs=true` in
`/api/resources/tools`, approval events, and durable run snapshots. Built-in
workspace-scoped servers such as `file_tools` are marked with
`workspace_scope_enforced=true`; third-party tools are conservatively marked
false unless the backend can recognize their safety boundary, so the tool
implementation itself must enforce `GOFLOW_WORKSPACE_ROOT` and reject traversal
or symlink escapes.

The built-in Go and Python tools are reference implementations:

- `mcp_servers/file_tools/main.go`
- `mcp_servers/network_tools/main.go`
- `mcp_servers/skill_runner/main.go`
- `mcp_servers/python_notes.py`

For vertical operations tools, use `network_tools` as the reference contract.
It deliberately returns device discovery, command, and configuration dry-run
plans without opening network connections. A real SSH/API connector should keep
the same authorization, host allowlist, command allowlist, dry-run, rollback,
approval, and evidence-ref fields so workflows can stay audit-friendly and
token-efficient.

## Config

Add the server as a modular MCP config, for example
`configs/mcp_servers/my_tools.yaml`:

```yaml
# Loaded automatically from configs/mcp_servers/*.yaml.
mcp_servers:
  - name: my_tools
    command: python
    args:
      - ./mcp_servers/my_tools.py
    enabled: true
    timeout: 30s
    workdir: .
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, TMP, TEMP]
    isolation: process_group
    restart_limit: 3
    cooldown: 10s
    max_concurrent_calls: 1
    allowed_commands:
      - python
    max_request_bytes: 65536
    max_response_bytes: 2097152
```

Use a unique server name. If two servers expose the same short tool name, GoFlow will require the qualified form, such as `my_tools/read_example`.

Enabled MCP servers must constrain their startup command with `allowed_commands` or `allowed_command_paths`. If `env_allowlist` is omitted, GoFlow passes only minimal platform variables plus `GOFLOW_WORKSPACE_ROOT`; add explicit names when the tool needs `PATH`, `HOME`, proxy settings, or language runtime variables.

## CLI Diagnostics

Run:

```text
/reload-tools
/tools
```

`/tools` shows:

- qualified tool name
- server
- kind
- health
- compact schema summary
- warnings for ambiguous short names or non-fatal schema issues

Discovery-time metadata errors stop the refresh and must be fixed before relying on the tool in agent workflows.

## Python Validation Harness

The built-in Python server can be validated with:

```bash
python scripts/validate_python_mcp.py
```

Use that script as a template for custom Python MCP server checks:

- create a temporary workspace
- set `GOFLOW_WORKSPACE_ROOT`
- send JSON-RPC lines over stdin
- validate structured responses
- validate workspace escape rejection
- validate symlink escape rejection where the platform supports symlinks

## Testing Checklist

Before shipping a new tool:

1. Confirm it appears in `/tools`.
2. Confirm the schema warning list is empty.
3. Confirm relative paths stay inside the workspace.
4. Confirm path traversal and symlink escapes are rejected.
5. Confirm large outputs are bounded.
6. Confirm write/delete tools require approval under the intended agent profile.

