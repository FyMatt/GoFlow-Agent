# MCP Isolation Strategy

[English](./mcp-isolation.md) | [简体中文](./mcp-isolation.zh-CN.md)

GoFlow supports MCP servers as local child processes for compatibility, but the
recommended strong isolation boundary is Docker/Podman container isolation.
Container isolation gives the same configuration model on Windows, Linux, and
macOS when a container runtime is available. Native adapters remain useful as
fallbacks or lifecycle/resource helpers, but they should not be presented as a
complete sandbox.

## Current Baseline

Implemented protections:

- workspace-root path checks inside built-in file-oriented tools
- MCP command allowlists through `allowed_commands` or `allowed_command_paths`
- minimal child-process environment when `env_allowlist` is omitted
- request and response byte limits
- tool kind policies at the agent layer
- approval prompts for write, exec, network, or confirm-policy calls
- `isolation: container` for the recommended Docker/Podman boundary, with
  explicit image, workspace mount, network mode, env allowlist, and
  resource-limit settings
- `isolation: process_group` for process lifecycle isolation
- `isolation: windows_job` for opt-in Windows Job Object lifecycle isolation
- `isolation: windows_restricted_token` for opt-in Windows restricted-token and low-integrity privilege reduction
- `isolation: linux_cgroup` for opt-in Linux cgroup resource-control isolation
- `isolation: linux_netns` for opt-in Linux network namespace isolation through `unshare --net`
- `/api/runtime.mcp_servers` server-level isolation/risk summaries
- `/api/config/diagnostics` warnings for advisory isolation settings, implicit
  minimal MCP environments, sensitive env allowlist names, and incomplete
  container hardening options, including machine-readable image provenance
  details
- `/api/resources/tools` per-tool `risk` summaries for discovered and saved MCP tools
- per-tool `risk` summaries detect path-like schema inputs and mark
  `workspace_scoped_inputs`; known built-in workspace-scoped tools are marked
  `workspace_scope_enforced`, while third-party file-oriented tools are warned
  unless their server boundary is recognized
- tool-call, approval, tool-result, pending approval, and durable run snapshots
  include tool `risk` metadata when the backend can infer the profile, so
  approval UIs can show the same safety context at decision time
- `/api/runtime.mcp_isolation_modes` exposes a platform-aware isolation catalog
  for settings UIs, including implemented status, config support, required
  options, and enforced/missing sandbox features
- optional `tool_risk_policy` config can force approval for unsandboxed risky
  tools, disable approve-and-remember for those calls, or strictly reject
  unsandboxed risky tools unless the MCP server runs through an enforced
  sandbox for that tool capability. `isolation: container` covers risky tools
  broadly; `isolation: linux_netns` only covers network-kind tools.
- CLI `/tools` risk and isolation summaries for terminal users

Important limit:

`container` is the broad real sandbox boundary GoFlow recognizes for risky MCP
tools. Its effective strength still depends on the container runtime, image,
mounts, network mode, privileges, and resource limits. `process_group`,
`windows_job`, and `linux_cgroup` help with lifecycle or resource control.
`windows_restricted_token` is a real Windows privilege-reduction adapter and
also uses Job Object lifecycle cleanup, but it does not enforce filesystem or
network policy. AppContainer-like isolation values are intentionally rejected;
Docker/Podman `isolation: container` is the supported strong sandbox path.

## Threat Model

GoFlow should assume third-party MCP servers can be buggy or over-broad. The framework should reduce accidental damage and make unsafe capabilities visible before an agent can use them.

Out of scope for the current baseline:

- defending against malicious code executed with the same user account
- kernel-level containment on every operating system
- transparent sandboxing for arbitrary language runtimes without platform support

## Recommended Layers

### 1. Framework Policy

Keep this layer cross-platform and always enabled.

- Require startup command allowlists.
- Keep `GOFLOW_WORKSPACE_ROOT` as the only expected file root.
- Validate tool metadata before exposure.
- Require strict JSON schemas.
- Gate tools by agent `allowed_tool_kinds` and `allowed_tools`.
- Keep approval prompts for write, exec, and network tools.
- Enable `tool_risk_policy.require_approval_for_unsandboxed_risky_tools` when
  high-risk tools should require an explicit operator decision unless they run
  inside `isolation: container` or a matching capability-specific enforced
  sandbox such as `linux_netns` for network-only tools.
