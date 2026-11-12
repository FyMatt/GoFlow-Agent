# Local resource extension guide

For local administrators and resource developers. This documents current mechanisms, not production certification of the first packs. [中文](./resource-extension-guide.zh-CN.md)

Source-only references: inline `internal/...` paths require a source checkout; release packages omit these files.

## Choose the extension layer

Add Agents, Skills, Workflows, templates and a Kit when existing tools suffice. Implement or configure an MCP adapter when execution capability is missing. YAML declares dependencies and behavior; it creates no tools and grants no authority. Agent, Skill, stage, user and approval boundaries still apply.

Actual packs: [software](../kits/software-engineering-kit/kit.yaml), [operations](../kits/operations-runbook-kit/kit.yaml), [security](../kits/security-research-kit/kit.yaml). See also [workflow authoring](./workflows.md).

## Routing v1

This Kit fragment selects a workflow already listed in the Kit:

```yaml
workflows: [field-notes-review]
routing:
  version: 1
  enabled: true
  priority: 10
  match_terms: [田野笔记整理, field notes review]
  entry_workflow: field-notes-review
  required_tools: [file_tools/read_file, file_tools/write_file]
```

- Only version `1` is supported. Priority ranges from `-100` to `100`, default `0`. A present routing contract defaults to enabled; `enabled: false` excludes it from automatic routing without uninstalling its resources.
- Matching is case-insensitive substring matching against any nonempty term, not semantic ranking or longest-match scoring.
- Declare exactly one of `entry_agent` and `entry_workflow`, included in the Kit's corresponding resource list. Required tools use qualified `server/tool` names and declare dependencies only.
- Pack-level `tools` are library references; this entry need not have permission to invoke every library tool. Undiscovered qualified references produce warnings. Put mandatory entry capabilities in `routing.required_tools`: preflight blocks unavailable or forbidden requirements without expanding runtime permissions.
- Request automatic selection with API `type: auto`. Explicit Workflow, Agent, Skill or Tool selections remain authoritative. Omitting `type` does not request Kit routing.
- One match at the highest priority wins, then normal dependency, environment, tool, target and approval preflight applies. A highest-priority tie returns `kit_routing_ambiguous`. Enabled contracts with no match return `kit_routing_no_match`. Refine terms or select an explicit entry; neither case silently becomes a software task.
- The legacy local recommender applies only when no routing contracts are enabled. Kits without routing do not participate. Dependency failure in the selected pack does not fall through to a lower-priority pack.

Use `POST /api/runs/preflight` with `{"type":"auto","input":"field notes review: read input/notes.txt"}` and inspect `target`, `issues` and `resource_pins`. Passing preflight is neither execution approval nor content validation. Sources: contract (`internal/scaffold/kit_routing.go`), selection and dependencies (`internal/api/kit_routing.go`), tests (`internal/api/kit_routing_test.go`).

## Kit layers and administration

Read precedence for the same Kit name is highest first:

| Layer | Location |
| --- | --- |
| Local save/import override | `rootData/kits/<name>/kit.yaml` |
| Data resource override | `Data/resources/kits/<name>/kit.yaml` |
| Runtime distribution | `runtime/kits/<name>/kit.yaml` |

Here `rootData` and `Data` both denote the effective `DataRoot`; `runtime` is `RuntimeHome`, not the workspace. Identical roots are deduplicated. `kit.yml` and flat `<name>.yaml` / `<name>.yml` forms remain compatible. Overrides replace the entire same-name Kit. Malformed or escaping overrides do not reveal a lower copy. Deletion tombstones hide lower copies; an explicit save restores visibility. Inspect the effective resource `path` instead of assuming the distribution copy is active.

Layer implementation (`internal/api/kit_layers.go`), layer tests (`internal/api/kit_layers_test.go`), and root resolution (`internal/api/resources.go`) cover this behavior. Other resource families have their own storage rules.

