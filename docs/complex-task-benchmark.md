# GoFlow V3.1 Complex Task Engineering Acceptance Baseline

This baseline answers one question: can GoFlow repeatedly complete complex work under real constraints, rather than merely finish fixed small fixtures?

## 1. Complex Task Definition

A task enters a complex workflow only when it satisfies at least three of these four conditions:

- It spans at least three modules, services, hosts, source sets, or document formats.
- It requires several usable deliverables, such as source code, tests, a release package, a report, and editable documents.
- It includes approval, backup, migration, rollback, recovery, authorization scope, or sensitive-data boundaries.
- It requires at least two independent verification methods, such as tests, builds, static analysis, browser review, source cross-checking, or document validation.

Words such as "complex", "complete implementation", or "high quality" are not evidence of complexity. A request that needs one read, one edit, and one test remains a direct task.

## 2. Acceptance Scale

- Development, security, and platform fixtures contain at least 100 version-controlled files and three dependent modules.
- The expected change is 6 to 30 files. A smaller correct patch must not be padded with unrelated changes.
- Every task has public acceptance criteria and independent hidden checks.
- Every complex task injects at least one recoverable failure, such as a temporary provider error, failed verification, terminated process, unavailable remote target, or failed document conversion.
- Each task runs three times from the same clean baseline. A hard failure resets that domain's consecutive-pass count.

## 3. Six Real Task Groups

### 3.1 Repository-Level Software Delivery

The repository contains an API, persistence, cache, command-line interface, and browser UI. The task migrates a configuration version, implements concurrency-safe TTL behavior, preserves old configuration compatibility, updates UI state, and writes upgrade guidance.

Required evidence includes the source Diff, migration implementation, regression tests, build, static checks, browser review, upgrade notes, and rollback steps. Hidden checks cover concurrency, expiration, old-version upgrade, restart recovery, and untouched modules.

### 3.2 Defensive Security Engineering

The input is an explicitly authorized local multi-module application with authentication, authorization, input-validation, file-boundary, and dependency risks. The system performs a read-only audit first, repairs evidence-backed findings after approval, and then runs an independent retest.

Required evidence includes authorization scope, finding-to-evidence mapping, minimal fixes, public and hidden regression tests, unresolved items, a retest report, and one security delivery. Active probing belongs only in the separate controlled red-team workflow.

### 3.3 Multi-Target Network Operations

The input contains at least five authorized test targets or equivalent native test processes. The task performs parallel read-only diagnosis, a canary change, batched rollout, health checks, a third-batch failure injection, stop-propagation, and precise rollback.

Required evidence includes the target inventory, before-and-after state, approval, backups, per-target results, failure boundary, rollback result, and final runbook. A failed target must not cause already verified unaffected targets to run again.

### 3.4 Internet Source Verification

The topic includes at least 15 candidate sources, including reposts, stale pages, same-domain sources, and conflicting official or primary materials.

Required outputs include source snapshots, deduplication results, atomic fact mapping, conflict resolution, unresolved claims, complete URLs, publication dates, access times, and consistent Markdown, JSON, and CSV. Hidden acceptance removes a source or changes the time boundary; the system must decide again instead of reusing a stale conclusion.

### 3.5 Office/WPS Combined Delivery

The input includes DOCX and PPTX templates with headers, footers, tables, styles, and masters, plus XLSX data and written modification requirements.

The result must read and modify templates precisely, preserve formatting, protect batch edits atomically, merge data, validate formulas and filters, export PDF, and produce page previews. Unspecified template content, masters, formulas, and fixed statements must remain unchanged.

### 3.6 Cross-Domain Unified Delivery

The input combines a software defect, security finding, deployment requirement, and formal-report requirement. The system selects only necessary domains and creates one unified delivery after each domain passes its own quality gate.

Evidence must prove correct branch selection, shared-context deduplication, domain failures are not hidden by other branches, approval scopes do not leak across branches, and every report conclusion traces to a real artifact or verification result.

## 4. Workflow Hard Gates

Complex tasks progress through this state sequence:

```text
requirement compilation -> repository or target map -> change slices -> risk and approval
-> slice implementation -> integration verification -> independent review -> bounded repair
-> final retest -> write-back -> unified delivery
```

- Requirement compilation records goals, scope, constraints, deliverables, acceptance criteria, and stop conditions.
- Independent read-only exploration may run in parallel. Writes follow dependency order.
- Every change slice records input hashes, affected files, prerequisites, and local verification.
- A completed slice with unchanged inputs must not run again.
- Repair addresses only explicit blockers and is bounded to one cycle. A remaining failure produces a blocked delivery, not an infinite loop.
- Success is determined only by real files, Diffs, commands, sources, document structures, or browser evidence.

## 5. Tool Robustness Gates

- File search uses the incremental index and repository map first, with a bounded full-text fallback.
- The same file hash is never injected in full more than once; later stages use summaries, symbols, and artifact references.
- Writes use SHA-256 preconditions, atomic publication, and a recoverable journal.
- Commands execute as argument arrays without model-built shell strings. Timeout and cancellation terminate the process tree.
- Test, build, and static-analysis commands are deduplicated by command fingerprint. Passing evidence is reused only when inputs are unchanged.
- Network and research tools retain the URL, time, hash, redirects, and source domain. Cache validity depends on the cutoff date and content hash.
- Office tools retain the source hash, backup, modification list, structural validation, converter details, and page-preview evidence.

## 6. Quality and Efficiency Metrics

Every run must satisfy all of these conditions:

- Requirement coverage is 100 percent and the Diff stays inside the allowed scope.
- All hard acceptance commands pass. Infrastructure limits are explicit and cannot hide functional failure.
- Deliverables can be run, installed, opened, or submitted directly.
- There is no model self-grading, simulated log, fabricated source, or nonexistent file.
- Model calls with no resulting artifact remain below 5 percent.
- Repeated tool calls with identical arguments are zero; repeated full reads of the same file hash are zero.
- Recovery does not repeat already completed side effects.
- Each domain passes three consecutive runs and receives a human score of at least 8 out of 10 each time.

## 7. Evidence Retention

Formal acceptance evidence must not exist only under `.artifacts`, `.tmp`, or a build cache. Every run retains this structure in the operator-selected acceptance workspace:

```text
acceptance-evidence/<domain>/<task-id>/<run>/
  request.txt
  task-contract.json
  timeline.json
  approvals.json
  tool-summary.json
  diff.patch
  verification.json
  quality-score.json
  deliverables/
  report.json
```

Caches may be deleted. Acceptance evidence requires explicit user deletion. The report records the provider, model, task ID, baseline hash, timing, token use, tool timing, recovery, final artifact hashes, and independent retest result, but never API keys, private keys, or sensitive file bodies.