- Enable `tool_risk_policy.disable_remember_for_unsandboxed_risky_tools` when
  risky host-process tools should never be approved-and-remembered or
  workflow-auto-approved.
- Enable `tool_risk_policy.reject_unsandboxed_risky_tools` for high-security
  deployments where risky host-process tools must be blocked outright unless
  they run through `isolation: container` or a matching capability-specific
  enforced sandbox.

### 2. Container Isolation

Use containers as the default recommendation for third-party, generated,
write-capable, exec-capable, network-capable, or otherwise untrusted MCP tools:

```yaml
isolation: container
network_disabled: true
isolation_options:
  image: ghcr.io/fymatt/goflow-agent-mcp-tools:latest
  runtime: docker
  workspace_mount: ro
  workspace_target: /workspace
  network: disabled
  memory: 256m
  memory_swap: 256m
  cpus: "0.5"
  pids_limit: "64"
  pull_policy: missing
  readonly_rootfs: "true"
  no_new_privileges: "true"
  cap_drop: all
  ipc: none
  userns: auto
  tmpfs: /tmp:rw,noexec,nosuid,size=64m
  init: "true"
```

Purpose:

- restrict filesystem access through workspace/tool mounts
- restrict network egress through `network: disabled`
- bound CPU, memory, and process count
- bound swap allowance where the container runtime supports
  `--memory-swap`
- make image pulls explicit with `pull_policy` (`missing` for normal use,
  `never` for production environments that pre-pull trusted images)
- use immutable `image@sha256:...` references for production profile tools;
  runtime status exposes `container_image_reference_type`,
  `container_image_digest_pinned`, `container_image_production_ready`, and
  `container_pull_policy` for container MCP servers
- drop common container privileges where the runtime supports it
- keep behavior consistent across Windows Docker Desktop, Linux Docker, and
  Podman deployments

Missing Docker/Podman is a setup problem for container-isolated tools. GoFlow
does not silently fall back to host execution for `isolation: container`.

### 3. Process Lifecycle Isolation

Current implementation:

```yaml
isolation: process_group
```

Purpose:

- start MCP servers in a separate process group where supported
- improve cleanup of child process trees
- provide a lightweight lifecycle fallback for trusted local tools

This is a compatibility fallback for trusted local development. It is not a
sandbox.

### 4. Platform Security Adapters

These adapters should be opt-in because behavior differs by host OS. Docker or
Podman `isolation: container` is the only current broad sandbox boundary.

| Adapter | Platform | Useful For | Notes |
|---|---|---|---|
| Windows Job Object | Windows | process tree limits, kill-on-close | implemented as `windows_job`; lifecycle cleanup only; not restricted-token containment |
| Restricted token | Windows | stronger privilege reduction plus process-tree cleanup | implemented as `windows_restricted_token`; lowers token privileges, sets low integrity, and attaches a Job Object; not filesystem/network policy |
| seccomp-bpf | Linux | syscall filtering | useful for narrow helpers, hard for Python/Go runtimes without tuning |
| chroot / pivot_root | Linux/Unix | filesystem view reduction | requires setup and sometimes elevated privileges |
| cgroups | Linux | CPU/memory/process limits | implemented as `linux_cgroup`; requires a writable cgroup v2 parent |
| container runtime | Linux/Windows/macOS | broad isolation boundary | implemented as `container`; heavier dependency, image/runtime policy still matters |
| network namespace | Linux | network egress control | implemented as `linux_netns` through `unshare --net`; network-only, not filesystem/privilege isolation |
| firewall rules | OS-specific | network egress control | future adapter; should be explicit per server, not inferred from `network_disabled` |

## Config Direction

Prefer this shape for real sandboxing:

```yaml
isolation: container
network_disabled: true
isolation_options:
  image: ghcr.io/fymatt/goflow-agent-mcp-tools:latest
  workspace_mount: ro
  network: disabled
```

Keep the existing `isolation` field simple for fallback adapters:

```yaml
isolation: process_group
```

Platform-specific adapters use explicit names, for example:

```yaml
isolation: windows_job
```

or:

```yaml
isolation: windows_restricted_token
```

or:

```yaml
isolation: linux_cgroup
isolation_options:
  cgroup_parent: /sys/fs/cgroup/goflow
  cgroup_name: file-tools
  memory_max: 256M
  pids_max: "64"
  cpu_max: "50000 100000"
```