1. Install dependencies into a test runtime first; configure Provider and MCP access separately, without copying credentials. Reload or restart when new modules require it.
2. Inspect `GET /api/resources/kits` and `GET /api/resources/kits/<name>`. Validate submitted YAML/JSON with `POST /api/resources/kits/validate`, or an existing Kit with `GET /api/resources/kits/<name>/validate`.
3. Save the complete manifest with `PUT /api/resources/kits/<name>`. Keep `routing.enabled: false` during preparation, then save it as `true` after dependencies are ready. There is no separate Kit enable endpoint; routing does not enable a disabled Provider or MCP server.
4. Export through `GET /api/resources/kits/<name>/export`; secrets are excluded by default. Import through `POST /api/resources/kits/import?overwrite=false` to preserve existing resources. Import currently defaults to overwrite when the query is omitted.
5. Inspect saved, skipped, errors, warnings and restart guidance. Import can partially succeed; it is not an atomic package transaction. Agent, Provider and MCP modules may require reload/restart. Re-run task preflight before starting a new task.

Manifest validation, dependency preflight and execution permission checks are separate. See actual API handlers (`internal/api/kits.go`).

## Pins and maturity

Automatically routed tasks record the selected Kit and related resource pins: SHA-256 identities of public configuration, including the Kit's portable origin layer. Changing the selected same-name copy can change its pin. Editing resources does not rewrite historical pins.

Relevant continuation and approval APIs reject drift with `409 resource_pin_drift`; start a new task with current resources. Pins do not restore archived resources or freeze binaries, credentials, remote systems or web content. See pin structure (`internal/session/resource_pins.go`) and integrity checks (`internal/api/resource_integrity.go`).

`vertical_pack.maturity` values `generic`, `guided`, `professional` and `production-ready` are author claims. Catalog `claimed_maturity` is separate from `verification_status: unverified`. Saving, routing and structural validation do not certify real-model execution or domain accuracy; retain independent acceptance evidence.

## Generic stage capabilities

These stage `params` work in unknown domains without workflow-name triggers:

| Parameter | Contract |
| --- | --- |
| `require_workspace_mutation: "true"` | Require this stage's execution evidence of changes; reported filenames and earlier edits are insufficient |
| `change_slice_policy` | `off`, `required`, or `if_missing`; omitted means no slice requirement |
| `seed_plan_intake: "true"` | Add explicit framework guidance, labeled `resource_helper`, not model/tool evidence |
| `approved_plan_scope: "true"` | Require stage `approval: true`; actual approval and tool policy still apply |
| `use_change_slice_scheduler: "true"` | Also require the approved-plan-scope declaration; grant no write tools |

Boolean capabilities accept strings `"true"` or `"false"`. Unconfigured stages never synthesize business reports, code, tests, Office files or web styling. Requested writes must use permitted tools; missing delivery stays incomplete or failed. See execution (`internal/agent/workflow_graph.go`) and capability regressions (`internal/agent/workflow_stage_capabilities_test.go`).

Parallel `domain_ref` / `domain` selection matches candidate stage names, `params.domain`, or comma-separated `params.domain_aliases`. A custom worker may declare `domain_aliases: field-notes-review,田野笔记` without adding core domain entries. Bundled aliases now live in resources. Custom workflows relying on implicit mappings such as software to software-worker must declare those aliases. Candidate limits and tool policies still apply; multiple matching candidates follow the normal parallel rules.

Long Skill bodies are summarized at runtime, currently to roughly 900 bytes from early headings/list items. Put essential input, authorization and tool-argument rules in a short leading list. Inspect skill_instruction_mode, skill_source_bytes and skill_injected_bytes in prompt_budget; merely editing late paragraphs does not ensure they reach the model.

A declared change_slice_policy requires an explicit goflow-change-slices array with read/write modes; test writes use write. Stage snapshots no longer infer write plans from read-only evidence or replace malformed declarations with guessed permissions. The default development planner now permits at most two contract-correction attempts, without synthesizing model output or artifacts.

Input files may be observed existing files or declared outputs of preceding write slices reachable through explicit dependencies. Unrelated/read-only producers cannot authorize missing inputs, and no read hash is invented for future outputs. Contract retries retain successful read observations once per call ID. Repeated transient transport failures remain paused; old action_failed records can continue only with saved incomplete context and a recoverable model transport error. Authentication, resource drift and missing context remain blocking conditions.

