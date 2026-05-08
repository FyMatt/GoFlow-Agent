# Configuration

[English](./configuration.md) | [简体中文](./configuration.zh-CN.md)

## Runtime config

`configs/goflow.yaml` is the main runtime configuration.

The current shipped example keeps global runtime settings in `configs/goflow.yaml`
and stores default agents, providers, and MCP servers as modular files under
`configs/agents/`, `configs/providers/`, and `configs/mcp_servers/`. The loader
still supports legacy `llm`-only input for backward compatibility.

## Path model

The loader now distinguishes two path scopes:

- **runtime-relative paths**: resolved from the GoFlow runtime home (the agent project itself)
- **workspace-relative paths**: resolved from the active workspace root

This matters most when you launch GoFlow with:

```bash
go run ./cmd/goflow --workspace D:/target-dir
```

In that mode:
- config still comes from `runtime-home/configs/goflow.yaml`
- `skill.directory` is resolved from runtime home
- MCP startup paths are resolved from runtime home
- session persistence is resolved under the external workspace

Select a deployment-specific config with `--config`:

```bash
go run ./cmd/goflow --config configs/goflow.docker.yaml --workspace /workspace --http :8080
```

Relative config paths are resolved from runtime home. Absolute config paths are used directly.

## CLI presentation behavior

In interactive CLI mode (`go run ./cmd/goflow` without `--http`), GoFlow adds a presentation layer on top of the same runtime:

- prints a compact startup banner with brand text plus runtime/workspace summary
- treats the current directory as an unconfirmed default workspace when `--workspace` is omitted
- allows pure chat before workspace confirmation, but hides workspace-scoped read/write/exec tools and stops `@file`, workflow, file-generation, and command-execution requests until `/workspace confirm`
- shows the active agent, current mode, tool policy, allowed tool kinds, explicit `allowed_tools`, workflow summary, pending conversational handoff summary, pending approval count, and remembered per-workspace tool approvals in that startup summary
- sets the terminal title to `GoFlow Agent - <workspace>` when stdin/stdout look like interactive terminals
- uses an interactive approval selector with `Up` / `Down` and `Enter`
- still accepts `1`, `2`, and `3` as approval shortcuts
- falls back to the numeric prompt when stdin/stdout are redirected or otherwise non-interactive

Workspace commands:

```text
/workspace status
/workspace confirm
/workspace clear
/workspace use <path>
```

CLI workspace rebinding is intentionally conservative: `use <path>` confirms
the current runtime workspace when the path matches, and otherwise dynamically
rebuilds the runtime only while execution is idle. The old workspace-scoped
session is saved, old MCP child processes are closed, and a fresh runtime,
MCP catalog, session scope, and tool policy are loaded for the selected path.
If a tool approval, handoff, paused workflow, ordinary Agent run, or workflow
run is still active, the command blocks the switch and prints a restart
fallback. HTTP mode exposes the same dynamic workspace rebinding when the server
is started with the runtime rebinder hook.

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

Provider definitions can live in the main config file or in separate module files under `configs/providers/*.yaml`. Module files are merged at startup after the main file is parsed, and a module with the same provider name overrides the main-file entry. Both direct and wrapped forms are accepted:

```yaml
deepseek:
  provider: openai-compatible
  base_url: https://api.deepseek.com/v1
  api_key: ${DEEPSEEK_API_KEY}
  model: deepseek-chat
```

```yaml
providers:
  deepseek:
    provider: openai-compatible
    base_url: https://api.deepseek.com/v1
    api_key: ${DEEPSEEK_API_KEY}
    model: deepseek-chat
```

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

GoFlow also uses these fields to reduce LLM input cost. Before each model call,
the runtime filters the tool schemas sent to the provider so the model only sees
tools allowed by the active agent and the matched skill. Qualified names such as
`file_tools/read_file` are accepted in `allowed_tools` and skill `tools`; the
executor still enforces the same policy if a model guesses a hidden tool name.
Streaming clients receive a `prompt_budget` event before the provider call with
estimated system, message, tool-schema, skill, exposed/filtered tool counts, and
total prompt tokens so operators can see which context bucket is driving cost.
The event also includes stable hashes for the system prompt, visible tool
schemas, matched skill context, and combined prompt prefix plus
`cacheable_prefix_tokens`; these are diagnostics for provider-side prompt/KV
cache behavior and do not store provider cache tensors inside GoFlow.
When a tool returns a large result, GoFlow keeps the full content in runtime
events/replay/result objects but compacts the tool message before replaying it
into the next LLM request. The compact prompt observation keeps head/tail context
and original-size metadata so the model can continue without repeatedly paying
for full file reads, web assets, command output, or large diffs.
Large compacted observations are also stored as bounded session artifacts with
refs like `goflow://session-artifacts/<id>`. A later CLI or HTTP request can
include that ref to rehydrate the artifact content into the next prompt only
when exact omitted content is needed. HTTP clients can list artifacts through
`GET /api/session-artifacts` and fetch one through
`GET /api/session-artifacts/{id}`.
Session prompt/tool history is also compacted during system prompt construction:
only a bounded recent slice is retained, oversized items are truncated with
compact markers, and the original session snapshot remains available for
inspection.
The session snapshot stores `prompt_budget` and bounded
`prompt_budget_history`. Provider-reported usage is also stored in bounded
`token_usage_history` when the provider returns token accounting. `GET
/api/runtime` returns a `cost` block with the latest budget, prompt-budget and
token-usage history, averages, max estimated prompt size, cacheable prefix
average, non-cacheable context average, unique prefix count, prompt-prefix reuse
samples/rate, latest prefix hash, provider-reported prompt/output/cached/total
token totals, provider cache-hit rate, per-agent/per-mode/per-stage trend rows,
and compact recommendations for likely avoidable context such as large message
history, broad tool schemas, prompt prefix churn, or reusable prefixes that are
not producing cached-token reports. `GET /api/runtime/cost` returns the same
cost diagnostics without the full runtime/session inventory so Studio can poll
cost panels cheaply. Both endpoints also expose `cost.features`, a stable
machine-readable feature catalog for cost-control UI. Current feature rows cover
prompt budget telemetry, tool-schema minimization, session-history compaction,
session artifact refs, provider prompt-cache signals, and optional auxiliary
router/summarizer model routes. UI clients should use `features[].state`,
`enabled`, `observed`, `requires_config`, `extra_model_call`, and measurement
fields such as `samples`, `saved_tokens`, and `filtered_tools` for status
badges instead of parsing recommendation text. Provider cached-token values are
provider-reported prompt-cache telemetry; GoFlow does not store provider
KV-cache tensors locally.

`GET /api/runtime` and `GET /api/runtime/cost` also expose
`cost.tuning[]` for measurement-led low-cost route tuning. GoFlow does not
blindly enable cheaper helper models; it first reports whether a route has
enough representative samples to justify an A/B run. Current route rows cover
`router` and `summarizer` and include:

- `state`: `candidate`, `configured_waiting_for_samples`, `observed_saving`,
  `observed_costly`, or `not_enough_data`.
- `observed_samples`, `prompt_budget_samples`, and `token_usage_samples`.
- provider-reported prompt/output/cached/total token totals for observed helper
  calls.
- `candidate_average_prompt_tokens` and `estimated_extra_call_tokens`, so an
  operator can see whether an extra helper call is likely to pay for itself.
- `net_savings_signal`, `quality_checklist`, `recommendation`, and `action`.

Treat `state: candidate` as a prompt to run a controlled test with a cheaper
provider/model, not as an automatic recommendation to enable the route for all
traffic. Treat `observed_saving` as a positive token signal that still needs
quality review, and `observed_costly` as a reason to disable or narrow that
route unless quality justifies the extra model call.

