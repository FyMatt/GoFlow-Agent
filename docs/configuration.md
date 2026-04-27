# Configuration

## Runtime config

`configs/agent.yaml` is the main runtime configuration.

The current shipped example uses the multi-agent format directly, while the loader still supports legacy `llm`-only input for backward compatibility.

## Path model

The loader now distinguishes two path scopes:

- **runtime-relative paths**: resolved from the GoFlow runtime home (the agent project itself)
- **workspace-relative paths**: resolved from the active workspace root

This matters most when you launch GoFlow with:

```bash
go run ./cmd/goflow --workspace D:/target-dir
```

In that mode:
- config still comes from `runtime-home/configs/agent.yaml`
- `skill.directory` is resolved from runtime home
- MCP startup paths are resolved from runtime home
- session persistence is resolved under the external workspace

Select a deployment-specific config with `--config`:

```bash
go run ./cmd/goflow --config configs/agent.docker.yaml --workspace /workspace --http :8080
```

Relative config paths are resolved from runtime home. Absolute config paths are used directly.

## CLI presentation behavior

In interactive CLI mode (`go run ./cmd/goflow` without `--http`), GoFlow adds a presentation layer on top of the same runtime:

- prints a compact startup banner with brand text plus runtime/workspace summary
- shows the active agent, current mode, tool policy, allowed tool kinds, explicit `allowed_tools`, workflow summary, pending conversational handoff summary, pending approval count, and remembered per-workspace tool approvals in that startup summary
- sets the terminal title to `GoFlow Agent - <workspace>` when stdin/stdout look like interactive terminals
- uses an interactive approval selector with `Up` / `Down` and `Enter`
- still accepts `1`, `2`, and `3` as approval shortcuts
- falls back to the numeric prompt when stdin/stdout are redirected or otherwise non-interactive

HTTP server mode keeps the plain startup line and does not use the interactive CLI banner or approval menu.

## Example

```yaml
agent:
  name: GoFlow Agent
  max_iterations: 8
  timeout: 2m

default_agent: chat

providers:
  primary:
    provider: openai-compatible
    base_url: ${GOFLOW_BASE_URL}
    api_key: ${GOFLOW_API_KEY}
    model: ${GOFLOW_MODEL}
    fallback_provider: backup
    timeout: 60s
    temperature: 0.2
    max_tokens: 2048
    retry_count: 2
    retry_backoff: 2s

  backup:
    provider: openai-compatible
    base_url: ${GOFLOW_BACKUP_BASE_URL}
    api_key: ${GOFLOW_BACKUP_API_KEY}
    model: ${GOFLOW_BACKUP_MODEL}
    timeout: 60s
    temperature: 0.2
    max_tokens: 2048
    retry_count: 1
    retry_backoff: 1s
```

For Windows + DeepSeek quick verification, set the key in your shell before running `run-goflow.example.cmd`:

```bat
set GOFLOW_API_KEY=your-deepseek-key
run-goflow.example.cmd
```

You can use `.env.example` as a starting point for environment variables and `run-goflow.example.cmd` as a safe bootstrap example.

The bootstrap script fills in DeepSeek defaults without embedding credentials in the repo.

```yaml
agents:
  chat:
    name: Chat
    description: Handles ordinary user requests and routes into planner/fixer/auditor roles when needed.
    provider: primary
    mode: chat
    tool_policy: confirm
    allowed_tool_kinds: [read, network]
    max_iterations: 8
  planner:
    name: Planner
    description: Produces scoped implementation plans before changes are made.
    provider: primary
    mode: plan
    tool_policy: confirm
    allowed_tool_kinds: [read, network]
    max_iterations: 6
  fixer:
    name: Fixer
    description: Makes minimal code changes based on an approved plan.
    provider: primary
    mode: fix
    tool_policy: confirm
    allowed_tool_kinds: [read, write, exec, network]
    max_iterations: 8
  auditor:
    name: Auditor
    description: Reviews completed work for regressions, risks, and follow-ups.
    provider: primary
    mode: audit
    tool_policy: confirm
    allowed_tool_kinds: [read, exec, network]
    max_iterations: 6

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

skill:
  directory: ./skills
  match_threshold: 1
  hot_reload: false

audit:
  enabled: true
  redact_content: false
  show_trace_in_cli: false

session:
  max_history: 8

log:
  level: info
  format: text
```

