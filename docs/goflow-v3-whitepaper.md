# GoFlow V3 Local Agent Workflow Platform Whitepaper

Version: V3

Release date: 2026-08-17

Product type: local agent workflow platform

Current product scope: Windows 10/11 x64 desktop Web. Compatibility source and historical Linux evidence are not release requirements.

See [local operations](./local-operations.md) for daily operation, troubleshooting and safe cleanup. Historical design and acceptance evidence do not replace evaluation of the current task.

## Executive summary

GoFlow V3 connects natural-language requests, real files, controlled tools, durable state, and independent verification into one delivery loop. A normal user selects a workspace, describes a goal, attaches screenshots or files when useful, handles approval in the current task, and reviews evidence before applying or downloading the result.

“Local” means that runtime orchestration, workspace state, task staging, secret references, tool proxies, and delivery audit remain on the machine. A model Provider may be remote when configured by the user. GoFlow never treats model prose as completion evidence: a task must produce the required files, Diff, build/test results, citations, document checks, or health checks before delivery.

V3 therefore makes four deliberate choices: simple tasks run directly; complex tasks use durable workflows; the UI exposes a small resource model; and deterministic code enforces paths, permissions, state, budgets, retries, hashes, and quality gates while the model handles interpretation and candidate generation.

## 1. Purpose and scope

This document is the product, architecture, usage, and acceptance contract for V3. It explains the problem boundary, the resource model, the task lifecycle, the five supported domain practices, the native security model, and the evidence required for delivery.

The [Chinese version](./goflow-v3-whitepaper.zh-CN.md) is the authoritative detailed usage reference for Chinese-speaking users. The [quick user guide](./user-whitepaper.md) is the short operational companion.

## 2. Product definition

GoFlow V3 contains four cooperating layers:

| Layer | Responsibility | User-visible result |
| --- | --- | --- |
| Task workbench | Input, attachments, status, approval, and artifacts | One place to run and review a task |
| Agent runtime | Requirement compilation, routing, retrieval, tools, and synthesis | Automatic selection of internal capabilities |
| Workflow orchestrator | Stages, routing, parallel work, checkpoints, recovery, and quality gates | Repeatable complex delivery |
| Controlled execution | Workspace, MCP, commands, host, and remote boundaries | Auditable, bounded tool actions |

GoFlow is not a general-purpose desktop automation product, a multi-tenant service, or a replacement for authorization and change management. It does not use Docker, Podman, or a virtual machine as a product runtime.

## 3. Operating principles

1. **Task first.** Resources are implementation details; a normal user starts from Tasks.
2. **Results first.** The UI highlights state, the next action, approvals, conclusions, and artifacts. Detailed events load on demand.
3. **Simple configuration.** Users manage Agent, Subagent, Workflow, Skill, and Connection. Operating-system sandbox parameters stay internal.
4. **Deterministic boundaries.** Code enforces paths, permissions, approval, state, timeouts, hashes, and validation. A model cannot mark a stage successful by itself.
5. **Evidence-based delivery.** A prose response without a file, Diff, test, citation, document check, or health check is a draft, not a completed delivery.

## 4. System model

```mermaid
flowchart LR
    A[User goal and attachments] --> B[Requirement compiler]
    B --> C[Task router]
    C --> D[Context retrieval and deduplication]
    D --> E[Durable execution state machine]
    E --> F[Tool proxy and native boundary]
    F --> G[Checkpoint, approval, recovery]
    G --> H[Quality gate and delivery manifest]
    H --> I[Download, apply, or recover]
```

Each run forms a compact task contract containing a goal, scope, deliverables, acceptance checks, authorized risk, and recovery expectations. Simple requests use a direct loop. Complex requests are split into bounded stages; subagents receive a narrow input and must return a structured result instead of an open-ended discussion.

## 5. Resource and compatibility model

### 5.1 Six resource types

- **Agent:** a stable role, decision boundary, and delivery style.
- **Subagent:** an independent bounded task with a defined output and stop condition.
- **Workflow:** stages, routing, approvals, checkpoints, and quality gates.
- **Skill:** reusable methods, references, scripts, and acceptance rules.
- **MCP tool:** one schema-defined capability that can be connected between workflow agents.
- **Connection:** an MCP or external capability entry point.

