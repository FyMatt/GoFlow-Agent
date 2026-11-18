# Resources

GoFlow V3 exposes six resource types: agents, subagents, workflows, Skills, individual MCP tools, and MCP connections. Individual tools can be inserted into Workflow Studio directly; Settings manages the single default model provider separately.

## Resource Principles

- Ordinary names and descriptions are Chinese.
- Forms show only intent, permissions, and enabled state.
- Built-ins are ready to run and share deterministic tools.
- Simple tasks run directly; complex tasks select a bounded workflow.
- Files, Diff, tests, checks, evidence, and document validation prove completion.

## Agents And Subagents

The default task agent routes work to narrow specialists for software engineering, network security, network operations, internet research, and Office/WPS documents. Subagents are used only for independent bounded work; they do not debate or create plans without artifacts.

## Workflows

Workflows are internal execution graphs with explicit inputs, stages, outputs, approvals, checkpoints, recovery, and quality gates. The canvas remains editable, but ordinary task execution does not require designing a workflow.

## Skills

Skills follow common `SKILL.md` conventions and can coexist with `AGENTS.md` and `CLAUDE.md`. A Skill supplies instructions, declared scripts, references, and acceptance checks. Scripts still pass through MCP permissions and native execution policy.

Relevant host Skills include `local-host-diagnostics`, `managed-local-config`, and `remote-host-diagnostics`. Host-facing connections use `target_scope: host`.

## MCP Connections

MCP connections use standard stdio JSON-RPC and fixed command allowlists. Third-party connections use `auto` or `native_strong`, operate on task copies, and cannot write the real workspace directly.

## Built-In Kits

Kits are curated defaults, not another user resource type. They can materialize connected resources when customization is required.

- `agent-framework-kit`: production-ready framework extension path; use `materialize` to create editable connected resources.
- `security-research-kit`: production-ready authorized offensive security workflow with explicit scope; disallowed exploitation is rejected.
- `binary-analysis-kit`: production-ready static evidence workflow; unsafe dynamic analysis is outside the default boundary.
- `customer-support-kit`: production-ready support workflow with privacy flags and reviewer quorum.
- `multi-domain-agent-kit`: production-ready routing with branch pruning and explicit join conflict handling.
- `internet-research-kit`: research agent plus source verifier, claim-to-source evidence, and Markdown, JSON, CSV delivery.

## Documents

The documentation specialist can read existing Office/WPS files, create new files, modify templates, interpret requested edits, preserve formatting during precise changes, merge documents, validate structure, and export PDF. Other domains use it for requirements, security reports, operation runbooks, and research packages.

## Lifecycle

Create, edit, validate, enable, use, and delete resources from the resource page. Replaced legacy files and uncalled compatibility code are removed after the new path passes acceptance; resources are not retained merely because they might be useful later.

See [MCP](./mcp.md), [scaffolds](./scaffolds.md), and [configuration](./configuration.md).
