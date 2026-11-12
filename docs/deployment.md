# Deployment

GoFlow V3 is distributed as a native local application for Windows 10/11 x64. Linux, Docker, Podman, virtual machines, and arm64 packages are not supported product paths.

## Runtime layout

- `goflow.exe`: main runtime.
- `configs/`: built-in providers, agents, and MCP connections.
- `mcp_servers/`: native built-in tools.
- `skills/`: domain instructions and reusable assets.
- `workflows/`: executable workflow definitions.
- the selected workspace: user project files, and possibly `.goflow/session.json` session data in the default source deployment.
- the data root: sessions, attachments, task copies, backups, indexes, and recovery metadata.

Keep the data root outside the selected workspace. GoFlow automatically moves task staging to an external temporary directory when an unsafe overlap is detected.

Inspect the configured `GOFLOW_DATA_ROOT` and session path before cleaning. Sessions, task staging, secrets and workspace versions are not disposable caches. See [local operations](./local-operations.md).

## Windows

Run the release archive from a user-writable installation directory:

```powershell
.\goflow.exe --config .\configs\goflow.binary.yaml --workspace D:\Projects\example --http 127.0.0.1:8080
```

Windows controlled execution uses Job Objects, restricted tokens, low-integrity processes, task-scoped ACL leases, and AppContainer when the host capability probe succeeds. Unknown code is refused when the required boundary is unavailable.

## Network exposure

Bind to `127.0.0.1` by default. Before binding to another interface, place GoFlow behind an authenticated local reverse proxy and restrict access with the operating-system firewall. The browser API is intended for the local user, not for anonymous public hosting.

## Native MCP execution

Ordinary users do not configure isolation internals. Connections use `isolation: auto`; trusted built-in tools use the narrow native adapter required by their capability. Approved unknown and third-party MCP servers are bounded to the confirmed workspace and cannot access paths outside it; task snapshots and version rollback protect writes.

## Validation

```bash
python scripts/validate_deployment_assets.py
python scripts/validate_release_archives.py --help
go test ./...
```

Deployment acceptance requires Windows native install, upgrade, recovery, sandbox, browser, and complex-task checks.
