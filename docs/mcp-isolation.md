# Native MCP isolation

GoFlow V3 does not use Docker, Podman, containers, or virtual machines. MCP connections are local native processes controlled by the operating-system boundary available on the host.

## User-facing rule

Ordinary users select a connection and its capability summary. They do not configure low-level isolation, mounts, images, namespaces, or resource flags. New connections use `isolation: auto`.

## Boundary selection

- `native_strong`: broad native boundary when the capability probe reports every required primitive ready.
- `windows_restricted_token`: Windows privilege reduction plus Job Object lifecycle control.
- `windows_job`: Windows process-tree lifecycle control for trusted local helpers.
- `linux_landlock`: Linux workspace filesystem boundary.
- `linux_netns`: Linux network-only boundary.
- `linux_cgroup`: Linux CPU, memory, and process limits.
- `process_group`: lifecycle cleanup only for trusted local tools.
- `none`: trusted local process only; never use for unknown code.

The API reports `sandboxed`, `filesystem_sandboxed`, `network_enforced`, `privilege_sandboxed`, and `resource_limited` from the real host adapter. A lifecycle or capability-specific adapter is never described as a complete sandbox.

## Unknown and third-party MCP

Unknown code receives only the confirmed workspace, environment allowlist, command allowlist, and standard handles. Once approved it can operate inside that workspace, but cannot access paths outside it. An external read-only snapshot and file recovery journal protect writes. If the requested native boundary is unavailable, startup is refused with a Chinese diagnostic and a remediation action.

## Network default

Network access is disabled unless the connection explicitly requires it and the user approves the operation. `network_disabled` is not treated as enforcement by itself; enforcement comes from the native network adapter.

## Verification

Use the runtime capability report, config diagnostics, task evidence, and process audit events to verify the actual boundary. Never replace a missing host primitive with a model statement or a simulated result.
