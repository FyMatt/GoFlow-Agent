# Remote target setup and acceptance

GoFlow runs on the Windows PC. An SSH server or Linux VM is an execution target; the platform does not need to be deployed there.

## Target setup

Save an SSH Target with its ID, address, port, user, credential reference, pinned SHA256 host-key fingerprint and exact command allowlist. Select the same target for the task; host-scoped tools must use its target_id.

The SSH tool currently supports key authentication. It does not accept model-generated passwords, interactive sudo or an arbitrary shell. An operator with password access can initially install a dedicated public key without replacing existing authorized_keys. Keep the private key in a restricted secret file and reference it as `secret://targets/<target-id>/private_key`. Never put passwords or private keys in tasks, resource YAML, reports or Git.

A first scan supplies a candidate identity, not independent proof of trust. Verify it using the VM console or administrator information. Later scans and every SSH login must match the pin. Reinstallation or key rotation requires verification again; do not disable StrictHostKeyChecking.

## Read-only acceptance

Configure only commands available and authorized on that target, for example:

```text
id
uptime
df -h /
free -m
systemctl --failed --no-pager
journalctl -u ssh --since -1hour --no-pager -n 80 -o short-iso
```

The allowlist matches the complete command text. Different arguments and compound commands are refused unless separately configured. Service names, commands and log permissions vary across distributions.

Use the remote-host-diagnostics skill to check the pin, collect the agreed evidence, write a complete report to the requested workspace path and read it back. Compare actual timeline tool results, exit codes, stderr and truncation with the report. Missing commands, unavailable sudo and inaccessible logs are limitations, not passing checks. Correlate acceptance activity with authentication logs without treating it as an intrusion or claiming no other attacks occurred.

## Windows compatibility and bounded reuse

Some inbox Windows ssh-keyscan versions advertise an unimplemented key-exchange algorithm. On the specific unsupported KEX method failure, the default client may retry with Git OpenSSH installed under standard Program Files. An explicitly configured custom client is not replaced. If no compatible client exists, the error details remain available; install a trusted updated client or configure GOFLOW_SSH_KEYSCAN_CMD. Server algorithms and fingerprint verification remain enforced. See the [Win32-OpenSSH issue](https://github.com/PowerShell/Win32-OpenSSH/issues/2140).

Temporary Windows identity directories use a protected ACL granting only the current user access. POSIX uses 0700/0600. Permission failures stop execution; chmod alone does not restrict inherited Windows access.

The SSH MCP environment allowlist includes USERNAME, USERDOMAIN, ProgramData, ProgramFiles, SystemRoot and USERPROFILE. Filtering these Windows runtime dependencies may terminate the client before diagnostics or hide a compatible client. A successful terminal command does not replace testing through the platform MCP manager.

Each SSH MCP process caches at most 64 public keys that already matched the pin, separated by address, port and fingerprint, for two minutes. Explicit key scans still contact the host. The host_key_cached field reports reuse, while every login still uses strict public-key verification. Changing the pin cannot reuse the old entry.

Repeated unauthenticated scans may trigger OpenSSH [PerSourcePenalties](https://man.openbsd.org/sshd_config#PerSourcePenalties). Reduce redundant client probes instead of disabling server protection.

## Acceptance limits

Successful connection, allowlist enforcement and mismatched-pin rejection prove tool behavior. Real model reports, professional operations and security assessment, and external notification delivery need separate evidence. Read-only diagnostics do not prove vulnerability coverage or host safety. Local stored events are not external delivered notifications.

## Recovery and change evidence

Input, approval and continuation transitions preserve the target from the durable run. They must not fall back to local or inherit another task's target. Select a different target by starting a new task. The live VM case exercised scope-gate recovery after restart without relaxing target checks.

Provide an index and separate raw logs for large evidence sets, then read bounded ranges. Keep complete hashes in the evidence index and check report values exactly. A local channel alone is not a stored event: without an executed notification node and an actual receipt, report that no notification was triggered or event created. Findings first raised during investigation have a separate notification branch; an initial alert request is not sent twice, and delivery failure still blocks the quality gate.

A workflow interrupted while waiting for model output can restore a continuable pause when no tool operation has an unknown outcome. Continuation preserves the run ID and completed stages, even before partial model text exists. Unknown tool effects are not treated as replayable provider waits.

Known Chinese report headings have display aliases; business values retain their declared validation contract. A patch with identical before/after content hashes and permissions does not count as a new file change; actual permission changes still count. An explicitly allowed go_race_unavailable fallback requires observed toolchain limitations and a passing ordinary Go test. It does not excuse actual test failures or claim the race detector passed.