## Key fields

### `providers`

A map of named provider configs. Each provider contains:

- `provider`
- `base_url`
- `api_key`
- `model`
- `fallback_provider`
- `timeout`
- `temperature`
- `max_tokens`
- `retry_count`
- `retry_backoff`

Environment variables are expanded for LLM values such as `base_url`, `api_key`, and `model`.

### `agents`

A map of named runtime agent profiles. Each agent can define:

- `name`
- `description`
- `system_prompt`
- `provider`
- `model`
- `temperature`
- `max_tokens`
- `max_iterations`
- `allowed_tool_kinds`
- `allowed_tools`
- `tool_policy`
- `mode`
- `skill` override

If a field is omitted, the loader fills in defaults from the global runtime config and provider config where appropriate.

`allowed_tool_kinds` gates broad capability classes such as `read`, `write`, `exec`, `network`, and `unknown`. `allowed_tools` is an optional narrower allowlist of exact MCP tool names; when present, a tool call must pass both the kind check and the exact-name check. This is the recommended way to allow one network or browser-capable tool without opening every tool in the same kind.

### `default_agent`

The agent selected for ordinary startup and post-workflow recovery. In the shipped config this is `chat`, which is intentionally not write-capable. Ordinary write-intent requests are routed to the `fixer` profile before write tools are used, then the runtime restores the previous chat state when the turn completes. If omitted, the loader picks the first agent name in sorted order.

### `agent`

Global defaults for the runtime:

- `name`
- `max_iterations`
- `timeout`

### `mcp_servers`

Each enabled server defines a stdio MCP process.

Useful fields:
- `name`
- `command`
- `args`
- `enabled`
- `timeout`
- `workdir`
- `env_allowlist`
- `isolation`
- `network_disabled`
- `restart_limit`
- `cooldown`
- `isolation_options`
- `allowed_commands`
- `allowed_command_paths`
- `max_request_bytes`
- `max_response_bytes`

Enabled MCP servers must include either `allowed_commands` or `allowed_command_paths`. This keeps accidental command drift visible during startup.

`env_allowlist` is opt-in. When it is omitted, MCP child processes receive only minimal platform variables and `GOFLOW_WORKSPACE_ROOT`; they do not inherit the full GoFlow process environment. Add only the variables the tool really needs, such as `PATH`, `HOME`, proxy variables, or language-runtime cache paths.

`isolation` is optional. Supported values are:

- `none` or omitted: start the MCP process normally
- `process_group`: start the MCP process in a separate OS process group where the host platform supports it
- `windows_job`: on Windows, start the MCP process in a Windows Job Object with kill-on-close lifecycle behavior
- `linux_cgroup`: on Linux, attach the MCP process to a cgroup v2 directory and apply optional resource limits

`process_group` is useful for cleaner process lifecycle management and future isolation adapters. It is not a network, filesystem, container, or job-object sandbox by itself. `windows_job` improves Windows process-tree cleanup, but it is still not a network or filesystem sandbox. `linux_cgroup` controls configured Linux resources; it does not restrict filesystem access, network access, or privileges.

`linux_cgroup` accepts these `isolation_options` keys:

- `cgroup_parent`: absolute cgroup parent path, default `/sys/fs/cgroup/goflow`
- `cgroup_name`: safe leaf name for this server, default `mcp-<pid>`
- `memory_max`: value written to `memory.max`, for example `256M`
- `pids_max`: value written to `pids.max`, for example `"64"`
- `cpu_max`: value written to `cpu.max`, for example `"50000 100000"`

