# MCP Isolation Strategy

GoFlow currently treats MCP servers as local child processes. This is flexible and makes cross-language tools easy to add, but it also means isolation must be explicit and layered.

## Current Baseline

Implemented protections:

- workspace-root path checks inside built-in file-oriented tools
- MCP command allowlists through `allowed_commands` or `allowed_command_paths`
- minimal child-process environment when `env_allowlist` is omitted
- request and response byte limits
- tool kind policies at the agent layer
- approval prompts for write, exec, network, or confirm-policy calls
- `isolation: process_group` for process lifecycle isolation
- `isolation: windows_job` for opt-in Windows Job Object lifecycle isolation
- `isolation: linux_cgroup` for opt-in Linux cgroup resource-control isolation

Important limit:

`process_group`, `windows_job`, and `linux_cgroup` help with lifecycle or resource control. They are not filesystem sandboxes, network sandboxes, privilege boundaries, containers, or policy engines.

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

### 2. Process Lifecycle Isolation

Current implementation:

```yaml
isolation: process_group
```

Purpose:

- start MCP servers in a separate process group where supported
- improve cleanup of child process trees
- make future OS adapters easier to compose

This should remain the default recommendation for local development.

### 3. Platform Security Adapters

These are future adapters and should be opt-in because behavior differs by host OS.

| Adapter | Platform | Useful For | Notes |
|---|---|---|---|
| Windows Job Object | Windows | process tree limits, kill-on-close | implemented as `windows_job`; lifecycle cleanup only |
| AppContainer / restricted token | Windows | stronger privilege reduction | more complex profile and filesystem mapping |
| seccomp-bpf | Linux | syscall filtering | useful for narrow helpers, hard for Python/Go runtimes without tuning |
| chroot / pivot_root | Linux/Unix | filesystem view reduction | requires setup and sometimes elevated privileges |
| cgroups | Linux | CPU/memory/process limits | implemented as `linux_cgroup`; requires a writable cgroup v2 parent |
| container runtime | Linux/Windows/macOS | broad isolation boundary | heavier dependency, clearer security story for untrusted tools |
| firewall rules / network namespace | OS-specific | network egress control | should be explicit per server, not inferred from `network_disabled` |

## Config Direction

Keep the existing `isolation` field simple:

```yaml
isolation: process_group
```

Platform adapters use explicit names, for example:

```yaml
isolation: windows_job
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

Future adapter-specific options should also live under a separate field to avoid overloading one string:

```yaml
isolation: container
isolation_options:
  image: goflow/mcp-python:latest
  readonly_rootfs: true
  network: disabled
  memory: 256m
```

## Implementation Plan

1. Keep `process_group` as the portable baseline.
2. Completed baseline: add internal process-isolation hooks around `exec.Cmd` startup and shutdown.
3. Completed baseline: implement Windows Job Object kill-on-close support for process-tree cleanup.
4. Completed baseline: add Linux cgroup support for resource limits where available.
5. Treat container and network namespace isolation as optional deployment features, not default local behavior.
6. Keep `network_disabled` advisory until an actual network adapter is configured and reported in health output.

## Operator Guidance

For trusted local tools:

- use `isolation: process_group`
- keep command allowlists tight
- keep agent tool permissions narrow

For third-party tools:

- prefer read-only tools first
- use exact `allowed_tools` on agents
- avoid broad `exec` tools
- on Linux, use `linux_cgroup` only after provisioning a writable cgroup parent for the GoFlow user
- run inside an external sandbox or container when possible
- inspect `/tools` warnings before exposing tools to agents

For untrusted tools:

- do not run them directly under the GoFlow process account
- require a real OS/container sandbox before enabling them
