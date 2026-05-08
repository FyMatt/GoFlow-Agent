# Scaffold Commands

[English](./scaffolds.md) | [简体中文](./scaffolds.zh-CN.md)

GoFlow includes small generators for common second-development tasks. They are intentionally conservative: generated files are examples that keep workspace boundaries visible and require the operator to choose when to expose new permissions.

## Commands

```text
/skill-templates
/new-skill <template> <name>
/new-tool python <name>
/new-agent <name>
/new-provider <name>
/new-workflow <name> [--template <template>]
/new-workflow-template <source-template> <name>
/new-policy-rule <preset> <name>
/new-team <preset> <name>
/new-kit <preset> <name> [--materialize|--full]
```

Generated paths are relative to the runtime home, not the active workspace:

- `skills/<name>/SKILL.md`
- `mcp_servers/<name>.py`
- `configs/mcp_servers/<name>.yaml`
- `configs/agents/<name>.yaml`
- `configs/providers/<name>.yaml`
- `workflows/<name>/workflow.yaml`
- `workflows/<name>/WORKFLOW.md`
- `templates/workflows/<name>.yaml`
- `policies/workflow_rules/<name>.yaml`
- `templates/teams/<name>.yaml`
- `kits/<name>/kit.yaml`

The active workspace remains the root for file tools and `@file` references.

## Skill Scaffold

List available templates:

```text
/skill-templates
```

Create a skill:

```text
/new-skill code-audit dependency-audit
```

The generated `SKILL.md` includes:

- metadata front matter
- tool declarations
- activation keywords
- preferred mode and agent
- output kind
- an example request
- extension notes for keywords, tools, agent boundary, and follow-up skills

After editing:

```text
/reload
/skills
```

Check that the skill appears with the expected mode, agent, output kind, and activation hints.

## Python MCP Tool Scaffold

Create a Python MCP server:

```text
/new-tool python workspace-helper
```

The generated server includes:

- stdio JSON-RPC loop
- `initialize`
- `tools/list`
- `tools/call`
- strict object schemas with `additionalProperties: false`
- `GOFLOW_WORKSPACE_ROOT` path scoping
- sample `ping`, `read_text`, and `write_text` tools

Add it as a modular MCP server config, for example
`configs/mcp_servers/workspace-helper.yaml`:

```yaml
# Loaded automatically from configs/mcp_servers/*.yaml.
mcp_servers:
  - name: workspace_helper
    command: python
    args:
      - ./mcp_servers/workspace-helper.py
    enabled: true
    timeout: 30s
    workdir: .
    env_allowlist: [PATH, HOME, USERPROFILE, TMP, TEMP]
    isolation: process_group
    allowed_commands:
      - python
    max_request_bytes: 65536
    max_response_bytes: 2097152
```

Then run:

```text
/reload-tools
/tools
```

If two servers expose the same short tool name, use the qualified name such as `workspace_helper/read_text`.

`isolation: process_group` is optional but recommended for third-party servers. It improves process lifecycle boundaries where the host OS supports separate process groups. It does not replace workspace path checks, command allowlists, tool policy, or network-aware agent permissions.

### HTTP Tool Presets

Studio can create tool scaffolds through the resource API:

```text
GET /api/resources/tools/scaffolds
GET /api/resources/tools/scaffolds/{preset}?name=workspace-helper
POST /api/resources/tools/scaffolds/{preset}
```

Current presets:

- `python-local`: local Python MCP starter with `process_group` lifecycle
  isolation
- `python-container-readonly`: containerized Python MCP starter with read-only
  workspace access, network disabled, default resource limits, non-root user,
  read-only rootfs, isolated IPC, user namespace, tmpfs scratch mounts, and
  container init
- `python-container-writer`: containerized Python MCP starter with write-capable
  workspace access, network disabled, default resource limits, read-only
  rootfs, isolated IPC, user namespace, tmpfs scratch mounts, and container init
- `python-container-network`: containerized Python MCP starter with read-only
  workspace access, explicit network egress, non-root user, read-only rootfs,
  isolated IPC, user namespace, tmpfs scratch mounts, and container init
- `python-container-production`: locked-down read-only containerized Python MCP
  starter for production deployments that pre-pull trusted digest-pinned images
  and keep `pull_policy: never`