Declare `require_json_output: "true"` and `required_json_keys` for machine-checked structured output; natural-language requests to return JSON are not sufficient. Security resources require alert decisions, severity, evidence and notification receipts before routing. Acceptance `exists` distinguishes unresolved `result.xxx` references from values; `false` and `0` remain present values.

`params.output_schema_json` adds the same supported validation subset as MCP arguments, including type, properties, required, enum, array items and size constraints. It is not a complete JSON Schema implementation, does not resolve external references, and unsupported keywords cannot establish acceptance. A declaration requires object output and blocks failures by default; existing explicit contract-failure policies still apply. For example, `'{"type":"object","required":["alert_required"],"properties":{"alert_required":{"type":"boolean"}}}'` rejects null or the string "false" while accepting boolean false. Delivery receipts may similarly constrain status values. Structure validation does not establish professional correctness.

For example, only for a task that actually requires writing:

```yaml
params:
  require_verification: "true"
  verification_mode: tool
  verification_tools: file_tools/write_file
  verification_any_tools: file_tools/read_file,web_tools/research_snapshot
```

- `command` mode requires a successful executed test, build or lint check. Legacy `require_verification: "true"` without a mode retains this default.
- `tool` mode needs declared validators, not software tests. Every `verification_tools` entry is required; existing `required_tools` declarations add requirements.
- `verification_any_tools` requires explicit tool mode and a nonempty list of qualified `server/tool` names. At least one must succeed. Combining all/any lists requires both conditions.
- Evidence needs a real completed call, call ID and content or persisted evidence. Failed, denied, suspended, empty, wrong-server or narrative-only results cannot satisfy the tool requirement. Other gate failures still apply. Collection does not prove complete domain coverage.
- Validator declarations grant no tools; stage `tools` / `allowed_tools` only narrow existing permissions.

Planning and verification may use permitted `web_search`, `fetch_url` and `research_snapshot` tools from `web_tools` for requested public technical docs. Record source URL, product/version and fetch time. Honor local-only, offline and existing-evidence-only requests; never upload private logs, configuration, credentials or source code. Public-doc access authorizes no target scanning, host access or modifications.

## Unknown-domain example and first-pack limits

The [field-notes example](../examples/custom-resource-pack/README.md) provides an actual [Kit](../examples/custom-resource-pack/kits/field-notes-kit/kit.yaml), [Agent](../examples/custom-resource-pack/configs/agents/field-notes-reporter.yaml), [Skill](../examples/custom-resource-pack/skills/field-notes-review/SKILL.md) and [Workflow](../examples/custom-resource-pack/workflows/field-notes-review/workflow.yaml). It reuses file MCP tools without a core domain enumeration.

In a test workspace, supply an input and request: `field notes review: read input/notes.txt and write deliverables/notes.json; mark absent dates and units unknown, and preserve the input.` Inspect actual JSON, unchanged source hash, read/write/readback evidence, gate status and pins. The example's file-evidence gate does not independently prove factual accuracy or production quality. A deterministic local Provider proves mechanism behavior only.

The first software, operations and security packs are replaceable workflows over existing adapters. Software still needs a usable project environment and executed checks. Operations needs actual inputs, targets and change authorization. Security collection/review is not a promise of complete scanning; missing logs cannot establish no attack. Missing capabilities need adapters, and incomplete coverage stays unknown or unverified. Resources do not automatically repair, isolate or clean up targets. See [local operations](./local-operations.md) and [acceptance](./acceptance-guide.md).

From the source root, run `python scripts/validate_docs.py` for links and bilingual pairing. It does not run Providers, contact target hosts or certify packs.

## Acceptance declaration

A kit can declare how its domain is accepted, so new domains are accepted by
adding resources instead of editing harness cases:

```yaml
acceptance:
  sample_request: 在这个 Go 仓库中完成配置 v2 迁移与并发安全 TTL 缓存。
  verify_commands: [go test ./..., go vet ./..., gofmt -l .]
  deliverables: [docs/upgrade.md]
  min_quality_score: 80
```

`GET /api/kits/{name}` returns the declaration. `scripts/kit_acceptance.py`
checks a delivered workspace against it, and
`scripts/p7_provider_runner.py --kit-acceptance` attaches the result to each
case (`--kit-acceptance-strict` also fails the case when a check fails).
