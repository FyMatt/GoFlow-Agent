# Windows local operations

This guide covers the Windows 10/11 x64 desktop Web product. Read it locally without GitHub, Ubuntu or a VM. See the [documentation index](./README.md).

## Start and stop

From a source checkout with Go installed:

```powershell
go build -o goflow.exe ./cmd/goflow
.\goflow.exe --config .\configs\goflow.yaml --workspace D:\Projects\example --http 127.0.0.1:8080
```

Release archives use their bundled launcher or `configs/goflow.binary.yaml`. Open <http://127.0.0.1:8080/console>. Use Ctrl+C for a foreground server and verify that no task is active before restarting.

Inspect a listener's owning PID and command line before stopping it. Never terminate every Python or Go process. A virtual-environment Python launcher and child may represent one service, not two. Use `process_status` and `stop_process` for managed previews; stop only processes owned by that verification pass unless the user explicitly requests broader shutdown.

## Evaluate a task

- Inspect the current stage, provider connection/retry state, latest tool result and timestamp. An upstream overload or interrupted stream is not itself a local loop.
- Continue resumes the saved run; retry creates a new attempt. Pinned historical resources do not automatically acquire newer template changes.
- Completed stages and passing pre-existing smoke tests do not prove the requested feature was delivered. Require actual file changes, requirement coverage and checks after the last modification.
- Web requests need evidence for the requested layout, animation, content and pages. Editing seed data alone does not populate an existing database.
- Run totals, the execution overview and usage charts should agree with the durable model usage ledger. Cache hit rate is cached input divided by input; cached input is not added to total tokens again.
- Historical model reports can contain mistakes. A display correction or new independent check does not turn an old failed run into a successful rerun.

## Python and patches

Use the existing project virtual environment and actual unittest/pytest runner. A missing system dependency is not evidence that the project environment is broken. A SHA mismatch differs from zero text matches. On an unmatched exact patch, reread the smallest relevant range and use its exact unique text; do not guess or send the same failed edit through another tool.

Review stages should inspect supplied execution evidence instead of inventing another test suite. Unrelated pre-existing risks belong in advisory notes, not unrequested implementation work.

For command tools, put only the executable in `program` and individual subcommands/options in `args`, for example `{"program":"go","args":["test","./..."]}`. Keep actual executable paths containing spaces intact, without adding shell quotes. The runtime repairs mixed fields only for clearly recognized development checks; quoted values, shell syntax, unknown programs and destructive operations are not split automatically. A malformed program field does not mean Go is missing, and shell wrappers do not bypass tool constraints.

Delivery checks inspect the complete persisted command payload, including its executable, arguments and exit status. Truncated display summaries do not replace full verification evidence, including for externalized logs; a missing exit status in a summary must not conceal a failed execution.

## Preserve data while cleaning

Default source deployments may store the session in the workspace's `.goflow/session.json`. A separate `GOFLOW_DATA_ROOT` changes persistent resource, secret and per-workspace data locations. Inspect the actual configuration.

Keep source, tests, build/acceptance scripts, configuration, skills, workflows and documentation. Do not treat `.git/`, secrets, sessions, task staging, workspace versions or backups as disposable clutter. Clean unused isolated Go caches and browser profiles only after verifying they are not in use. Preserve screenshots and reports separately from browser caches. Check hashes and rollback requirements before removing duplicate builds.

Use exact validated paths and prefer the Windows recycle bin. Do not remove project databases, virtual environments or user files to reduce the platform's size.

## Acceptance boundary

Automated regression validates mechanisms, not every provider response or complex task outcome. A new real task and human acceptance remain necessary. Updating the local executable does not update an older `dist/` archive or publish a GitHub release.
