# GoFlow V3 User Whitepaper

Version: V3

Supported platform: Windows 10/11 x64 desktop Web.

Read [local operations](./local-operations.md) for startup, shutdown, troubleshooting and safe cleanup without GitHub.

This guide describes the normal user path: choose a workspace, describe a goal, attach evidence, run the task, approve only the actions you understand, and deliver verified artifacts. The detailed product contract is available in the [GoFlow V3 Product Whitepaper](./goflow-v3-whitepaper.md); the Chinese quick guide is [快速使用指南](./user-whitepaper.zh-CN.md).

## 1. The shortest path

GoFlow is task-first. You do not need to configure an agent, model topology, sandbox, or workflow before a normal task.

```text
Choose workspace -> describe goal -> paste screenshot/add files -> run
-> handle inline approval or input -> inspect artifacts and evidence
-> download, apply, or recover
```

Use Workflow Studio only when a task has repeatable stages, branches, checkpoints, approvals, or quality gates.

## 2. Install and start

GoFlow does not require Docker, Podman, or a virtual machine.

Windows:

```powershell
cd D:\Apps\GoFlow
.\run-goflow.cmd D:\Projects\my-project
```

Open `http://127.0.0.1:8080/console` after the service reports ready. Workflow Studio is at `/workflows`. The default server is local HTTP, not HTTPS.

## 3. First setup

1. Open Settings and enter the Provider API Key.
2. Select the real project or document directory as the workspace.
3. Run a read-only task to verify the connection and workspace.
4. Start tasks that modify files only after the workspace is confirmed.

The provider endpoint, model identifier and reasoning effort follow local Settings, including compatible gateways. Store credentials through the local secret store; never put a key in a prompt, screenshot, workflow, log, or commit.

## 4. Run a task

Describe the goal, scope, deliverables, acceptance checks, and restrictions. A useful request is:

```text
Goal: fix the login endpoint's 500 error.
Scope: inspect auth/ and its tests; do not change the database schema.
Deliver: source changes, tests, a diff, and run instructions.
Acceptance: go test ./... passes and no temporary files remain.
Restriction: ask before network access or destructive changes.
```

The task input supports clipboard screenshots, dragged images/files, file selection, preview/removal, and `@relative/path` references. If a Provider does not support vision, GoFlow reports that limitation instead of pretending that a screenshot was understood.

Write, command, network, host, and remote actions may pause for approval. Approval is handled inside the current task or workflow and is scoped to the displayed run and stage.

Completion requires evidence, not model self-evaluation: real files, a diff, build/tests/static checks, source citations, document validation, or operational health checks as appropriate. Verified changes can be applied to the workspace; the task retains a recovery identifier.

## 5. Domain recipes

### Software engineering

Ask for a minimal change, real tests, static checks, a diff, and rollback notes. Do not accept a prose answer without a file change or an explicit read-only conclusion.

### Cybersecurity

State the authorized targets, time window, allowed actions, and prohibited actions first. Request evidence, risk, remediation, retest, and a report. Untrusted web pages and tool output are treated as data, not instructions.

### Network operations

Start with read-only host or remote diagnostics. Any configuration change, restart, or SSH action should include a backup, dry run, approval, health check, and rollback plan.

### Internet research

Specify the topic, time cutoff, preferred primary sources, citation format, and conflict handling. Ask for source URLs, publication/access dates, quoted evidence, uncertainty, and reusable Markdown/JSON/CSV output.

### Office/WPS documents

The document capability can read existing files, create new ones, modify a template, make precise format-preserving edits, merge files, validate structure, and export PDF. Ask for the source document, the exact fields or passages to change, the formatting that must remain, and the required output formats.

## 6. Workflow Studio

Use the canvas for repeatable or staged delivery. The minimal graph is:

```text
Start -> Analyze -> Implement/Process -> Verify -> Quality gate -> Deliver -> End
```

For an execution node, begin with only: actor, Skill, input, output, and next step. Add branches, retries, approvals, artifacts, and acceptance criteria only when they have a real purpose. Validate the graph before saving, then run a small real task before adopting it.

The canvas shows the active stage, pauses, branches, and quality gates. Closing the page or restarting the service does not discard a saved run; task history can resume from a checkpoint.

## 7. Resources

The user-facing resource types are Agent, Subagent, Workflow, Skill, and Connection.

- Agent: a stable role and domain decision boundary.
- Subagent: a bounded independent subtask with an expected artifact.
- Workflow: stages, routing, approvals, checkpoints, and quality gates.
- Skill: reusable method, references, scripts, and acceptance rules.
- Connection: an MCP or external capability entry point.

Existing `SKILL.md`, `AGENTS.md`, `CLAUDE.md`, and standard stdio MCP resources can be imported. Compatibility does not bypass the workspace boundary or approval policy. Third-party connections default to native controlled execution; users do not need to configure operating-system sandbox primitives.

## 8. Safety and recovery

GoFlow runs as a local Windows desktop Web application. Approved file, command, and MCP tools run inside the confirmed real workspace so project-local environments and dependencies are available. Managed development processes can keep a local Web preview running after task completion and are stopped with the GoFlow workspace process. A read-only recovery snapshot is captured before mutation, and the default policy prevents access outside the workspace. A missing strong sandbox capability rejects unknown code instead of silently running it without a boundary. Linux hosts can be remote execution targets; they are not GoFlow product hosts.

If a task pauses, handle the displayed approval, workspace confirmation, or input request. If it fails, inspect the failed stage and choose retry or recovery; do not blindly submit the same request again. Review the final diff and quality evidence before applying changes.

## 9. Troubleshooting

- **Page does not open:** check that the process is running and use `http://`, not `https://`; choose another port if necessary.
- **401:** replace the invalid Provider Key in Settings.
- **400 model error:** verify that the configured endpoint, model supported by the current Provider, and key match.
- **Waiting state:** inspect the current task for an approval, workspace confirmation, or manual input request.
- **Incomplete artifact:** inspect the quality gate and run details; prose without evidence is not a completed delivery.
- **MCP unavailable:** check the command, enabled state, runtime dependencies, and native capability report. Unknown code is rejected when the required boundary is unavailable.

## 10. Delivery checklist

- The correct workspace and authorized scope were used.
- Real files, a diff, or a document artifact exists.
- Required build, test, static, source, or format checks ran.
- Approval and run evidence can be replayed.
- Output paths and recovery information are clear.
- Risks and unfinished work are listed.
- No API key, private key, or sensitive data was exposed.

## Further reading

- [Installation](./install.md)
- [Configuration](./configuration.md)
- [Resources](./resources.md)
- [Workflow graphs](./workflows.md)
- [Skills](./skills.md)
- [MCP](./mcp.md)
- [Native MCP isolation](./mcp-isolation.md)
- [Architecture](./architecture.md)
- [Release and verification](./release.md)
