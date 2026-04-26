# MCP Tool Authoring

This guide describes how to add a custom MCP server to GoFlow.

## CLI Scaffold

Create a Python MCP server starter with:

```text
/new-tool python <name>
```

The scaffold is written to `mcp_servers/<name>.py`. It includes stdio JSON-RPC handling, `tools/list`, `tools/call`, and workspace path checking against `GOFLOW_WORKSPACE_ROOT`.

GoFlow validates the generated scaffold before writing it. The generated file must contain the workspace boundary, JSON-RPC tool methods, and schema `additionalProperties` guards.

The generated Python server includes `ping`, `read_text`, and `write_text` examples so both read and write tool shapes are visible. See [Scaffold Commands](./scaffolds.md) for the generated-file workflow, config snippet, and verification checklist.

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

The built-in Go and Python tools are reference implementations:

- `mcp_servers/file_tools/main.go`
- `mcp_servers/python_notes.py`

## Config

Add the server to `configs/agent.yaml`:

```yaml
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

## Testing Checklist

Before shipping a new tool:

1. Confirm it appears in `/tools`.
2. Confirm the schema warning list is empty.
3. Confirm relative paths stay inside the workspace.
4. Confirm path traversal and symlink escapes are rejected.
5. Confirm large outputs are bounded.
6. Confirm write/delete tools require approval under the intended agent profile.

