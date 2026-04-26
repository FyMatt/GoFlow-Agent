# MCP Integration

## Current scope

GoFlow Agent currently supports stdio-based MCP servers.

The MCP layer is split into:
- `internal/mcp/client.go` - process lifecycle, RPC I/O, env injection, and restart behavior
- `internal/mcp/manager.go` - configured server registry, cached tools, and routing

## What the manager does

The manager exposes a narrow runtime-facing API:

- `ListTools`
- `RefreshTools`
- `CallTool`
- `HealthStatus`
- `ToolNames`

Behavior today:
- cached tool lists are reused after discovery
- the first `ListTools` call refreshes lazily if needed
- `RefreshTools` rebuilds the cache and routing index explicitly
- `RefreshTools` rejects malformed third-party tool metadata before it reaches an agent
- `HealthStatus` reports server availability for operator-facing status output

## Naming and routing

Each discovered tool is tagged with its MCP server name.

Tools can be addressed by:
- short name, such as `read_file`
- qualified name, such as `file_tools/read_file`

When tools come from named servers, the CLI prefers the qualified form for display.

Discovery validates each advertised tool:

- `name` must be non-empty and match `[A-Za-z0-9_-]+`
- `kind` must be one of `read`, `write`, `exec`, `network`, or `unknown`
- `input_schema` must be a strict object schema with `properties` and `additionalProperties=false`
- duplicate tool names from the same server are rejected

## Runtime home and workspace propagation

Built-in MCP servers are now launched from runtime-home-relative paths while being pointed at the active workspace with:

```text
GOFLOW_WORKSPACE_ROOT=<workspace path>
```

This means:
- the server code can live inside the GoFlow project
- the server still reads/writes files only inside the target workspace
- GoFlow can be launched against an external empty folder without copying runtime assets into that folder

## Built-in `file_tools` server (Go)

### Tools

- `read_file`
- `write_file`
- `delete_file`
- `list_dir`
- `list_tree`
- `file_info`
- `search_files`

### Behavior

- validates explicit paths
- resolves relative paths under the workspace root
- rejects path escape attempts
- rejects symlink escapes that resolve outside the workspace root
- rejects directory reads through `read_file`
- enforces request and response byte limits
- returns structured JSON payloads
- creates parent directories for writes when needed
- only deletes directories when `recursive=true` is explicitly provided

## Built-in `web_tools` server (Go)

### Tools

- `web_search`
- `fetch_url`
- `fetch_page_assets`

### Behavior

- classifies tools as `network`
- searches the public web through DuckDuckGo Lite and returns compact result metadata
- fetches HTTP(S) URLs with byte limits, status, content type, title, text preview, and truncated content
- fetches an HTML page plus referenced JavaScript and CSS assets for source-oriented web security review
- rejects non-HTTP(S) URL schemes

## Built-in `python_notes` server (Python)

### Tools

- `write_note`
- `read_note`
- `python_ast_summary`
- `json_query`
- `binary_file_info`
- `binary_strings`
- `hex_preview`

### Purpose

This server proves cross-language MCP support and hosts Python-native inspection helpers that are awkward or less useful to duplicate in Go.

It shows that MCP tools do **not** need to be implemented in Go. Any language can be used as long as the process can:
- start from the configured command
- speak the expected stdio/JSON-RPC protocol
- return the expected response structure

All Python tools resolve paths under `GOFLOW_WORKSPACE_ROOT` and reject path traversal or symlink escapes outside the active workspace.

The extra inspection tools support:
- Python source structure summaries through `python_ast_summary`
- bounded JSON field extraction through `json_query`
- binary triage metadata, strings, and hex previews through the binary helpers

## Runtime behavior

The stdio client is designed to be restartable:

- starts processes lazily
- resets internal pipes when the child exits
- interrupts then kills hung processes on shutdown paths
- reports restart count and last known error through health status
- applies environment allowlists plus required platform variables
- passes only minimal platform variables when `env_allowlist` is omitted
- injects `GOFLOW_WORKSPACE_ROOT` for workspace-aware servers
- can start MCP servers in a separate process group with `isolation: process_group`
- can opt into Windows Job Object lifecycle isolation with `isolation: windows_job` on Windows hosts
- can opt into Linux cgroup resource-control isolation with `isolation: linux_cgroup` on Linux hosts