Preset list responses include `default_image` and `default_isolation_options`
for container presets so Studio can show the generated runtime boundary before
opening a named detail preview.

Example request:

```json
{
  "name": "sandboxed-reader",
  "image": "ghcr.io/fymatt/goflow-agent-mcp-python:<version>",
  "runtime": "docker",
  "workspace_mount": "ro",
  "network": "disabled",
  "isolation_options": {
    "memory": "256m"
  }
}
```

The backend writes both `mcp_servers/<name>.py` and
`configs/mcp_servers/<name>.yaml`. Container presets keep the generated tool
code on the host and mount only that file into the container:

```yaml
isolation: container
isolation_options:
  image: ghcr.io/fymatt/goflow-agent-mcp-python:<version>
  workspace_mount: ro
  network: disabled
  ipc: none
  userns: auto
  memory: 256m
  memory_swap: 256m
  cpus: "0.5"
  pids_limit: "64"
  pull_policy: missing
  readonly_rootfs: "true"
  no_new_privileges: "true"
  cap_drop: all
  tmpfs: /tmp:rw,noexec,nosuid,size=64m;/run:rw,noexec,nosuid,size=8m
  init: "true"
  user: 65532:65532
  tool_source: <runtime-home>/mcp_servers/sandboxed-reader.py
  tool_target: /goflow-tools/sandboxed-reader.py
  tool_mount: ro
```

Release builds default to the matching
`ghcr.io/fymatt/goflow-agent-mcp-python:<version>` image tag. Development builds
fall back to `latest`, and every scaffold request can override `image`.
Container presets default to `ipc: none`, `userns: auto`, `memory_swap: 256m`,
and `pull_policy: missing`. If the selected Docker/Podman runtime does not
support `userns: auto`, override it with `nomap`, `keep-id`, or remove the
option for that host. Use `pull_policy: never` only when production hosts
pre-pull trusted images. Read-only and network container presets also default
to `user: 65532:65532`. The write-capable container preset leaves `user` unset
by default so local bind mount permissions do not silently break writes on Linux
hosts; set it explicitly when the workspace mount permissions support non-root
writes.

For production tool scaffolds, select `python-container-production` and replace
the default image tag with an immutable `image@sha256:...` reference after
building or pulling a trusted image. The generated config uses
`isolation_profile: production` and `pull_policy: never`, so deployments must
pre-pull the image on each host. `/api/runtime.mcp_servers[]` reports
`container_image_reference_type`, `container_image_digest_pinned`,
`container_image_production_ready`, and `container_pull_policy`; Studio should
render those fields directly instead of parsing warning text.

Use `overwrite=1` or body field `"overwrite": true` when replacing an existing
file-backed custom tool. The saved resource appears immediately, but MCP server
processes are created during startup, so Studio should show the returned
restart/apply-state guidance.

## Agent Scaffold

Create an agent config snippet:

```text
/new-agent researcher
```

The generated file is stored under `configs/agents/<name>.yaml`. GoFlow loads `configs/agents/*.yaml` on startup and merges those profiles over the top-level `agents:` map, so keep each custom agent as its own versionable file and restart GoFlow after changing runtime agent config.

Start narrow:

```yaml
allowed_tool_kinds: [read]
```

When adding network or write access, prefer an exact allowlist:

```yaml
allowed_tool_kinds: [read, network]
allowed_tools: [web_tools/fetch_url, web_tools/web_search]
```

Run:

```text
/agents
```

Confirm the new agent displays the expected mode, policy, tool kinds, and allowlist.

## Provider Scaffold

Create a model provider config snippet:

```text
/new-provider deepseek
```

The generated file is stored under `configs/providers/<name>.yaml`. GoFlow loads `configs/providers/*.yaml` on startup and merges those provider definitions over the top-level `providers:` map. Fill in `base_url`, `api_key`, and `model`, then restart GoFlow so the provider registry is rebuilt.

Example:

```yaml
deepseek:
  provider: openai-compatible
  base_url: https://api.deepseek.com/v1
  api_key: ${DEEPSEEK_API_KEY}
  model: deepseek-chat
  timeout: 60s
  retry_count: 2
  retry_backoff: 2s
```

