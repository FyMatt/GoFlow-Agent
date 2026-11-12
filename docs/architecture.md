# Architecture

GoFlow V3 is a local agent workflow platform for Windows 10/11 x64 desktop Web. It combines a task workbench, deterministic Go orchestration, model reasoning, native controlled execution, and verifiable delivery. Authorized Linux hosts connect through target resources and SSH tools.

## Request Flow

```text
user input + screenshots + files
  -> requirement compiler
  -> direct task or bounded workflow router
  -> context retrieval and repository instructions
  -> persistent state machine
  -> MCP tool broker and inline approval
  -> workspace snapshots and checkpoints
  -> quality gate
  -> artifacts, Diff, evidence, apply or recovery
```

Simple tasks execute directly. Complex tasks select only necessary workflow stages. Model calls without a useful result, duplicate tool calls, and discussion-only subagent work are rejected or deduplicated.

## Product Surface

The primary surface is `/console`: task input, screenshot paste, file drop, inline approval, progress, artifacts, Diff, and recovery. Resource management exposes agents, subagents, workflows, Skills, and MCP connections. Settings keeps the default OpenAI-compatible Provider setup to one API Key field.

The workflow canvas edits the same YAML graphs used by the runtime. Visual position metadata does not affect execution. The frontend is embedded in the Go binary and does not require a separate web build at runtime. Static assets (HTML, CSS, JavaScript) are served with gzip compression and ETag-based conditional requests, reducing first-paint transfer from ~1.2 MB to ~230 KB.

## Execution Core

The persistent state machine records ordinary tasks and workflow runs. Checkpoints, approvals, tool results, externalized large arguments, artifacts, and recovery state survive restart. Before the first write, GoFlow stores a read-only recovery snapshot outside the workspace. Approved tools then edit, build, and test in the real project directory, so project-local environments and dependencies remain available. A completed change can be saved as a workspace version; the latest three versions are retained, and rollback verifies current file hashes before restoring anything.

## Native Security

Windows uses AppContainer where available, restricted tokens, low-integrity directories, NTFS ACL leases, and Job Objects. Unknown code and third-party MCP connections fail closed when the required boundary is unavailable. Linux isolation code remains compatibility source outside the current release target.

## Context And Performance

Repository maps, incremental indexing, instruction discovery, context deduplication, prompt compaction, structured log summaries, browser action caching, idempotent tool calls, and candidate-fix verification reduce Token use and latency.

## Delivery

A response is not completion. Delivery requires real files, Diff, build/test/lint output, source evidence, post-change checks, or document validation. The manifest records artifacts, verification, limits, and recovery actions.

See [configuration](./configuration.md), [MCP](./mcp.md), and [workflows](./workflows.md).

## Current architecture notes (2026-11-10)

- **Domains live only in kits**: the kernel keeps no domain keyword table and no
  domain→agent/workflow map. Kit `routing` contracts decide the entry; unmatched
  focused tasks fall back to the active agent and unmatched complex tasks ask the
  user to choose.
- **Per-stage isolated sessions**: each stage rebuilds its prompt and relies on a
  stable prefix for cache hits. Measured hits of 70-76% are the practical ceiling
  of this shape; the remaining cost is new content per turn.
- **Bounded tool output at the source**: `read_file` returns at most 300 lines with
  `next_offset`; `read_files` caps each file at 200 lines; `fetch_url` inlines at
  most 4000 characters; command output spills to an artifact reference.
- **Locate first**: `read_file_outline` returns declaration line numbers and
  `search_files` returns match ranges via `context_lines`.
- **Read reuse**: stage prompts list files earlier stages already read; repeated
  reads in one conversation collapse to a stub.
- **Cost visibility**: provider `pricing` makes the per-run estimate computable and
  the run detail shows it.