`network_disabled` is surfaced in health/status output as a capability declaration. It is not an OS-level network sandbox; network-capable tools should still declare kind `network` so agent profiles can gate them.

`isolation: process_group` is the first conservative OS-level adapter. On supported platforms it starts the MCP child in a separate process group so lifecycle cleanup can target the server boundary more reliably. It does not provide network filtering, filesystem sandboxing, containerization, or privilege dropping.

`isolation: windows_job` is an opt-in Windows adapter. It places the MCP child process in a Windows Job Object with kill-on-close behavior, so timeout shutdown can terminate the job boundary instead of only the direct process. It is still lifecycle isolation, not a filesystem or network sandbox.

`isolation: linux_cgroup` is an opt-in Linux adapter. It creates or uses a child cgroup under `isolation_options.cgroup_parent`, writes configured `memory.max`, `pids.max`, and `cpu.max` values, and attaches the MCP process to that cgroup. It requires a writable cgroup v2 parent for the GoFlow user and is still resource control, not filesystem or network sandboxing.

For the isolation roadmap and adapter tradeoffs, see [MCP Isolation Strategy](./mcp-isolation.md).

## Config example

```yaml
mcp_servers:
  - name: file_tools
    command: go
    args:
      - run
      - ./mcp_servers/file_tools
    enabled: true
    timeout: 30s
    workdir: .
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, TMP, TEMP]
    isolation: process_group
    restart_limit: 3
    cooldown: 10s
    allowed_commands:
      - go
    max_request_bytes: 65536
    max_response_bytes: 2097152

  - name: python_notes
    command: python
    args:
      - ./mcp_servers/python_notes.py
    enabled: true
    timeout: 30s
    workdir: .
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, TMP, TEMP]
    isolation: process_group
    restart_limit: 3
    cooldown: 10s
    allowed_commands:
      - python
    max_request_bytes: 65536
    max_response_bytes: 2097152

  - name: web_tools
    command: go
    args:
      - run
      - ./mcp_servers/web_tools
    enabled: true
    timeout: 30s
    workdir: .
    env_allowlist: [PATH, HOME, USERPROFILE, LOCALAPPDATA, TMP, TEMP]
    isolation: process_group
    restart_limit: 3
    cooldown: 10s
    allowed_commands:
      - go
    max_request_bytes: 65536
    max_response_bytes: 2097152
```

## How to inspect MCP tools

Start the CLI and run:

```text
/tools
```

The CLI now renders each tool with structured operator-facing metadata, including:

- qualified tool name such as `file_tools/read_file`
- tool kind such as `read` or `write`
- description
- MCP server name
- current server health summary
- compact input schema summary when available

Current built-in output should include entries for:

- `file_tools/list_dir`
- `file_tools/list_tree`
- `file_tools/file_info`
- `file_tools/read_file`
- `file_tools/search_files`
- `file_tools/write_file`
- `file_tools/delete_file`
- `web_tools/web_search`
- `web_tools/fetch_url`
- `web_tools/fetch_page_assets`
- `python_notes/read_note`
- `python_notes/write_note`
- `python_notes/python_ast_summary`
- `python_notes/json_query`
- `python_notes/binary_file_info`
- `python_notes/binary_strings`
- `python_notes/hex_preview`

## Current limitations

- transport is stdio only
- `network_disabled` is advisory rather than a cross-platform kernel sandbox
- `isolation: process_group` is process-lifecycle isolation, not a security sandbox
- `isolation: windows_job` improves Windows process-tree cleanup, but does not restrict filesystem or network access
- `isolation: linux_cgroup` requires host cgroup setup and does not restrict filesystem or network access
- there is no concurrency control or circuit breaker layer yet

## Future direction

Planned next steps for MCP should build on the existing manager/client split:
- runtime transports beyond the current HTTP/SSE surface where they fit the same schema contracts
- stronger OS-level isolation options for third-party MCP servers, such as platform-specific job objects, seccomp, chroot, containers, or firewall rules where available
- more cross-language examples beyond Python

For adding new servers and tools, see [MCP Tool Authoring](./mcp-authoring.md).

