# MCP authoring

MCP servers are standard stdio processes. GoFlow accepts Codex/Claude-compatible connection documents and standard MCP tool schemas.

## Recommended server contract

- keep tool names stable and namespaced by the connection;
- declare a precise JSON Schema with `additionalProperties: false`;
- classify each tool as read, write, exec, network, or unknown;
- validate every workspace path and reject traversal and symlink escapes;
- return bounded UTF-8 results with an explicit error flag;
- keep generated code deterministic and reviewable;
- never embed API keys or private keys in code, arguments, logs, or artifacts.

## Native execution

Connections use `isolation: auto`. GoFlow selects the smallest native boundary that satisfies the tool capability and the host probe. Unknown or third-party servers run inside a task copy and are refused when the required boundary is unavailable.

## Built-in scaffolds

`GET /api/resources/tools/scaffolds` lists local Python and native helper templates. A scaffold contains the server source, config, schemas, path checks, bounded output, and activation instructions. It does not ask ordinary users for low-level sandbox fields.

The built-in `network_tools` connection is the reference for authorized network operations: every mutating action requires explicit approval and emits structured evidence for replay and rollback review.

## Compatibility

Import standard MCP stdio definitions from Codex or Claude Code, then review the displayed Chinese capability summary before enabling the connection. Exported definitions remain portable and do not include secrets.