Verifier passes can also be routed to a cheaper model than the main agent. The
`agent` field still selects the verifier role and prompt behavior, while
optional `provider` and `model` override only the LLM route used for the
tool-free verification call:

```yaml
verifier:
  enabled: true
  agent: auditor
  provider: cheap
  model: cheap-checker
  modes: [fix, audit]
  max_tokens: 512
```

If `provider` is set and `model` is omitted, GoFlow uses that provider's
configured model. `GET /api/runtime` exposes the effective verifier route under
`verifier`, and verifier calls emit their own `prompt_budget` and `token_usage`
events when available.

Auxiliary cost-control routes live under `cost_control`. These routes are
disabled by default because each enabled helper may add an extra model call, but
they let deployments move low-value helper work to a cheaper model:

```yaml
cost_control:
  router:
    enabled: true
    provider: cheap
    model: cheap-router
    max_tokens: 96
    temperature: 0
  summarizer:
    enabled: true
    provider: cheap
    model: cheap-summary
    max_tokens: 512
    temperature: 0
```

`router` is only used when the local keyword router does not recognize an
ordinary request; it classifies the request as `chat`, `plan`, `fix`, or
`audit` without tools. `summarizer` is used only when a tool loop reaches its
iteration budget and GoFlow asks for a final no-tool summary. If `provider` is
set and `model` is omitted, the provider's configured model is used. `GET
/api/runtime` exposes these effective helper routes under `auxiliary_models`,
and `cost.tuning[]` shows whether the measured workload currently supports
turning each route on, keeping it enabled, or disabling it.

Agent definitions can live in the main config file or in separate module files under `configs/agents/*.yaml`. Module files are merged at startup after the main file is parsed, and a module with the same agent name overrides the main-file entry. Both direct and wrapped forms are accepted:

```yaml
researcher:
  name: Researcher
  provider: primary
  mode: audit
  tool_policy: confirm
  allowed_tool_kinds: [read, network]
  allowed_tools: [web_tools/fetch_url, web_tools/web_search]
```

```yaml
agents:
  researcher:
    name: Researcher
    provider: primary
    mode: audit
    tool_policy: confirm
    allowed_tool_kinds: [read, network]
```

### `default_agent`

The agent selected for ordinary startup and post-workflow recovery. In the shipped config this is `chat`, which is intentionally not write-capable. Ordinary write-intent requests are routed to the `fixer` profile before write tools are used, then the runtime restores the previous chat state when the turn completes. If omitted, the loader picks the first agent name in sorted order.

### `agent`

Global defaults for the runtime:

- `name`
- `max_iterations`
- `timeout`

### `mcp_servers`

Each enabled server defines a stdio MCP process.

MCP server definitions can live in the main config file or in separate module
files under `configs/mcp_servers/*.yaml`. Module files are loaded first as the
base tool catalog, then any same-named `mcp_servers` entries in the selected
main config override them. This lets the default development config stay
modular while Docker and release-archive configs can override commands for the
same built-in servers.

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
- `windows_restricted_token`: on Windows, start the MCP process with a restricted primary token and low integrity for privilege reduction, then attach it to a kill-on-close Job Object for process-tree cleanup
- `linux_cgroup`: on Linux, attach the MCP process to a cgroup v2 directory and apply optional resource limits
- `linux_netns`: on Linux, start the MCP process through `unshare --net` for network namespace isolation
- `container`: start the MCP server through `docker run` or `podman run` with an explicit image, workspace mount mode, environment allowlist, network mode, and optional resource limits

`appcontainer`, `app_container`, `windows_appcontainer`, and
`windows_app_container` are rejected with a specific unsupported-mode diagnostic
rather than treated as generic typos. GoFlow's supported strong sandbox path is
Docker/Podman `isolation: container`; AppContainer-like values are intentionally
invalid so users do not mistake them for an available Windows sandbox.

For new MCP tools, prefer `isolation: container` when Docker/Podman is
available. It is the recommended cross-platform strong boundary for generated,
third-party, write-capable, exec-capable, network-capable, or otherwise
untrusted tools. `process_group` is useful for cleaner process lifecycle
management. It is not a network, filesystem, container, or job-object sandbox
by itself. `windows_job` improves Windows
process-tree cleanup, but it is still not a network, filesystem,
restricted-token, or privilege sandbox. `windows_restricted_token`
lowers process privileges and integrity level and also uses Job Object cleanup,
but it still does not provide a filesystem or network sandbox. `linux_cgroup`
controls configured Linux resources; it does not restrict filesystem access,
network access, or privileges. `linux_netns` restricts network egress through a
Linux network namespace, but it does not restrict filesystem access or
privileges. `container` is the broad real sandbox boundary recognized by
GoFlow's risky-tool policy, but the boundary still depends on the selected
image, workspace mount mode, network mode, resource limits, and container
runtime policy.

`GET /api/runtime` exposes the effective isolation status for every configured
MCP server under `mcp_servers`. Use this response, not just the raw YAML field,
to show operators whether a server is actually sandboxed, merely lifecycle or
resource limited, or still able to access host resources outside the workspace.
`GET /api/runtime`, `GET /api/help`, and `GET /api/capabilities` also expose
`mcp_isolation_modes`, a platform-aware capability catalog for settings UIs.
Each mode reports whether it is accepted by config on the current host, whether
the adapter is implemented, which sandbox features it enforces, which features
are still missing, required options such as `image`, and a disabled reason for
platform-specific modes. Mode entries also mark the Docker/Podman
`container` mode with `recommended: true` and `recommended_for`, while native
modes carry `fallback: true` plus `fallback_kind` metadata such as lifecycle
cleanup, resource control, network-only, or Windows privilege reduction.
Studio should use `/api/capabilities` for lightweight MCP isolation dropdown
discovery. The same mode entries include an `options` array with field names,
types, required flags, defaults, allowed values, descriptions, and
recommendations for `container`, `linux_cgroup`, and `linux_netns`, so visual
editors can render isolation forms without hard-coding backend option schemas.
For Windows MCP servers, the response also includes `windows_isolation` and
`missing_sandbox_features` when `windows_job` or
`windows_restricted_token` is configured so Studio can show whether the
adapter is lifecycle-only or privilege-reduction plus lifecycle cleanup.

`linux_cgroup` accepts these `isolation_options` keys:

- `cgroup_parent`: absolute cgroup parent path, default `/sys/fs/cgroup/goflow`
- `cgroup_name`: safe leaf name for this server, default `mcp-<pid>`
- `memory_max`: `memory.max` value; accepts `max`, a positive byte count, or `K`/`M`/`G`/`T` suffixes such as `256M`, which GoFlow normalizes to bytes before writing
- `pids_max`: `pids.max` value; accepts `max` or a positive integer such as `"64"`
- `cpu_max`: `cpu.max` value; accepts `max` or `"<quota> <period>"` such as `"50000 100000"`

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

`linux_netns` accepts these `isolation_options` keys:

- `unshare_command`: optional command path, default `unshare`; either `unshare`
  or an absolute path is accepted
- `map_root_user`: optional boolean passed as `--map-root-user`

Example:

```yaml
mcp_servers:
  - name: network_helper
    command: python
    args: ["./mcp_servers/network_helper.py"]
    enabled: true
    allowed_commands: [python]
    network_disabled: true
    isolation: linux_netns
    isolation_options:
      unshare_command: /usr/bin/unshare
```

With `isolation: linux_netns`, GoFlow executes the MCP command as
`unshare --net -- <command> ...`. If the host cannot create the namespace,
startup fails instead of silently running with host network access.

`container` supports top-level `isolation_profile` plus explicit
`isolation_options`. Profiles are the recommended starting point:

| Profile | Workspace | Network | Notes |
| --- | --- | --- | --- |
| `readonly` | `ro` | disabled | default for generated readers and untrusted read-only helpers |
| `writer` | `rw` | disabled | for tools that must create or edit workspace files |
| `network` | `ro` | bridge | for fetchers and web research helpers that need outbound network access |
| `production` | `ro` | disabled | uses `pull_policy: never`; pair with digest-pinned images |

Profile defaults include resource limits, `memory_swap`, `readonly_rootfs`,
`no_new_privileges`, `cap_drop: all`, `ipc: none`, `userns: auto`, tmpfs
scratch mounts, `init: true`, and `tool_mount: ro`. `readonly`, `network`, and
`production` also default to `user: 65532:65532`. Explicit
`isolation_options` override profile defaults field by field. New configs
should use top-level `isolation_profile`; `isolation_options.profile` is kept
as a legacy alias.

`container` accepts these `isolation_options` keys:

- `image`: required container image name
- `runtime`: optional runtime command, default `docker`; `podman` and absolute runtime paths are accepted
- `workspace_mount`: `rw`, `ro`, or `none`; default `rw`
- `workspace_target`: container path for `GOFLOW_WORKSPACE_ROOT`, default `/workspace`
- `container_workdir`: optional `--workdir` path inside the container
- `network`: `disabled`/`none`, `default`, `bridge`, `host`, or a safe custom network name
- `ipc`: `private` or `none`; default `private`. Use `none` for ordinary MCP
  tools that do not need shared memory or host IPC.
- `userns`: `private`, `auto`, `nomap`, or `keep-id`; default `private`. Use
  `auto` or `nomap` on compatible Docker/Podman runtimes to reduce host UID/GID
  exposure.
- `memory`, `cpus`, `pids_limit`: passed to the container runtime as resource
  limits. `memory` must be a positive integer with an optional `b`, `k`, `m`,
  `g`, or `t` suffix, such as `256m`; `cpus` must be a positive number; and
  `pids_limit` must be a positive integer.
- `readonly_rootfs`, `no_new_privileges`, `init`: boolean hardening/runtime flags
- `user`, `cap_drop`, `security_opt`: optional runtime privilege controls.
  `user` must be a single `user` / `UID` / `user:group` / `UID:GID` token
  using letters, numbers, `_`, `.`, or `-`; `cap_drop` accepts `all` or
  capability names such as `NET_ADMIN`; `security_opt` accepts comma,
  semicolon, or newline-separated values
- `tmpfs`: optional semicolon or newline-separated `--tmpfs` mounts such as
  `/tmp:rw,noexec,nosuid,size=64m`; commas are preserved inside each tmpfs
  option
- `tool_source`, `tool_target`, `tool_mount`: optional single-tool bind mount. Set
  `tool_source` to an absolute host file path, `tool_target` to the container
  path, and `tool_mount` to `ro`, `rw`, or `none`. Studio scaffold presets use
  this to mount only the generated Python tool file into the container while
  keeping the workspace mount separately controlled.

Example:

```yaml
mcp_servers:
  - name: file_tools_container
    command: /app/bin/file_tools
    args: ["--stdio"]
    enabled: true
    allowed_commands: [/app/bin/file_tools]
    network_disabled: true
    isolation: container
    isolation_profile: readonly
    isolation_options:
      image: ghcr.io/fymatt/goflow-agent-mcp-tools:latest
      runtime: docker
      workspace_target: /workspace
      container_workdir: /app
      security_opt: seccomp=/etc/goflow/seccomp.json;apparmor=goflow-mcp
```

With `isolation: container`, `network_disabled: true` is enforced by default as `--network none` unless `isolation_options.network` explicitly sets another compatible disabled value. With `isolation: linux_netns`, network isolation is enforced by the namespace adapter. Without `container` or `linux_netns`, `network_disabled` remains an advisory capability declaration and does not install an OS-level network filter.

`GET /api/config/diagnostics` returns field-targeted MCP setup warnings for
Studio and API consumers. For container MCP servers it reports weak defaults
such as open network egress, read-write workspace mounts, missing resource
limits, and omitted privilege hardening flags (`no_new_privileges`,
`cap_drop`, `readonly_rootfs`, and `user`). It also reports missing `tmpfs`
scratch mounts when `readonly_rootfs` is enabled, missing `init`, floating
container image tags, container images that are not digest-pinned for
production, `network: host`, shell-like `allowed_commands`, missing `ipc:
none`, missing `userns`, incomplete `tool_source` / `tool_target` pairs, and
read-write `tool_mount`
settings. The
response includes `target_kind`, `target_name`, `field`, `severity`, and
`recommendation`, so a UI can attach the warning to the exact MCP server field
without parsing message text.

The same diagnostics endpoint also reports provider and agent quality issues
that are valid YAML but poor runtime defaults, such as provider fallback cycles,
the default chat agent carrying broad write/exec permissions, agents allowing
write/exec/network/unknown tools with `tool_policy: allow`, and exact
`allowed_tools` entries whose MCP server is not currently configured or
discovered.

When the saved config cannot load at all, diagnostics still returns HTTP 200
with `status: "error"`, a generic `config_load_failed` item, and field-targeted
items for common failures such as missing `default_agent`, provider required
fields, agent/provider references, verifier and `cost_control` provider
references, MCP names/commands/allowlists, duplicate MCP names, isolation modes,
and invalid container isolation fields. This lets Studio keep the settings page
usable and highlight the exact field that prevents startup.

Tools returned by `tools/list` are validated before they are exposed to agents. Each tool must declare a safe name, a valid `kind`, and a strict object `input_schema` with `additionalProperties: false`.

### `tool_risk_policy`

`tool_risk_policy` is optional and disabled by default for compatibility. It
adds enforcement based on the effective MCP server boundary, in addition to
agent `tool_policy`, `allowed_tool_kinds`, and `allowed_tools`.

```yaml
tool_risk_policy:
  require_approval_for_unsandboxed_risky_tools: true
  disable_remember_for_unsandboxed_risky_tools: true
  reject_unsandboxed_risky_tools: false
```

- `require_approval_for_unsandboxed_risky_tools`: when true, write, exec,
  network, and unknown-kind tools require approval if their MCP server is not
  running through an enforced sandbox for that capability. `isolation:
  container` covers risky tools broadly; `isolation: linux_netns` only satisfies
  this check for network-kind tools because it enforces network isolation only.
  The check still applies even when an agent profile uses `tool_policy: allow`.
- `disable_remember_for_unsandboxed_risky_tools`: when true, approve-and-remember
  and workflow approve-tools auto approval are rejected for the same
  unsandboxed risky tools. Operators can still approve the current call once.
  HTTP `/actions` discovery also marks those remember-style actions unavailable
  with a `tool_risk_policy` reason, so Studio can disable the button before a
  user submits an action the backend would reject.
- `reject_unsandboxed_risky_tools`: when true, write, exec, network, and
  unknown-kind tools without an enforced sandbox for that capability are
  rejected before execution. This is the strict mode for deployments that
  should not allow a human approval to bypass missing sandboxing. Deny/cancel
  actions remain available for already-paused runs, but approve actions are
  marked unavailable with a policy reason.

This policy treats `container` as the broad real sandbox boundary. It also
recognizes capability-specific enforced boundaries where they match the tool
kind, currently `linux_netns` for network tools. `process_group`, `windows_job`,
and `linux_cgroup` remain useful lifecycle or resource controls, but they do
not suppress these risk-policy checks.

`GET /api/runtime` exposes the active policy under `tool_risk_policy`, including
whether any switch is enabled, which tool kinds it applies to, and plain-language
approval, remember, and rejection behavior. It also includes
`recognized_sandbox_boundaries`, a structured list of isolation modes that
satisfy the policy and the tool kinds they cover. Frontend and API clients
should use this runtime field to explain global policy state without parsing
natural-language text, while per-run `/actions` responses remain authoritative
for whether a specific approval button is available.

`GET /api/config/diagnostics` also emits field-targeted
`tool_risk_policy_*` items so settings UIs can explain the saved policy before
restart. The diagnostics distinguish disabled policy, forced approval,
disabled remember scopes, strict rejection, and the risky middle state where
approval is required but approve-and-remember is still allowed.
The same response annotates enabled MCP servers with
`tool_risk_policy_mcp_broad_sandbox`,
`tool_risk_policy_mcp_network_sandbox`, or
`tool_risk_policy_mcp_unsandboxed` so settings UIs can show whether each server
is recognized by the policy as broadly sandboxed, network-only sandboxed, or
still treated as unsandboxed for risky tools.

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

Stop HTTP mode with `Ctrl+C`. The CLI catches the signal, calls HTTP server
shutdown, and saves the session snapshot before exiting.

Current endpoints:
- `GET /console`
- `GET /workflows`
- `GET /api/runtime`
- `GET /api/runtime/cost`
- `POST /api/runtime/agent`
- `GET /api/capabilities`
- `GET /api/help`
- `GET /api/config/diagnostics`
- `GET /api/update-policy`
- `GET /api/workspace`
- `POST /api/workspace/confirm`
- `POST /api/workspace/clear`
- `POST /api/workspace/select`
- `POST /api/workspace/requirement`
- `GET /api/workspace-files`
- `POST /api/run`
- `POST /api/run/stream`
- `GET /api/runs`
- `GET /api/agent-runs`
- `GET /api/agent-runs/{id}`
- `GET /api/agent-runs/{id}/events`
- `GET /api/agent-runs/{id}/events/stream`
- `GET /api/agent-runs/{id}/timeline`
- `GET /api/agent-runs/{id}/replay`
- `GET /api/agent-runs/{id}/diffs`
- `GET /api/agent-runs/{id}/export`
- `GET /api/agent-runs/{id}/actions`
- `POST /api/agent-runs/{id}/cancel`
- `POST /api/agent-runs/{id}/retry`
- `POST /api/agent-runs/{id}/approve_tool`
- `POST /api/agent-runs/{id}/approve_remember_tool`
- `POST /api/agent-runs/{id}/deny_tool`
- `POST /api/agent-runs/{id}/approve_all_tools`
- `POST /api/agent-runs/{id}/approve_remember_all_tools`
- `POST /api/agent-runs/{id}/deny_all_tools`
- `GET /api/session`
- `GET /api/session-artifacts`
- `GET /api/session-artifacts/{id}`
- `POST /api/workflows/{name}`
- `POST /api/workflows/{name}/stream`
- `GET /api/workflow-runs`
- `GET /api/workflow-runs/{id}`
- `GET /api/workflow-runs/{id}/events`
- `GET /api/workflow-runs/{id}/events/stream`
- `GET /api/workflow-runs/{id}/artifacts`
- `GET /api/workflow-runs/{id}/stages`
- `GET /api/workflow-runs/{id}/stages/{stage}`
- `GET /api/workflow-runs/{id}/timeline`
- `GET /api/workflow-runs/{id}/replay`
- `GET /api/workflow-runs/{id}/evidence`
- `GET /api/workflow-runs/{id}/navigation`
- `GET /api/workflow-runs/{id}/diffs`
- `GET /api/workflow-runs/{id}/export`
- `GET /api/workflow-runs/{id}/actions`
- `POST /api/workflow-runs/{id}/cancel`
- `POST /api/workflow-runs/{id}/retry`
- `POST /api/workflow-runs/{id}/input`
- `POST /api/workflow-runs/{id}/approve`
- `POST /api/workflow-runs/{id}/approve-tool`
- `POST /api/workflow-runs/{id}/approve-tools`
- `POST /api/workflow-runs/{id}/resume-sub-workflow`
- `GET /api/resources/skills`
- `GET /api/resources/skills/{name}`
- `PUT /api/resources/skills/{name}`
- `GET /api/resources/providers`
- `GET /api/resources/providers/{id}`
- `POST /api/resources/providers/validate`
- `POST /api/resources/providers/{id}/validate`
- `PUT /api/resources/providers/{id}`
- `DELETE /api/resources/providers/{id}`
- `GET /api/resources/agents`
- `GET /api/resources/agents/{id}`
- `POST /api/resources/agents/validate`
- `POST /api/resources/agents/{id}/validate`
- `PUT /api/resources/agents/{id}`
- `DELETE /api/resources/agents/{id}`
- `GET /api/resources/tools`
- `GET /api/resources/tools/{name}`
- `POST /api/resources/tools/validate`
- `POST /api/resources/tools/{name}/validate`
- `PUT /api/resources/tools/{name}`
- `DELETE /api/resources/tools/{name}`
- `GET /api/resources/tools/scaffolds`
- `GET /api/resources/tools/scaffolds/{preset}`
- `POST /api/resources/tools/scaffolds/{preset}`
- `GET /api/kits`
- `GET /api/kits/{name}`
- `GET /api/resources/kits`
- `GET /api/resources/kits/{name}`
- `PUT /api/resources/kits/{name}`
- `DELETE /api/resources/kits/{name}`
- `GET /api/resources/kits/{name}/validate`
- `GET /api/resources/kits/scaffolds`
- `GET /api/resources/kits/scaffolds/{preset}`
- `POST /api/resources/kits/scaffolds/{preset}`
- `GET /api/resources/team-templates/scaffolds`
- `GET /api/resources/team-templates/scaffolds/{preset}`
- `POST /api/resources/team-templates/scaffolds/{preset}`
- `POST /api/resources/team-templates/validate`
- `POST /api/resources/team-templates/{name}/validate`
- `POST /api/resources/policy-rules/validate`
- `POST /api/resources/policy-rules/{name}/validate`
- `GET /api/resources/kits/{name}/export`
- `POST /api/resources/kits/import`
- `GET /api/workflow-graphs`
- `POST /api/workflow-graphs`
- `GET /api/workflow-graphs/{name}`
- `PUT /api/workflow-graphs/{name}`
- `DELETE /api/workflow-graphs/{name}`
- `GET /api/workflow-options`
- `POST /api/approvals/{callID}/approve`
- `POST /api/approvals/{callID}/approve/stream`
- `POST /api/approvals/{callID}/approve-remember`
- `POST /api/approvals/{callID}/approve-remember/stream`
- `POST /api/approvals/{callID}/deny`
- `POST /api/approvals/{callID}/deny/stream`
- `POST /api/approvals/approve-all`

`GET /api/help` returns a machine-readable CLI/Web parity document. Existing
clients can keep using `commands` and `capabilities`; Studio can also render
the structured `parity` matrix, `coverage` counts, `client_contracts`, and
`resource_capabilities` list to explain which CLI operations have HTTP
equivalents, whether they are durable, whether they touch the workspace, and
whether a restart is required after a browser-side edit.
`GET /api/capabilities` returns the lightweight subset intended for startup
feature detection: `capabilities`, `client_contracts`, and
`resource_capabilities` only. Use `/api/help` for full onboarding/help pages,
and `/api/capabilities` when a client only needs to discover supported
contracts and editor affordances.
The `container_tool_scaffold_hardened_defaults` capability indicates that
containerized Python MCP tool scaffolds generate resource limits, read-only
rootfs, no-new-privileges, cap-drop-all, tmpfs scratch mounts, `ipc: none`, and
`userns: auto` by default. The
`container_tool_scaffold_versioned_image_default` capability indicates that
release builds use the matching MCP Python image tag by default instead of
always using `latest`. The `tool_scaffold_default_option_metadata` capability
indicates that scaffold list entries include default option metadata directly.
The `mcp_isolation_mode_recommendations` capability indicates that
`mcp_isolation_modes` entries include `recommended`, `recommended_for`,
`fallback`, and `fallback_kind` fields for isolation selector ordering and
operator copy.
For production container MCP servers, a version tag is only the minimum
baseline. Use immutable digest references such as `repo/tool@sha256:...` after
verifying or pre-pulling the trusted image. Diagnostics report
`mcp_container_image_floating_tag` for `latest` or untagged images and
`mcp_container_image_not_digest_pinned` for version-tagged images that are still
mutable.
Both endpoints include a `meta` object with `schema:
"goflow.api.discovery"`, `schema_version`, `min_supported_schema_version`,
`runtime_version`, and a stable `cache_key`. Browser clients should use this
metadata for compatibility checks and cache invalidation instead of inferring
support from the runtime version alone.
Each `client_contracts` item describes one browser integration area such as
ordinary Agent runs, workflow runs, approvals, workspace selection,
configuration apply state, or resource editing. The contract lists primary,
stream, and action endpoints plus required client behavior. Browser clients
should consume these backend-published contracts instead of duplicating CLI
behavior in frontend-only code.
Each `resource_capabilities` item describes one file-backed resource family
with stable fields such as `kind`, `collection_path`, `detail_path`,
`validate_path`, `scaffold_path`, `storage_root`, CRUD/validation/scaffold
booleans, `restart_required_on_save`, `hot_reload_supported`,
`apply_state_on_save`, `apply_message`, and `diagnostics_path`. Some resources
also include an `actions` array with action names, methods, paths, return
kinds, and whether the action requires a saved resource. Studio should use
these fields for editor badges and operation buttons instead of hard-coding
resource behavior. Examples include workflow graph import/export, workflow
template capture/fork, workflow schema capture/activate/import/export, and kit
bundle import/export/validation.
Provider, agent, and MCP tool modules report
`apply_state_on_save: "restart_required"` because active provider clients,
agent profiles, and MCP child processes are built during runtime startup.
Skills report `hot_reload_when_supported`, while workflow graphs, workflow
templates, workflow schemas, policy rules, team templates, kits, and Studio
metadata resources report active/catalog-oriented apply states.
`capabilities.config_diagnostics_field_targets` and
`capabilities.config_diagnostics_severity_counts` advertise that
`/api/config/diagnostics` includes form-field targets plus top-level severity
counts for settings badges. `capabilities.config_diagnostics_recommendations`
advertises backend-maintained next-step text in each diagnostic item when
available. `capabilities.config_diagnostics_apply_summary` advertises the
structured `/api/config/diagnostics.apply` restart/rebootstrap summary.
Workspace command entries cover `/workspace status`, `/workspace confirm`,
`/workspace clear`, `/workspace use <path>`, preflight checks, and `@file`
suggestions.
The `runtime_workspace_action_metadata` capability means `/api/runtime`
includes `workspace_capabilities` and `workspace_actions` in addition to the
plain workspace snapshot.

`POST /api/run` returns the final JSON agent result. When the request body sets
`"background": true`, `/api/run` and `/api/run/stream` both return
`202 Accepted` with `run_id`, `events_url`, `timeline_url`, `replay_url`,
`actions_url`, `diffs_url`, `cancel_url`, and the initial `agent_runs` snapshot
instead. The run continues under a server-owned context, so browser refreshes
or tab navigation do not cancel the ordinary Agent turn. Clients can reconnect to
`GET /api/agent-runs/{id}/events/stream?since=<last_seq>` or use the standard
`Last-Event-ID` header to replay only missed events. Browser clients should use
the returned URLs directly when possible instead of reconstructing paths.
`GET /api/agent-runs` returns the legacy newest-first run array when called
without query parameters. When any collection filter is supplied, or when
`envelope=1` is passed, it returns `{runs, filters, counts}` for Studio history
lists. Supported collection filters are `agent` / `agent_id`, `mode`, `status`,
`tool` / `tool_name`, `q` / `search`, `retry_of`, `limit`, `active=1`,
`terminal=1`, `needs_action=1`, and `errors_only=1`; counts include total,
matched, returned, active, terminal, needs-action, and error totals.
`GET /api/agent-runs/{id}/timeline` returns a compact UI-oriented timeline
with run, status, tool, approval, error, token, and final-message items.
`GET /api/agent-runs/{id}/replay` returns the run snapshot, filtered events,
timeline, actions, and query filters in one bundle for history/detail panels.
The replay bundle also includes `diffs`, parsed from write/delete tool results
when available. `GET /api/agent-runs/{id}/diffs` returns only those parsed
changes with git-like status codes, line deltas, ranges, bounded diff previews,
and a ready-to-render `patch` string for Studio diff viewers.
`GET /api/agent-runs/{id}/export?format=json|md` exports the same replay bundle
as formatted JSON or a readable Markdown report. It accepts the same filters as
`/events`, `/timeline`, and `/replay`; pass `include_content=1` when the user
explicitly wants full event/output text in a Markdown export.
When an ordinary Agent run pauses on tool approval, the run snapshot includes
run-scoped `pending_approvals`, and each pending approval includes a `risk`
object when the backend can infer the MCP tool profile. The risk metadata
contains the capability kind, risk level, destructive/approval flags, server
isolation level, sandbox enforcement flags, warnings, and recommendations.
It is diagnostic context for the operator; enforcement still comes from agent
policy, approval choices, and MCP isolation. `/actions` advertises approve,
approve and remember, deny, approve-all, approve-and-remember-all, and deny-all
actions. When `tool_risk_policy.disable_remember_for_unsandboxed_risky_tools`
is enabled, remember-style ordinary Agent actions are returned with
`available:false` and a policy reason for unsandboxed write/exec/network/unknown
tools; one-time approve and deny actions remain available when the run is
otherwise resumable.
These approval actions run in the background and write results back to the same
run snapshot. The suspended ordinary tool loop is stored in the run's durable
resume context, so a later runtime load from the same session snapshot can
approve or deny the tool and continue the Agent turn. If a legacy run or
corrupted snapshot lacks that resume context, `/actions` marks those approval
actions unavailable and the client should offer retry or cancel.
Action discovery responses for ordinary Agent runs and workflow runs include
machine-readable button/form metadata. Each action may include `kind`,
`supports_background`, `supports_stream`, `accepts_body`, `requires_body`,
`body_schema`, and `risk`. Studio should use these fields instead of inferring
behavior from the action name. Tool approval actions include the same risk
profile shape used by pending approvals and stream events, while manual-input
actions mark `requires_body: true` and describe the expected `inputs` object in
`body_schema`. The `run_action_protocol_metadata` capability flag advertises
that these fields are present. By default `/actions` returns the legacy action
array. Add `?envelope=1` or `?meta=1` to receive a versioned discovery envelope
with `meta.schema: "goflow.run_actions"`, run status, `needs_action`,
`events_path`, replay/timeline/action paths, a recommended action, and action
counts. This envelope is intended for Studio action panels and toolbar state.

`POST /api/run/stream` returns `text/event-stream` and forwards runtime
`schema.StreamEvent` values as SSE frames by default. When the request body sets
`"background": true`, it returns the same durable run acceptance JSON as
`POST /api/run` instead of binding execution to the browser request.
Tool-call, approval, and tool-result stream events include the same optional
`risk` object as pending approval snapshots when the backend can infer the MCP
tool profile. Durable Agent and workflow run replay/events endpoints persist or
reconstruct that field for history panels.

Resource editors can validate before saving modular skills, providers, agents,
and MCP tool servers with:

- `POST /api/resources/skills/validate` or `/api/resources/skills/{name}/validate`
- `POST /api/resources/providers/validate` or `/api/resources/providers/{id}/validate`
- `POST /api/resources/agents/validate` or `/api/resources/agents/{id}/validate`
- `POST /api/resources/tools/validate` or `/api/resources/tools/{name}/validate`
- `POST /api/resources/workflow-schemas/validate` or `/api/resources/workflow-schemas/{name}/validate`
- `POST /api/resources/workflow-templates/validate` or `/api/resources/workflow-templates/{name}/validate`

These dry-run endpoints apply the same defaults and structural checks used by
`PUT`, render and parse the generated YAML or `SKILL.md` where applicable, and
return `valid:true` with a `normalized` document plus restart/apply guidance
when the resource type needs it. They do not write skill, provider, agent,
tool-code, MCP config, workflow schema, or workflow template files. Workflow
schema validation also avoids importing the draft into the active session
catalog. Validation failures return `valid:false` with structured `issues` so
Studio forms can show field errors before attempting a save. Each issue includes
`severity`, `field`, `message`, and, when the backend can classify it, a stable
`code` plus operator-facing `recommendation`. Frontend forms should prefer
`code` / `field` for control highlighting and use `message` as the detailed
error text.

`GET /api/runs` returns one Studio-friendly envelope that combines ordinary
Agent runs and workflow runs. Each item includes `type`, status, request,
summary, agent/workflow metadata, action/replay/evidence/diff/event paths, and
`needs_action` / `has_error` flags. Items also include `actions_summary` when
the backend can compute available actions without an extra request. The summary
contains total/available action counts, actions that require a body, actions
with risk metadata, a recommended action, available action names, and
lightweight available action rows. Studio can use it for history-list badges and
quick actions, then open the item-specific `actions_path` or
`actions_path?envelope=1` for full button/form metadata.
Supported filters are `type=agent|workflow`,
`agent` / `agent_id`, `mode`, `workflow` / `name`, `status`, `tool` /
`tool_name`, `action` / `action_name`, `q` / `search`, `retry_of`, `limit`,
`active=1`, `terminal=1`, `needs_action=1`, and `errors_only=1`. The `counts`
block includes total,
matched, returned, agent, workflow, active, terminal, needs-action, and error
totals. The `facets` block summarizes available run types, statuses, agents,
modes, workflows, tools, and currently available actions, including counts, so
Studio can render a unified history page and filter controls without separately
joining and scanning the agent and workflow collections.

`GET /api/workflow-runs/{id}/evidence` returns a workflow-specific
evidence/provenance graph with stage, artifact, check, file, and reference
nodes; typed edges such as `produced`, `checks`, `evidenced_by`,
`related_file`, and `references`; quality counters; failed criteria and
validation issues; and the same `stage`, `kind`, `artifact_kind`, `status`, and
search filters used by replay endpoints. Studio should use this endpoint for
evidence panels, quality-gate result displays, and provenance graph views
instead of re-deriving those relationships from raw replay JSON.

Vertical Agent kits are versioned YAML manifests stored under
`kits/<name>/kit.yaml`. A kit can reference agents, providers, skills, tools,
workflow graphs, workflow templates, team templates, policy rules, required
environment variables, examples, tags, and metadata. `GET /api/kits` lists the
catalog, `GET /api/kits/{name}` returns a manifest with compatibility
validation, and `/api/resources/kits/{name}` can save or delete the same file.
CLI users can run `/kits` to list local kit manifests and `/kits <name>` to
inspect one manifest's referenced resources, examples, metadata, file path, and
missing environment-variable warnings.
`GET /api/resources/kits/{name}/validate` checks whether a saved kit's
referenced agents, skills, tools, workflows, templates, policy rules, and
required environment variables are available in the active runtime. Missing hard
dependencies are errors; unseen tools and missing environment variables are
warnings so teams can prepare a kit before all optional runtime pieces are
active. Studio can validate an unsaved draft with
`POST /api/resources/kits/validate` or
`POST /api/resources/kits/{name}/validate`; the backend normalizes the draft,
returns `normalized` plus compatibility `issues`, and does not write
`kits/<name>/kit.yaml`.

Kit scaffolds provide backend presets for common vertical domains:
`software-engineering`, `agent-framework`, `web-security`,
`security-research`, `binary-analysis`, `documentation`,
`operations-runbook`, and `customer-support`. CLI users can create the starter
manifest with
`/new-kit <preset> <name>`. HTTP users can call
`GET /api/resources/kits/scaffolds` to list presets with referenced agents,
skills, tools, workflows, templates, policy rules, and validation readiness.
`POST /api/resources/kits/scaffolds/{preset}` writes a new
`kits/<name>/kit.yaml` manifest; the JSON/YAML body can override `name`,
`title`, `description`, references, examples, tags, and metadata. Existing kits
are protected by default; pass `overwrite=1` or body field `"overwrite": true`
to update an existing manifest.

Team template scaffolds provide backend presets for reusable multi-agent teams:
`software-review`, `agent-framework`, `security-review`, `web-research`,
`binary-triage`, `documentation`, `operations-runbook`, and
`customer-support`. HTTP users can call
`GET /api/resources/team-templates/scaffolds` to list presets,
`GET /api/resources/team-templates/scaffolds/{preset}?name=<team-name>` to
preview the generated template document, and
`POST /api/resources/team-templates/scaffolds/{preset}` to write a validated
`templates/teams/<name>.yaml` resource. The JSON/YAML body can set `name`,
`title`, `description`, `category`, `tags`, `recommended_workflow`,
`recommended_entry_agent`, and `overwrite`. Existing built-in or custom names
are protected by default; pass `overwrite=1` or body field `"overwrite": true`
to create a custom override.

Team template editors can validate before saving with
`POST /api/resources/team-templates/validate` or
`POST /api/resources/team-templates/{name}/validate`. These endpoints do not
write files. They return `valid:true` with a normalized template document, or
`valid:false` with structured `issues` such as `role_templates`, `handoffs`,
`blackboard_templates`, or `quorum_presets`.

Policy rule editors can validate before saving with
`POST /api/resources/policy-rules/validate` or
`POST /api/resources/policy-rules/{name}/validate`. These endpoints normalize
the v2 `goflow.workflow_policy_rule` envelope, validate the operator/defaults
using the same rule engine metadata as save, and return structured issues
without writing `policies/workflow_rules/*.yaml`.
Policy rule scaffold presets are available through
`GET /api/resources/policy-rules/scaffolds`,
`GET /api/resources/policy-rules/scaffolds/{preset}`, and
`POST /api/resources/policy-rules/scaffolds/{preset}`. Current presets cover
risk thresholds, truthy references, text contains checks, minimum counts, team
review quorum gates, and custom expression gates. Creation writes a validated
`policies/workflow_rules/<name>.yaml` file, returns `201 Created`, and refuses
to replace existing rules unless `overwrite=true` is supplied.

Workflow node metadata and expression helper metadata editors can validate
before saving with:

- `POST /api/resources/workflow-node-metadata/validate` or `/api/resources/workflow-node-metadata/{type}/validate`
- `POST /api/resources/expression-helpers/validate` or `/api/resources/expression-helpers/{name}/validate`

These endpoints normalize the metadata resource, verify that it targets an
existing workflow node type or expression helper, return structured `issues` on
failure, and do not write `metadata/workflow_nodes/*.yaml` or
`metadata/expression_helpers/*.yaml`.

Kit bundles use `kind: goflow.kit_bundle` and package the kit manifest plus any
referenced resource documents that are available on disk or in the active
runtime. `GET /api/resources/kits/{name}/export?format=json|yaml` downloads the
bundle; provider API keys are omitted by default and only included when the
operator explicitly passes `include_secrets=1`. `POST /api/resources/kits/import`
accepts the same JSON/YAML bundle and writes included providers, agents, skills,
tools, workflow graphs, workflow templates, team templates, policy rules, and
the kit manifest back to their modular resource directories. Pass
`overwrite=false` to skip existing resources instead of replacing them. Imports
return per-resource saved/skipped/error rows plus validation output. Provider,
agent, and MCP tool modules may require a runtime restart or reload before they
become active in the running process.

The terminal exposes the same bundle path for code-state users:
`/kits <name> --export [--format json|yaml] [--include-secrets]` prints a
bundle, and `/kits --import <path> [--replace]` imports it into the active
runtime home. Without `--replace`, existing resources, including built-in
templates already visible in the runtime, are kept and reported as skipped.

When the workspace was only defaulted from the server process directory,
workspace-scoped HTTP requests return `409 workspace_required` until the
operator confirms the workspace through `POST /api/workspace/confirm` or the
browser page at `GET /workspace`. `POST /api/workspace/select` can confirm the
current path. When HTTP runtime rebinding is available, selecting a different
path rebuilds the runtime under the new root and returns the new workspace
snapshot after checking for active runs, pending approvals, pending handoffs,
and paused workflow state. When it is unavailable or blocked, selecting a
different path returns blocker metadata or `restart_required` guidance so the
operator can restart with `--workspace <path>` and rebind MCP servers safely.
`GET /api/workspace` returns the current workspace snapshot plus Studio-facing
guidance:

- `capabilities.pure_chat_without_workspace`: pure chat can continue without
  confirmed workspace access
- `capabilities.workspace_required_for_file_tasks`: file, tool, workflow,
  command, and `@file` operations must use a confirmed workspace
- `capabilities.can_confirm_current_workspace` and
  `can_clear_confirmation`: whether Studio should enable confirm/clear actions
- `capabilities.can_switch_workspace_without_restart`: whether selecting a
  different workspace can happen in-process
- `capabilities.runtime_rebind_supported`: whether the active HTTP server can
  safely rebuild MCP processes, workspace-scoped approvals, session state, and
  tool policy to a different root in-place
- `capabilities.switch_requires_restart`: true when a restart is still required
- `capabilities.requires_runtime_restart`: true when a restart is still required
- `capabilities.switch_mode`: `dynamic_rebind` or `restart_required`
- `capabilities.switch_blockers`: machine-readable blocker codes such as
  `mcp_servers_bound_to_workspace_root`,
  `session_state_and_approvals_are_workspace_scoped`, and
  `tool_policy_is_evaluated_against_startup_workspace`
- `capabilities.switch_blocker_details`: structured blocker objects with
  `code`, `category`, `severity`, `actionable`, `transient`, `message`, and
  `recommendation` fields for user-facing guidance
- `capabilities.restart_command_hint`: a CLI-style command template such as
  `["goflow", "--workspace", "<path>"]` when restart is required
- `capabilities.switch_plan` and the top-level `switch_plan`: a structured
  switch plan with `mode`, `strategy`, `runtime_rebind_supported`,
  `requires_runtime_restart`, `requires_mcp_rebootstrap`,
  `requires_session_scope_reset`, `requires_approval_scope_reset`,
  `requires_tool_policy_rebuild`, `blockers`, `blocker_details`,
  `restart_command_hint`, `suggested_args`, and selected target-path fields
  when a path was submitted
- `capabilities.guidance`: a versioned `goflow.workspace.guidance.v1` client
  contract with preflight/select/folder-picker endpoints, conflict fields,
  refresh paths, and ordered operator steps for `dynamic_rebind` and
  `restart_required` modes
- `capabilities.can_open_host_folder_picker`: currently `false`; the backend
  does not open native UI dialogs
- `capabilities.folder_picker_mode`: currently `client_or_text`, meaning a Web
  or desktop shell may show its own folder picker and submit the selected path
- `actions`: button-ready hints for `confirm`, `clear`, `select_same`, and
  `switch`, plus the client-only `choose_folder` hint. Actions include HTTP
  method, API path, availability, restart requirement, descriptions,
  `client_only`, `follow_up_action`, and `suggested_args` such as
  `["--workspace", "<path>"]`

When `POST /api/workspace/select` receives a different workspace path, the
dynamic-rebind `200` response or restart-required `409` response repeats the
switch metadata at the top level with
`selected_path`, `normalized_selected_path`, `restart_reason`,
`switch_blockers`, `switch_blocker_details`, `suggested_args`,
`restart_command_hint`, and `switch_plan`. Studio should render these
structured fields instead of parsing the human-readable message.

When `runtime_rebind_supported` is false, the restart requirement is
deliberate: MCP server processes, remembered tool approvals, session state, and
workspace-scoped tool policy are bound at runtime startup. When
`runtime_rebind_supported` is true, the backend performs that teardown and
rebootstrap in-process, but only while no Agent/workflow execution or pending
workspace approval would be orphaned by the switch.
`POST /api/workspace/requirement` lets Studio preflight a draft request before
submitting a run. The JSON body can include `input` and optional `operation`
values such as `workflow`, `@file`, `tool`, `write`, or `exec`; the response
reports `required`, `confirmed`, `blocked`, `reason`, `message`, and
`confirmation_endpoint`.

`GET /api/workspace-files` powers Web and CLI `@file` suggestions. It accepts
`prefix`, `q`/`query`, and `limit` query parameters. The backend skips low-value
directories such as `.git`, `.goflow`, `.venv`, `node_modules`, `__pycache__`,
`vendor`, `dist`, and `build` during traversal before applying result limits,
so dependency folders cannot hide real project files. `prefix` still supports
path-prefix completion, and when it is a plain filename fragment it also works
as a contains search; `q`/`query` is an explicit contains search over relative
paths and file names. The response includes explainability metadata for Studio:
`search_mode` (`all`, `prefix`, `prefix_or_contains`, or `contains`),
`traversal_limit`, `visited`, `traversal_truncated`, and `ignored_dirs`.
`truncated` still means the returned result set was capped by `limit`, while
`traversal_truncated` means the backend reached its internal traversal cap
before seeing the whole workspace.

`GET /api/session` returns the same persisted workflow, pending handoff, and
pending approval snapshot surfaced by the CLI. Pending ordinary approvals,
durable Agent-run `pending_approvals`, workflow `pending_tool_risk`, and
durable workflow-run `pending_tool_risk` are annotated with tool risk metadata
when the backend can infer the profile from the active MCP catalog.

`GET /console` serves the modular Agent Studio UI. It is a browser-native
workflow platform surface, not a terminal clone. It includes overview,
workflow Studio, playground, workspace, approvals, resource catalog,
observability, and settings modules.

`GET /api/runtime` returns the browser-facing runtime inventory: version,
active agent, mode, workspace state, session snapshot, agents, skills, tools,
MCP health, MCP server isolation/risk profiles, and first-run
environment-variable status. `mcp_servers` distinguishes lifecycle-only,
resource-control, container-configured, and unsandboxed servers and reports
whether network, filesystem, and privilege sandboxing are actually enforced.
The embedded session snapshot carries the same pending-approval risk fields as
`GET /api/session`, so Studio can render approval cards without making a second
resource lookup.
`workspace_capabilities` and `workspace_actions` mirror the guidance from
`GET /api/workspace`, so Studio can refresh one runtime payload and still know
whether pure chat is allowed, whether file/workflow/tool operations need
confirmation, which workspace actions are available or restart-required, and
which follow-up endpoints should be refreshed after a workspace switch.

`POST /api/runtime/agent` switches the active agent for browser-initiated runs.
The Studio Playground uses this to let users choose `chat`, `planner`, `fixer`,
`auditor`, or any custom configured agent before sending a request. Tool policy
and approval behavior still come from the selected agent profile.

`GET /api/config/diagnostics` reload-validates the selected config, summarizes
modular provider, agent, MCP, workflow, workflow template, workflow schema, team
template, workflow metadata, expression metadata, policy rule, and kit files,
reports missing provider keys, unknown workflow references, broad default-agent
permissions, and whether saved agent/provider/MCP server config differs from
the active runtime and therefore needs a restart or future rebootstrap. Common
provider, agent, and MCP diagnostics include `target_kind`, `target_name`, and
`field` so Studio can highlight the exact form control. Diagnostic items may
also include a `recommendation` with a backend-maintained next step. Empty
extension directories such as `templates/workflows`, `schemas/workflows`, and
`metadata/workflow_nodes` are reported as `module_dir_empty` info items with
`category: "optional"`, `optional: true`, and `actionable: false`; they are
normal extension capacity, not missing built-in resources. MCP env
diagnostics are name-only: `mcp_env_allowlist_sensitive` reports credential-like
variable names without exposing values, while
`mcp_env_allowlist_implicit_minimal` explains that an omitted `env_allowlist`
uses the minimal child-process environment. Global safety-policy diagnostics are
also field-targeted: `tool_risk_policy_*` items explain disabled policy, forced
approval, disabled remember scopes, strict rejection, and the warning state
where approval is required but approve-and-remember remains allowed. Enabled MCP
servers also receive `tool_risk_policy_mcp_*` diagnostics that identify broad
container boundaries, Linux network-only boundaries, and servers that strict
risk policy will still treat as unsandboxed. `/api/help` and `/api/capabilities`
advertise this support as
`config_diagnostics_tool_risk_policy` and the more specific
`config_diagnostics_tool_risk_policy_mcp_boundaries`. The response also includes a
`diagnostics` severity count with total, info, warning, and error counts for
status badges. The response also includes an `apply` object for Studio restart
guidance:

- `apply.state`: `active`, `restart_required`, or `error`
- `apply.restart_required`: whether saved config differs from the active
  runtime
- `apply.affected_kinds`: changed runtime-bound resource families such as
  `provider`, `agent`, or `mcp_server`
- `apply.rebootstrap_blockers`: stable blocker codes explaining why the active
  runtime cannot safely hot-apply the saved config today
- `apply.runtime_rebootstrap_supported` and `apply.hot_reload_supported`:
  currently `false` for provider, agent, and MCP server config changes
- `apply.restart_command_hint`: button-ready command parts such as `goflow`,
  `--config`, and `--workspace`

Use `GET /api/config/diagnostics?include_optional=0` or
`?verbose=0` when a consumer wants to hide optional non-actionable extension
directory info items while keeping actionable warnings and errors. The CLI
equivalent is `/config-diagnostics`; add `--json` to print the full
machine-readable envelope returned by the HTTP endpoint.

`GET/PUT /api/resources/skills/{name}` backs the Studio skill editor. A save
generates `skills/<name>/SKILL.md`, parses it with the same skill validator used
at startup, then reloads skills when the configured skill manager supports hot
reload. This is an explicit operator configuration save, separate from LLM tool
calls and their approval flow. Studio can preflight a skill with
`POST /api/resources/skills/validate` or
`POST /api/resources/skills/{name}/validate`; the backend renders a temporary
`SKILL.md`, parses it with the normal skill parser, returns the normalized
document or structured issues, and removes the temporary file without touching
the real skill directory or triggering hot reload.

`GET/PUT/DELETE /api/resources/agents/{id}` and
`GET/PUT/DELETE /api/resources/providers/{id}` edit versionable module files
under `configs/agents/` and `configs/providers/`. Those resources are
configuration saves; PUT responses include `restart_required`,
`apply_state: "restart_required"`, and a human-readable `apply_message` because
they take effect in the active runtime after restart or a future runtime
rebootstrap. These apply-state fields are JSON response metadata only and are
not written into the YAML module files. GET detail and list responses also
include apply-state metadata when a module file exists: `active` means the saved
module matches the current runtime, while `restart_required` means Studio should
prompt for restart/rebootstrap before the edit is active.

`GET/PUT/DELETE /api/resources/tools/{name}` edits a Python MCP server under
`mcp_servers/<name>.py` and its modular server config under
`configs/mcp_servers/<name>.yaml`. PUT responses include the same restart/apply
metadata because MCP server processes are started during runtime bootstrap.
Tool list/detail responses include saved MCP modules from disk even before they
are loaded, so newly created tool servers remain visible after a page refresh.
DELETE removes only file-backed custom modules and rejects tools that are only
discovered from the active MCP runtime.

Tool scaffold presets create safer starter modules for Studio and HTTP clients:
`python-local`, `python-container-readonly`, `python-container-writer`, and
`python-container-network`. `GET /api/resources/tools/scaffolds` lists presets,
`GET /api/resources/tools/scaffolds/{preset}?name=<tool-name>` returns a
preview document, and `POST /api/resources/tools/scaffolds/{preset}` writes
`mcp_servers/<name>.py` plus `configs/mcp_servers/<name>.yaml`. The request body
may be JSON or YAML, for example:

Container preset list entries include `default_image` and
`default_isolation_options` so clients can show the default runtime boundary
without generating a named preview document first.

```json
{
  "name": "sandboxed-reader",
  "image": "ghcr.io/fymatt/goflow-agent-mcp-python:<version>",
  "runtime": "docker",
  "workspace_mount": "ro",
  "network": "disabled",
  "overwrite": false,
  "isolation_options": {
    "memory": "256m"
  }
}
```

Pass `overwrite=1` or body field `"overwrite": true` to replace an existing
file-backed custom tool. Container presets mount the generated Python file into
the container with `tool_source`, `tool_target`, and `tool_mount: ro`, then run
`python <tool_target>` inside the selected image. They still require a runtime
restart or future MCP rebootstrap before the new server process is active.
When `image` is omitted, release builds default to the matching
`ghcr.io/fymatt/goflow-agent-mcp-python:<version>` image tag; development
builds fall back to `latest`.
Container presets also default to `ipc: none` and `userns: auto`; override
`userns` with `nomap`, `keep-id`, or remove it when the selected Docker/Podman
runtime does not support automatic user namespaces.

Tool resource responses also include a `risk` object when the backend can infer
one. For discovered runtime tools, `risk` includes the qualified tool name,
tool kind, broad capabilities, destructive/approval flags, server isolation
level, whether filesystem/network/privilege sandboxing is actually enforced,
and warnings/recommendations. Studio should use this field to warn users before
enabling or approving write, exec, network, unknown, or destructive tools. The
field is diagnostic metadata; it does not by itself enforce a sandbox. Runtime
tool details can be read with the qualified path form, for example
`GET /api/resources/tools/file_tools/read_file`; PUT and DELETE remain limited
to file-backed custom MCP server modules.
The same `risk` shape is reused in stream events and pending approval snapshots
returned by `/api/session`, `/api/runtime`, `/api/agent-runs`, and
`/api/workflow-runs` so approval dialogs can show the current call's risk
without joining against the tool catalog manually.

`GET /api/update-policy` returns the update strategy metadata used by the
settings page. Release-archive self-update should verify checksums/signatures
and remain opt-in. Docker and source installs should normally show prompted
upgrade commands rather than mutate themselves silently.

`GET /workspace` serves a basic workspace status and confirmation page.

`GET /workflows` serves the Studio focused on the visual workflow canvas.
Custom workflow graphs saved through the Studio are persisted under
`workflows/<name>/workflow.yaml` in runtime home.

## Backward compatibility

The loader still accepts the older top-level `llm` shape. When used, it is normalized into:

- one provider
- one default agent
- the current runtime defaults

That compatibility exists to keep existing configs loading, not as the recommended format for new deployments.