Kit packages compose resources but do not create another user-facing resource type. Built-in resources are preferred for ordinary users.

### 5.2 Compatibility

V3 can import common `SKILL.md`, `AGENTS.md`, `CLAUDE.md`, and standard stdio MCP resources. Compatibility maps understood fields; it does not grant external resources unrestricted permissions. Unsupported fields must be reported, and the workspace, approval, audit, and quality policies remain authoritative.

## 6. User operating method

### 6.1 Installation

Use the Windows x64 archive, verify `SHA256SUMS`, extract it to a user-writable application directory, and start `run-goflow.cmd`. Open `http://127.0.0.1:8080/console`; Workflow Studio is at `/workflows`.

### 6.2 First setup

Open Settings and configure the model service, then select the actual project or document directory as the workspace. The active model and reasoning effort follow the current provider configuration. Credentials are stored through local secret references.

### 6.3 Task input

Use the five-part request format:

```text
Goal:
Scope:
Deliverables:
Acceptance:
Restrictions and approvals:
```

The workbench accepts pasted screenshots, dragged images/files, selected attachments, and `@relative/path` references. Attachments are hashed and tied to the run. If the configured Provider cannot process vision, GoFlow reports the capability boundary instead of fabricating visual understanding.

### 6.4 Approval and delivery

Read-only checks normally proceed directly. File writes, commands, network, host, and remote operations may pause for approval. Approval is inline, run-scoped, and stage-scoped. Before applying a change, inspect the real file, Diff, executed checks, artifacts, risks, and recovery identifier.

### 6.5 Recovery

Runs and workflow state are persisted. A closed browser or restarted service does not discard a saved run. Resume from the latest checkpoint and avoid re-running stages that already have evidence. If another process changed the workspace, re-check the Diff before continuing.

## 7. Five domain practices

### Software engineering

The standard loop is repository map, requirement localization, minimal change, build, tests, static checks, Diff review, and delivery notes. Minimum evidence is a real Diff plus the required build and verification output.

### Cybersecurity

Start with authorized scope, target, time window, allowed actions, and prohibited actions. Follow with passive collection, evidence, risk, remediation, retest, and report. Active or side-effecting actions require a bounded approval.

### Network operations

Start read-only. A configuration or remote change should include backup, impact analysis, dry run, approval, execution, health check, and rollback verification.

### Internet research

Specify the topic, time cutoff, preferred primary sources, citation format, and conflict handling. Deliver source URLs, publication/access dates, quotations, uncertainty, and reusable Markdown/JSON/CSV output.

### Office/WPS documents

The document capability reads existing files, creates new files, edits templates, makes precise format-preserving changes, merges documents, validates structure, and exports PDF. The request should state the source document, exact changes, formatting that must remain, and output formats.

## 8. Workflow design and governance

Use a workflow when a task has fixed stages, role handoffs, branching/parallel work, human approval, checkpoints, or repeat value. A minimal graph is:

```text
Start -> Analyze -> Implement/Process -> Verify -> Quality gate -> Deliver -> End
```

An execution node initially needs only actor, Skill, input, output, and next step. Add branches, retry, approval, artifacts, and acceptance criteria only when they have a real purpose. A valid workflow has a start and end, explicit inputs/outputs, safe failure paths, bounded retries, delivery evidence, and recovery checkpoints. Avoid subagent discussions or model calls with no independent artifact.

## 9. Native security model

The supported desktop host uses Windows AppContainer, restricted tokens, ACLs, and Job Objects where available. Linux servers and VMs are remote targets accessed through authorized tools; GoFlow is not deployed there. Unknown code and third-party MCP tools run in a task copy with controlled read-only dependencies. The default task policy prevents writes outside the task workspace.

Tasks are network-disabled by default; network access is mediated by an approved tool. API keys, SSH private keys, and sensitive environment variables are referenced locally and must not be written to resources, logs, reports, artifacts, or commits. If a required strong boundary is not available, unknown code is rejected with a clear reason rather than silently downgraded.

## 10. Engineering quality model

