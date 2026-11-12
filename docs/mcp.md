# MCP Connections

GoFlow uses standard stdio MCP connections for deterministic tools. The model chooses intent; Go code validates schemas, permissions, workspace scope, approvals, execution, evidence, and recovery.

## Built-In Connections

| Connection | Purpose | Boundary |
| --- | --- | --- |
| `file_tools` | Read, search, patch, and write task files | confirmed live workspace plus per-file recovery journal |
| `command_tools` | Build, test, lint, run approved commands, and manage development servers | confirmed live workspace |
| `web_tools` | Search, fetch, browser evidence, and source collection | explicit network tools |
| `network_tools` | Network operations planning and authorized device actions | approval and target policy |
| `office_tools` | Read, create, template-edit, merge, validate, and export Office/WPS documents | task workspace and Office host bridge |
| `host_tools` | Local host facts, logs, and bounded managed configuration | `target_scope: host` |
| `ssh_tools` | Explicit remote host inspection and approved operations | `target_scope: host` |
| `skill_runner` | Run deterministic scripts declared by a Skill | native execution policy |

`host_tools` reads logs only under `GOFLOW_HOST_LOG_ROOTS`. The `managed-local-config` Skill governs bounded local changes. The `remote-host-diagnostics` Skill governs remote inspection. Credentials are referenced and redacted.

## Native Isolation

GoFlow V3 does not use container runtimes or virtual machines. Supported modes are:

- `auto`: choose the smallest available native boundary; refuse unsafe fallback.
- `native_strong`: Windows AppContainer/restricted token/ACL/Job Object, or Linux Landlock/namespaces/seccomp/rlimit/cgroup v2.
- `process_group`: lifecycle cleanup for trusted built-ins.
- `windows_job` and `windows_restricted_token`: explicit Windows fallback boundaries.
- `linux_cgroup` and `linux_netns`: explicit Linux resource or network boundaries.

Unknown code and third-party MCP connections use `auto` or `native_strong` and are bounded to the confirmed workspace and declared environment. Writes still require the configured task approval. Pre-task snapshots and file journals provide recovery, and quality-gated work can be saved into the three-version workspace history.

## Runtime Contract

Each connection declares a fixed command, `allowed_commands` or `allowed_command_paths`, minimal `env_allowlist`, request/response limits, timeout, restart limit, cooldown, and maximum concurrent calls. A process that exceeds the circuit breaker enters cooldown instead of restarting forever.

```yaml
mcp_servers:
  - name: file_tools
    command: go
    args: [run, ./mcp_servers/file_tools]
    enabled: true
    workdir: .
    allowed_commands: [go]
    isolation: process_group
    restart_limit: 3
    cooldown: 10s
    max_concurrent_calls: 1
```

`network_disabled` is only considered enforced when the selected native boundary actually blocks egress.

## Network Operations

`network_tools` requires `authorized_scope`, `allowed_hosts`, credential references, dry-run planning, approval metadata, rollback information, and artifact references. `device_batch_command_plan` prepares bounded multi-device work. `device_restconf_live_apply` is an explicit high-risk action and cannot run from an unapproved plan.

## Browser Evidence

`browser_snapshot` captures the page and structured state. `browser_probe_points` identifies bounded inspection points. Any active probe requires `active_probe_approved`. Browser actions are cached where safe to reduce duplicate navigation and Token use.

## Binary Evidence

Static analysis tools include `binary_format_summary`, `binary_entropy_map`, `binary_symbol_hints`, and `binary_extract_window`. They produce bounded evidence and do not imply unsafe dynamic execution.

## Research Evidence

`research_snapshot` records sources and retrieval metadata. `compile_research_evidence` produces claim-to-source evidence with caching and exports usable Markdown, JSON, and CSV artifacts.

## Documents

`office_tools` supports new documents, reads, template-based edits, precise edits that preserve formatting, merges, validation, and PDF export. Development, security, operations, and research workflows use the same document capability for requirements, reports, runbooks, and evidence packages.

## Success Evidence

An MCP call is not success by itself. Delivery requires relevant files, Diff, build/test/lint output, source evidence, post-change checks, or document validation. Tool errors remain structured and cannot be replaced by model self-assessment.

See [configuration](./configuration.md), [isolation](./mcp-isolation.md), and [resources](./resources.md).