Assign an agent to the provider by setting its `provider` field:

```yaml
researcher:
  provider: deepseek
  mode: audit
  allowed_tool_kinds: [read, network]
```

## Workflow Scaffold

Create an executable workflow graph:

```text
/new-workflow release-check
```

The default template is `plan-fix-audit`. You can also choose a richer built-in
template:

```text
/new-workflow security-review --template human-input-security-review
```

Available workflow templates are also exposed through the HTTP API:

```text
GET /api/workflow-templates
GET /api/workflow-templates/{name}
```

Current built-in templates:

- `task-decomposition-plan`
- `agent-framework-extension`
- `plan-fix-audit`
- `software-quality-gate`
- `web-research-risk`
- `security-audit-evidence-gate`
- `parallel-research-review`
- `binary-triage`
- `docs-review-publish`
- `operations-runbook`
- `customer-support-triage`
- `human-input-security-review`
- `software-team-review-gate`

Use `task-decomposition-plan` before high-risk or broad tasks when you want the
agent to produce a reviewable workflow draft with node contracts, data flow,
artifacts, risk labels, and acceptance criteria before any implementation
workflow runs.

Use `agent-framework-extension` when the task is to create or reshape GoFlow
itself: a vertical Agent, skill, MCP tool, workflow, team template, policy rule,
or kit. It starts with a manual scope gate, passes the request through a
framework-extension team context, reviews the linked resource design, and only
then routes to an approval-protected materialization stage.

The default generated workflow starts with:

- `plan` through `planner` and `execution-plan`
- `implement` through `fixer` and `code-writing` with `approval: true`
- `audit` through `auditor` and `code-audit`

Run it:

```text
/workflow release-check review the auth changes
```

Rules to keep:

- each stage names one configured agent
- each stage names one loaded skill
- write or exec stages should normally have `approval: true`
- `next` can only point to declared stage names
- agent permissions remain the final tool boundary

## Workflow Template Scaffold

Workflow templates are reusable graph blueprints. They are useful when you want
Studio users or CLI users to create many workflows from the same reviewed
pattern.

Inspect available templates:

```text
/workflow-templates
/workflow-templates plan-fix-audit
```

Fork a built-in or loaded template into a file-backed custom template:

```text
/new-workflow-template plan-fix-audit custom-plan-template
```

Generated files live under `templates/workflows/*.yaml` and use the versioned
`goflow.workflow_template_resource` envelope. After editing, restart GoFlow or
reload the runtime that owns workflow resources, then create workflows from it:

```text
/new-workflow release-check --template custom-plan-template
```

## Workflow Policy Rule Scaffold

Custom policy rules let workflow `policy_guard` nodes use a named, reusable
gate instead of embedding every condition directly in a workflow graph.

Create a rule:

```text
/new-policy-rule risk-threshold high-risk-gate
```

Available presets:

- `risk-threshold`: branch on a severity/risk output such as `high` or `critical`
- `truthy-reference`: pass when a referenced output is present
- `contains-text`: pass when a referenced output contains expected text
- `minimum-count`: pass when a list/count reaches a threshold
- `team-review-quorum`: gate on executable team approval/rejection state
- `expression`: start from a custom workflow expression

Generated files live under `policies/workflow_rules/*.yaml` and use the
versioned `goflow.workflow_policy_rule` envelope. After creating or editing a
rule, restart GoFlow or reload the runtime that owns workflow resources, then
inspect:

```text
/policy-rules
/policy-rules high-risk-gate
```

Use the rule from a workflow stage:

```yaml
- name: gate
  node_type: policy_guard
  params:
    rule: high-risk-gate
    ref: stages.audit.outputs.risk
    minimum: high
  next:
    - fix
  deny_next:
    - end
```

## Team Template Scaffold

Team templates describe reusable multi-agent collaboration patterns. They can
be referenced by workflow `team` nodes and by `team_approval_gate` policy rules.

Create a custom team template from a built-in collaboration pattern:

```text
/new-team software-review custom-review-team
```

Available presets:

- `software-review`
- `agent-framework`
- `security-review`
- `web-research`
- `binary-triage`
- `documentation`
- `operations-runbook`
- `customer-support`