The completion contract is domain-specific:

| Domain | Minimum delivery evidence |
| --- | --- |
| Engineering | Diff, build, tests, static checks, and run instructions |
| Security | Authorization, findings, raw evidence, risk, remediation, retest, report |
| Operations | Target, diagnosis, change record, health check, recovery plan |
| Research | Sources, citation mapping, conflicts, timestamps, structured export |
| Documents | Editable source, preview/PDF, structural and format validation |

Quality gates are deterministic. They inspect existence, hashes, Diff, exit codes, test output, citation fields, document structure, and approval state. Efficiency is measured through productive model calls, context deduplication, deterministic cache hits, tool latency, and post-recovery duplication; lower token use must not mean skipped verification.

## 11. UI and data model

The Tasks page is a single main work area with inline approvals and artifacts. Recent tasks, full event detail, and recovery history open on demand. Workflow Studio is canvas-first with a compact toolbar and on-demand node library/settings. Resources expose six resource types, including individual MCP tools that can be inserted into workflows. Settings exposes the Key, workspace, appearance, and data maintenance; low-level isolation options remain internal.

Runtime home contains the application and built-in resources. Workspace root contains the user's project or documents. Data root contains sessions, secret references, backups, and recovery records. Task staging contains one run's copy, dependencies, and artifacts. Upgrading the application must not replace the workspace or data root.

## 12. Deployment lifecycle

The release lifecycle is verify archive, stop the old process, install/upgrade, start checks, verify data preservation, and roll back on failure. The native installer supports upgrade backups, rollback, data-preserving uninstall, reinstall, and explicit purge. Uninstalling the application does not automatically delete user data.

## 13. Typical deployments

1. **Screenshot-driven engineering:** paste a CI or browser error, identify the corresponding code, make the smallest change, run checks, and deliver the Diff.
2. **Authorized web security report:** collect passive evidence, perform bounded approved checks, produce a report, then retest the fix.
3. **Network service change:** diagnose, back up, dry-run, approve, apply, health-check, and verify rollback.
4. **Research package:** deduplicate sources, capture quotations and dates, mark conflicts, and export Markdown/JSON/CSV.
5. **Template document production:** preserve a DOCX template, apply precise changes, merge evidence, export DOCX/PDF, and validate layout.

## 14. Limitations and disclosure

Provider vision, rate limits, cost, and model availability are external constraints. Native isolation depends on OS and policy capabilities; unavailable strong primitives cause unknown code to be rejected. Office formatting depends on input structure and the available local document runtime. Security and remote operations still require authorization and human review. External sources can change or conflict and must retain timestamps. Models can be wrong; tools and quality gates are the final authority.

## 15. Acceptance baseline

The V3 acceptance baseline covers Windows amd64 release and lifecycle tests, native malicious fixtures, Python/Node MCP protocol boundaries, domain delivery evidence, real browser review for desktop and light/dark states, full Go/Python/security regression, resource links, Chinese localization, deployment assets, and secret scanning. Linux compatibility code and mobile layouts are outside the product acceptance scope.

Evidence is kept under the project `.artifacts/` directory. The current implementation baseline is documented in [PROJECT_IMPLEMENTATION_PLAN.zh-CN.md](../PROJECT_IMPLEMENTATION_PLAN.zh-CN.md), and release inventory is in [V3_RELEASE_CANDIDATE_INVENTORY.zh-CN.md](../V3_RELEASE_CANDIDATE_INVENTORY.zh-CN.md).

Use the [User Acceptance and Quality Guide](./acceptance-guide.md) for real-task review.

## Appendix: request template

```text
Goal:
Scope:
Input files or screenshots:
Allowed tools and actions:
Deliverables:
Acceptance checks:
Actions requiring approval:
Objects that must not be touched:
Recovery requirement:
```

Further reading: [quick user guide](./user-whitepaper.md), [installation](./install.md), [configuration](./configuration.md), [resources](./resources.md), [workflow graphs](./workflows.md), [skills](./skills.md), [MCP](./mcp.md), [native MCP isolation](./mcp-isolation.md), [architecture](./architecture.md), and [release verification](./release.md).
