# Configuration

GoFlow V3 keeps ordinary setup small. Select a workspace, add the Provider API Key in Settings, and start a task. The built-in provider, model, agents, workflows, Skills, and MCP connections supply the remaining defaults.

## Start

```powershell
go run ./cmd/goflow --config configs/goflow.yaml --workspace D:\Projects\test --http 127.0.0.1:8080
```

The product target is Windows 10/11 x64 desktop Web. Linux servers and VMs may be authorized remote targets.

Provider `timeout` bounds the entire non-streaming request. For streaming it bounds the initial meaningful-output wait and periods without text, reasoning or tool-argument progress; an active stream may last longer. Empty heartbeats cannot extend the wait indefinitely. Caller deadlines and cancellation still apply. A Responses protocol failure, stall or interrupted transport before assistant output switches the next existing retry to HTTP/1.1 without increasing the retry count.

## Provider

The default provider uses the configured OpenAI-compatible endpoint. The settings page writes the key through the secret store; public configuration should use a secret reference instead of a literal value.

```yaml
providers:
  primary:
    provider: openai-compatible
    base_url: https://tokenmarket.cheap/v1
    wire_api: responses
    api_key: secret://providers/primary/api_key
    model: gpt-5.6-sol
```

There is no backup-model chain in the default product.

## Resources

User-managed resources are limited to agents, subagents, workflows, Skills, and MCP connections. Files under `configs/agents`, `configs/mcp_servers`, `workflows`, and `skills` are loaded automatically. GoFlow also reads common `AGENTS.md`, `CLAUDE.md`, and `SKILL.md` instructions.

Ordinary users should edit the Chinese name, description, permissions, and enabled state. Runtime limits and native sandbox details remain system-managed.

## MCP Connections

A local MCP connection needs a name, command, arguments, command allowlist, and isolation mode:

```yaml
mcp_servers:
  - name: file_tools
    command: go
    args: [run, ./mcp_servers/file_tools]
    enabled: true
    workdir: .
    allowed_commands: [go]
    isolation: process_group
```

Supported isolation values are `auto`, `native_strong`, `process_group`, `windows_job`, `windows_restricted_token`, `linux_cgroup`, and `linux_netns`. Use `auto` for third-party connections. GoFlow selects the native boundary and refuses unknown code when the required strong boundary is unavailable. Removed container configuration is rejected with a migration error.

`isolation_options` is only accepted for `linux_cgroup` and `linux_netns`. `network_disabled` is a declaration unless an enforced native boundary supplies network isolation.

## Host Targets

Connections that inspect the physical machine use `target_scope: host`. The built-in `host_tools` connection handles local facts and bounded configuration changes; `ssh_tools` handles explicit remote targets.

Managed local JSON changes are restricted to existing files under `GOFLOW_HOST_MANAGED_CONFIG_ROOTS`. Backups and recovery evidence are written under `GOFLOW_HOST_CHANGE_BACKUP_ROOT`. Host logs are separately allowlisted; credentials are referenced, not embedded.

## Safety Defaults

- Approved commands and third-party MCP processes are bounded to the confirmed real workspace and cannot access paths outside it; a recovery snapshot is retained before writes.
- Write, exec, network, and host changes use inline approval when required.
- Task staging, checkpoints, quality gates, and recovery are automatic.
- API keys, SSH private keys, and sensitive environment variables must not be logged or committed.
- A task is successful only when files, diffs, tests, checks, evidence, or document validation prove the result.

See [MCP](./mcp.md), [MCP isolation](./mcp-isolation.md), and [resources](./resources.md).

## Run-completion email notification

Add `notifications.email` to `configs/goflow.yaml` to send a plain-text email when a run finishes (completed, failed, blocked or cancelled):

```yaml
notifications:
  email:
    enabled: true
    smtp_host: smtp.example.com
    smtp_port: 587
    username: bot@example.com
    password: ${SMTP_PASSWORD}          # or secret://notifications/email
    from: bot@example.com
    to: [ops@example.com]
    tls: starttls                        # starttls | tls | none
    subject_prefix: "[GoFlow]"
    timeout: 20s
```

Notes: `starttls` upgrades on 587, `tls` dials TLS directly (465) and `none` is for local testing only. `from` falls back to `username`. The password supports environment expansion and `secret://` references. Delivery failures are recorded as audit entries and never change the run outcome.