Example:

```yaml
mcp_servers:
  - name: file_tools
    command: /app/bin/file_tools
    enabled: true
    allowed_commands: [/app/bin/file_tools]
    isolation: linux_cgroup
    isolation_options:
      cgroup_parent: /sys/fs/cgroup/goflow
      cgroup_name: file-tools
      memory_max: 256M
      pids_max: "64"
      cpu_max: "50000 100000"
```

`network_disabled` is an advisory capability declaration. It does not currently install an OS-level network filter.

Tools returned by `tools/list` are validated before they are exposed to agents. Each tool must declare a safe name, a valid `kind`, and a strict object `input_schema` with `additionalProperties: false`.

#### Built-in examples

- `file_tools`: Go implementation for workspace file access
- `web_tools`: Go implementation for web search, URL fetch, and page-plus-asset fetches
- `python_notes`: Python implementation for notes, Python AST summaries, JSON selection, and binary triage helpers

### `skill`

Controls skill discovery and matching.

- `directory`
- `match_threshold`
- `hot_reload`

`skill.directory` is resolved from runtime home.

### `session`

Controls session behavior.

- `max_history`
- `persist_path` (optional)

If `persist_path` is omitted, GoFlow defaults to:

```text
<workspace>/.goflow/session.json
```

The persisted session snapshot now includes operator-facing workflow state as well:
- current workflow name/status/next stage
- workflow request and latest summary
- workflow last approval prompt/result
- pending approval summaries with tool name, agent, and argument preview
- pending ordinary-chat handoff state, including the original request, target agent/mode, expected next action, and approved-plan summary when the runtime is waiting for a natural-language confirmation such as `可以` or `go ahead`
- last ordinary-chat routing outcome, including the source agent, target agent, target mode, request summary, and routing reason so role switches remain inspectable across restart

### `audit`

Controls audit collection and CLI trace visibility.

- `enabled`
- `redact_content`
- `show_trace_in_cli`

## CLI usage and config interaction

### Start in current directory

```bash
go run ./cmd/goflow
```

### Start against an external workspace

```bash
go run ./cmd/goflow --workspace D:/target-dir
```

The config file still comes from the runtime project, but file operations and session persistence target `D:/target-dir`.

## HTTP API

The same runtime can also be exposed over HTTP.

Start the server with:

```bash
go run ./cmd/goflow --http :8080
```

You can also combine it with an external workspace:

```bash
go run ./cmd/goflow --workspace D:/target-dir --http :8080
```

Current endpoints:
- `POST /api/run`
- `POST /api/run/stream`
- `GET /api/session`
- `POST /api/workflows/{name}`
- `POST /api/workflows/{name}/stream`
- `GET /api/workflow-graphs`
- `POST /api/workflow-graphs`
- `GET /api/workflow-graphs/{name}`
- `PUT /api/workflow-graphs/{name}`
- `DELETE /api/workflow-graphs/{name}`
- `GET /api/workflow-options`
- `POST /api/approvals/{callID}/approve`
- `POST /api/approvals/{callID}/approve/stream`
- `POST /api/approvals/{callID}/deny`
- `POST /api/approvals/{callID}/deny/stream`
- `POST /api/approvals/approve-all`

`POST /api/run` returns the final JSON agent result.

`POST /api/run/stream` returns `text/event-stream` and forwards runtime `schema.StreamEvent` values as SSE frames.

`GET /api/session` returns the same persisted workflow, pending handoff, and pending approval snapshot surfaced by the CLI.

`GET /workflows` serves the built-in workflow graph editor. Custom workflow
graphs saved through the editor are persisted under
`workflows/<name>/workflow.yaml` in runtime home.

## Backward compatibility

The loader still accepts the older top-level `llm` shape. When used, it is normalized into:

- one provider
- one default agent
- the current runtime defaults

That compatibility exists to keep existing configs loading, not as the recommended format for new deployments.

