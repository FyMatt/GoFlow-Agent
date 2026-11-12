# Installation

GoFlow supports a Windows 10/11 x64 release archive.

## Install

1. Download the Windows amd64 ZIP archive.
2. Verify the archive against `SHA256SUMS` and the published signature bundle.
3. Extract it to a user-writable application directory.
4. Start GoFlow and select a workspace in the browser.

Windows:

```powershell
.\run-goflow.cmd
```

Open `http://127.0.0.1:8080` after the runtime reports that the HTTP server is ready.

## First-run requirements

- Set only the Provider API key in Settings.
- Select and confirm the workspace before file, tool, or workflow tasks.
- Review the native sandbox capability report before enabling unknown third-party connections.

Advanced native host integrations use the explicit contracts `GOFLOW_HOST_MANAGED_CONFIG_ROOTS`, `GOFLOW_HOST_CHANGE_BACKUP_ROOT`, and `GOFLOW_SSH_TOOLS_CMD`; ordinary users do not need to configure these values.

## Upgrade

Create a data backup, verify the new release archive, stop the old process, replace the application files, and restart. Persistent user data remains in the configured data root. Failed migrations must restore the pre-upgrade backup.

The archive also includes a native install manager that does not require Python or another development runtime:

Extract the new release outside the installation prefix, then run the commands below from that extracted release. On Windows, do not run `goflow-install.exe` from inside the installation being upgraded, rolled back, or removed because the running executable would block the atomic directory switch.

```powershell
.\bin\goflow-install.exe install --archive goflow-agent_VERSION_windows_amd64.zip --prefix "$env:LOCALAPPDATA\Programs\GoFlow" --data-root "$env:LOCALAPPDATA\GoFlow\data"
.\bin\goflow-install.exe upgrade --archive goflow-agent_VERSION_windows_amd64.zip --prefix "$env:LOCALAPPDATA\Programs\GoFlow" --data-root "$env:LOCALAPPDATA\GoFlow\data"
.\bin\goflow-install.exe rollback --prefix "$env:LOCALAPPDATA\Programs\GoFlow"
```

Upgrade replaces application files and preserves the data root. `rollback` restores the latest valid backup for that installation while leaving the data root unchanged.

## Uninstall

Stop GoFlow and remove the application directory. Remove the data root only when sessions, resources, backups, and recovery records are no longer needed.

```powershell
.\bin\goflow-install.exe uninstall --prefix "$env:LOCALAPPDATA\Programs\GoFlow"
.\bin\goflow-install.exe uninstall --prefix "$env:LOCALAPPDATA\Programs\GoFlow" --purge-data
```