For `linux_cgroup`, GoFlow validates resource values before writing them.
`memory_max` accepts `max`, positive byte counts, or `K`/`M`/`G`/`T` suffixes and is normalized to bytes for cgroup v2.
`pids_max` accepts `max` or a positive integer.
`cpu_max` accepts `max` or the cgroup v2 `"<quota> <period>"` form, where the period must be a positive integer.

Linux network isolation is explicit:

```yaml
isolation: linux_netns
network_disabled: true
isolation_options:
  unshare_command: /usr/bin/unshare
  map_root_user: "false"
```

For `linux_netns`, GoFlow starts the MCP command through `unshare --net -- ...`.
If `unshare` is missing or the current user lacks permission to create a
network namespace, the MCP server startup fails clearly instead of falling back
to the host network. This adapter isolates network egress only; it is not a
filesystem, mount, seccomp, or privilege sandbox.

Adapter-specific options live under `isolation_options` to avoid overloading the `isolation` string:

```yaml
isolation: container
isolation_profile: readonly
network_disabled: true
isolation_options:
  image: ghcr.io/fymatt/goflow-agent-mcp-tools:latest
  runtime: docker
  workspace_target: /workspace
  container_workdir: /app
  security_opt: seccomp=/etc/goflow/seccomp.json;apparmor=goflow-mcp
  tool_source: /opt/goflow/mcp_servers/sandboxed-reader.py
  tool_target: /goflow-tools/sandboxed-reader.py
```

`isolation_profile` is the preferred way to select reusable Docker/Podman
hardening defaults. GoFlow currently provides:

| Profile | Workspace | Network | Intended use |
| --- | --- | --- | --- |
| `readonly` | read-only | disabled | generated readers, static analysis, untrusted helpers |
| `writer` | read-write | disabled | formatters, code generators, tools that must mutate the workspace |
| `network` | read-only | bridge | fetchers and web research helpers that need outbound network access |
| `production` | read-only | disabled | pre-pulled, digest-pinned production images with `pull_policy: never` |

Profile defaults include resource limits, `readonly_rootfs`, `no_new_privileges`,
`cap_drop: all`, isolated IPC, user namespace defaults, tmpfs scratch mounts,
container init, and read-only generated-tool mounts. `readonly`, `network`, and
`production` also default to a non-root UID/GID. Explicit `isolation_options`
always win over profile defaults, so operators can override one field without
copying the whole hardening block. `isolation_options.profile` is accepted as a
legacy alias, but new configs should use top-level `isolation_profile`.

For `container`, GoFlow executes the configured MCP `command` and `args` inside the image. It does not silently fall back to host execution if Docker/Podman is missing. `ipc: none` passes `--ipc none`; `userns: auto`, `nomap`, or `keep-id` passes the matching `--userns` value when the runtime supports it. `memory_swap` passes `--memory-swap` and should usually match `memory` for read-only helper tools. `pull_policy` passes Docker/Podman `--pull` with `always`, `missing`, or `never`. `security_opt` accepts comma, semicolon, or newline-separated container runtime security options. `tmpfs` accepts semicolon or newline-separated `--tmpfs` entries and preserves commas inside each entry's mount options.

`tool_source`, `tool_target`, and `tool_mount` are optional and must be set as a
pair for single-tool bind mounts. `tool_source` must be an absolute host path,
`tool_target` is the in-container path, and `tool_mount` is `ro`, `rw`, or
`none`. Studio's containerized Python tool presets use this shape so the
generated MCP server file can be mounted read-only into a general Python image
without mounting the whole runtime home.

`GET /api/config/diagnostics` reports field-targeted container hardening hints
for MCP server modules. It warns when a container keeps network egress open,
mounts the workspace read-write, omits `memory` / `cpus` / `pids_limit`, uses an
explicit root `user`, or does not opt into `no_new_privileges`,
`cap_drop: all`, `readonly_rootfs`, and non-root `user` where writable bind
mounts are not required. It also flags missing `tmpfs` scratch mounts when
`readonly_rootfs` is enabled, missing `init`, floating image tags, images that
are not pinned by digest in production, production profiles that override
`pull_policy` away from `never`, `network: host`, shell-like startup
allowlists, missing `ipc: none`, missing `userns`, incomplete `tool_source` /
`tool_target` pairs, and read-write generated-tool mounts. Image-related
diagnostics include `details.image_reference_type` (`missing`, `floating`,
`version_tag`, or `digest`), `details.digest_pinned`, `details.image`, and
`details.pull_policy` where applicable. These diagnostics are advisory and
Studio-facing; they make weak container profiles visible without changing
runtime behavior.