Generated files live under `templates/teams/*.yaml` and use the versioned
`goflow.team_template_resource` envelope. Inspect loaded teams with:

```text
/teams
/teams custom-review-team
```

Use the team from a workflow:

```yaml
- name: review
  node_type: team
  params:
    team: custom-review-team
    execute: "true"
  next:
    - gate
```

## Kit Scaffold

Create a vertical Agent kit manifest:

```text
/new-kit software-engineering acme-platform
```

The generated file is stored at `kits/<name>/kit.yaml`. It packages references
to existing agents, skills, tools, workflows, workflow templates, team
templates, policy rules, required environment variables, examples, and tags.
Kit scaffolds do not create every referenced resource; they give you a
versionable manifest that can be edited in YAML or through Studio resources.
CLI users can inspect local kit manifests with `/kits` and `/kits <name>`;
the detail view shows referenced resources, examples, metadata, and missing
environment-variable warnings without starting the HTTP server.

Create a full linked starter kit when you want copyable Agent/Skill/Tool/
Workflow resources that reference each other:

```text
/new-kit multi-domain-agent goflow-starter --materialize
/new-kit software-engineering acme-platform --materialize
```

This creates:

- `kits/acme-platform/kit.yaml`
- `configs/agents/acme-platform-agent.yaml`
- `skills/acme-platform-skill/SKILL.md`
- `mcp_servers/acme-platform-helper.py`
- `configs/mcp_servers/acme-platform-helper.yaml`
- `workflows/acme-platform-workflow/workflow.yaml`
- `templates/workflows/acme-platform-template.yaml`
- `templates/teams/acme-platform-team.yaml`
- `policies/workflow_rules/acme-platform-gate.yaml`

The generated workflow passes outputs between nodes: the team node feeds the
planning skill, the planning output feeds a policy gate, and the approved or
denied route feeds a report or revision node. The generated helper tool uses
Docker/Podman `isolation: container` with read-only workspace access by default.

For the broadest starter path, use `multi-domain-agent`. It creates or
references one connected entry kit that can route software engineering, web
security, security research, binary analysis, documentation, operations,
customer support, and framework-extension requests into the matching workflow
and team templates. It is intended for first-time Studio users because the
generated resource graph shows how Agents, Skills, Tools, Teams, Workflow
Templates, Policy Rules, and Kits are meant to work together instead of as
isolated examples.

Available presets:

- `multi-domain-agent`
- `software-engineering`
- `agent-framework`
- `web-security`
- `security-research`
- `binary-analysis`
- `documentation`
- `operations-runbook`
- `customer-support`

Use the HTTP resource APIs when you need validation, import/export, or bundle
workflows:

```text
GET /api/resources/kits
GET /api/resources/kits/scaffolds
POST /api/resources/kits/scaffolds/{preset}
GET /api/resources/kits/{name}/validate
GET /api/resources/kits/{name}/export?format=yaml
POST /api/resources/kits/import
```

For HTTP materialization, pass `materialize=true` in the query string or JSON
body. The response includes `resources`, `restart_required`, and
`activation_steps` so Studio can show what was created and why a restart is
needed:

```json
{
  "name": "acme-platform",
  "materialize": true,
  "overwrite": false
}
```

## Verification Checklist

1. Run the relevant reload command: `/reload` or `/reload-tools`.
2. Inspect discovery output: `/skills`, `/tools`, or `/agents`.
3. Confirm no schema warnings or unknown references appear.
4. Run a small request before using the scaffold in a larger workflow.
5. Check `/status` and `/session` when a workflow or approval pauses.

## Complete Extension Example

See `examples/extension-workflow` for a copyable example that combines:

- a Python MCP server under `mcp_servers/`
- a modular MCP server config under `configs/mcp_servers/`
- a narrow read-only agent snippet under `configs/agents/`
- optional provider snippets under `configs/providers/`
- a custom skill under `skills/`
- a graph workflow under `workflows/`

Use it when you want to verify the full second-development path end to end instead of testing each scaffold in isolation.

Run the packaged smoke test before copying the example into a runtime home:

```bash
python scripts/validate_extension_workflow.py
```