## Implementation Plan

1. Make Docker/Podman `isolation: container` the recommended strong isolation
   path for new MCP tools, generated tools, risky tools, and release-facing
   examples.
2. Completed baseline: add internal process-isolation hooks around `exec.Cmd` startup and shutdown.
3. Completed baseline: implement Windows Job Object kill-on-close support for process-tree cleanup.
4. Completed baseline: add Linux cgroup support for resource limits where available.
5. Completed baseline: expose MCP server, per-tool, stream-event, and
   pending-approval risk profiles through HTTP resources and run snapshots so
   Studio can show risky capabilities before enablement and at approval time.
6. Completed backend baseline: add container isolation through Docker/Podman.
   Follow-up direction: treat it as the default recommendation for strong MCP
   sandboxing and keep native adapters as compatibility/fallback layers.
7. Completed backend baseline: add explicit Linux network namespace isolation through `isolation: linux_netns`; keep `network_disabled` advisory for non-container and non-netns servers.
8. Completed backend baseline: add HTTP MCP tool scaffold presets that can generate
   local Python tools or containerized Python tools with read-only tool-file
   mounts and restart/apply-state guidance.
9. Completed backend baseline: publish a companion
   `ghcr.io/fymatt/goflow-agent-mcp-python` image for containerized Python tool
   presets and make it the default scaffold image.
10. Completed backend baseline: add field-targeted config diagnostics for
    incomplete container hardening so Studio can attach warnings to exact MCP
    server fields before operators enable risky tools.
10a. Completed backend baseline: mirror container hardening details into
    runtime and per-tool risk profiles, including a `non_root_user` feature
    when `isolation_options.user` is configured, an explicit root-user warning,
    and a non-root recommendation for read-only/network containers that do not
    need writable bind mounts. Root checks inspect the UID/name before the
    optional group separator, so `root:*`, `0:*`, and padded numeric UID zero
    are all treated as root.
10b. Completed backend baseline: make runtime container risk scoring require
    complete resource limits (`memory`, `cpus`, and `pids_limit`) before
    reporting `resource_limited: true`. A container only reaches
    `risk_level: low` when network egress is disabled, resource limits are
    complete, the configured user is non-root, and workspace/tool mounts are
    not writable.
10c. Completed backend baseline: add reusable Docker/Podman
    `isolation_profile` presets (`readonly`, `writer`, `network`,
    `production`) with profile metadata in `/api/runtime`, `/api/help`, and
    `/api/capabilities`. Generated container MCP tools now use profile-backed
    defaults, while explicit `isolation_options` remain field-level overrides.
11. Completed backend baseline: make Windows Job Object semantics
    machine-readable in runtime and per-tool risk profiles through
    `windows_isolation`, `sandbox_features`, and `missing_sandbox_features`.
    `windows_job` currently reports `restricted_token: false` and
    `app_container: false` because it only provides process lifecycle cleanup.
12. Completed backend baseline: add `windows_restricted_token` as a real
    Windows privilege-reduction adapter using a restricted primary token and
    low integrity. It now also attaches the child to a kill-on-close Job Object.
    Runtime risk profiles report `restricted_token: true` and `job_object: true`
    while still listing filesystem policy and network policy as absent.

## Operator Guidance

For trusted local tools:

- prefer `isolation: container` when Docker/Podman is available, especially if
  the tool can write files, execute commands, fetch network content, parse
  untrusted input, or run third-party code
- use `isolation: process_group` only for trusted local tools when container
  setup is intentionally skipped
- keep command allowlists tight
- keep agent tool permissions narrow

For third-party tools:

- prefer read-only tools first
- use exact `allowed_tools` on agents
- avoid broad `exec` tools
- on Linux, use `linux_cgroup` only after provisioning a writable cgroup parent for the GoFlow user
- run inside `isolation: container` unless there is a clear reason not to
- inspect `/tools` warnings before exposing tools to agents
- inspect pending approval `risk` fields in `/api/session`, `/api/runtime`,
  `/api/agent-runs`, or `/api/workflow-runs` before approving paused tools

For untrusted tools:

- do not run them directly under the GoFlow process account
- require `isolation: container` or an equivalent external sandbox before
  enabling them
