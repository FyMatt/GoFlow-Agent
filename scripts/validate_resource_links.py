#!/usr/bin/env python3
"""Validate that bundled resource templates reference existing resources.

This is a lightweight CI guard for GoFlow's starter Kits and Kit scaffold
presets. It intentionally uses only Python's standard library: the bundled YAML
files use a simple subset here, and deep schema validation remains covered by
Go tests and runtime validation.
"""

from __future__ import annotations

import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


LIST_FIELDS = [
    "providers",
    "agents",
    "skills",
    "tools",
    "workflows",
    "workflow_templates",
    "team_templates",
    "policy_rules",
]

RESOURCE_GROUP_FIELDS = [
    "agents",
    "skills",
    "tools",
    "workflow_templates",
    "team_templates",
    "policy_rules",
]

VERTICAL_PACK_MATURITIES = {"generic", "guided", "professional", "production-ready"}

MATURITY_RANK = {
    "generic": 1,
    "guided": 2,
    "professional": 3,
    "production-ready": 4,
}

PROFESSIONAL_VERTICAL_PACK_TEXT_FIELDS = [
    "domain",
    "summary",
]

PROFESSIONAL_VERTICAL_PACK_LIST_FIELDS = [
    "supported_tasks",
    "required_inputs",
    "tool_boundaries",
    "safety_gates",
    "quality_gates",
    "evidence_artifacts",
    "simple_mode",
    "expert_mode",
    "token_strategy",
]

WEB_SECURITY_BROWSER_TOOLS = [
    "web_tools/browser_snapshot",
    "web_tools/browser_probe_points",
]

PRODUCTION_READY_DOMAIN_TOOL_PREFIXES = {
    "software-engineering": "file_tools/",
    "agent-framework": "file_tools/",
    "operations-runbook": "network_tools/",
    "web-security": "web_tools/browser_",
    "binary-analysis": "python_notes/",
}

PRODUCTION_READY_DOMAIN_REQUIREMENTS = {
    "software-engineering": {
        "tools": [
            "file_tools/read_file",
            "file_tools/search_files",
            "file_tools/write_file",
        ],
        "files": [
            ("mcp_servers/file_tools/main.go", ["read_file", "search_files", "write_file", "workspace root"]),
            ("mcp_servers/file_tools/main_test.go", ["outside workspace", "write_file", "search_files"]),
            ("internal/agent/templates/workflows/engineering-parallel-delivery.yaml", ["parallel", "join", "worker_contract", "engineering_v1", "quality_gate"]),
            ("internal/agent/workflow_templates_test.go", ["engineering-parallel-delivery", "worker_contract", "prompt budgets"]),
            ("internal/scaffold/materialized_test.go", ["RenderMaterializedWorkflowTemplateUsesModelAndContextBudgets", "context:", "token_policy"]),
            ("docs/workflows.md", ["engineering-parallel-delivery", "worker_contract", "context.max_tokens", "artifact-first"]),
            ("docs/scaffolds.md", ["engineering-parallel-delivery", "bounded parallel worker", "model.max_tokens"]),
            ("scripts/validate_release_archives.py", ["engineering-parallel-delivery"]),
            ("scripts/validate_deployment_assets.py", ["validate_resource_links.py", "validate_web_i18n.py", "validate_docs.py"]),
        ],
        "contract_terms": [
            "workspace",
            "approval",
            "verification",
            "audit",
            "evidence",
            "worker",
            "parallel",
            "artifact",
            "token",
        ],
    },
    "agent-framework": {
        "tools": [
            "file_tools/read_file",
            "file_tools/search_files",
            "file_tools/write_file",
        ],
        "files": [
            ("internal/agent/templates/workflows/agent-framework-extension.yaml", ["agent-framework-extension", "input_gate", "quality_gate", "materialize", "approval"]),
            ("internal/scaffold/materialized.go", ["activation_steps", "RenderMaterialized", "tool-boundary"]),
            ("internal/scaffold/materialized_test.go", ["Materialized", "activation", "workflow template"]),
            ("internal/scaffold/kit_presets.go", ["VerticalPack", "SimpleMode", "ExpertMode"]),
            ("internal/api/resources.go", ["materialize", "restart_required", "activation_steps"]),
            ("docs/scaffolds.md", ["agent-framework-extension", "materialization", "activation_steps", "runtime home"]),
            ("docs/resources.md", ["agent-framework-kit", "production-ready", "materialize"]),
            ("scripts/validate_release_archives.py", ["examples/extension-workflow"]),
            ("scripts/validate_deployment_assets.py", ["internal/scaffold/templates/kits/materialized/generic/tool-config.yaml.tmpl"]),
        ],
        "contract_terms": [
            "runtime home",
            "materialization",
            "activation",
            "rollback",
            "validation",
            "approval",
            "resource graph",
            "scaffold",
            "evidence",
        ],
    },
    "operations-runbook": {
        "tools": [
            "network_tools/device_discovery_plan",
            "network_tools/device_command_plan",
            "network_tools/device_config_dry_run",
        ],
        "files": [
            ("mcp_servers/network_tools/main.go", ["device_discovery_plan", "authorized_scope", "allowed_hosts"]),
            ("mcp_servers/network_tools/main_test.go", ["RequiresAuthorization", "OutsideAllowlist", "NeverAppliesChanges"]),
            ("configs/mcp_servers/network_tools.yaml", ["network_tools", "max_concurrent_calls"]),
            ("docs/mcp.md", ["network_tools", "authorized_scope", "allowed_hosts"]),
            ("docs/mcp.zh-CN.md", ["network_tools", "authorized_scope", "allowed_hosts"]),
            ("docs/mcp-authoring.md", ["network_tools", "approval", "evidence"]),
            ("scripts/validate_release_archives.py", ["network_tools"]),
            ("scripts/validate_deployment_assets.py", ["network_tools"]),
        ],
        "contract_terms": [
            "authorized_scope",
            "allowed_hosts",
            "credential",
            "allowlist",
            "dry",
            "rollback",
            "approval",
            "audit",
            "evidence",
        ],
    },
    "web-security": {
        "tools": [
            "web_tools/browser_snapshot",
            "web_tools/browser_probe_points",
        ],
        "files": [
            ("mcp_servers/web_tools/main.go", ["browser_snapshot", "browser_probe_points", "authorized_scope"]),
            ("mcp_servers/web_tools/main_test.go", ["BrowserSnapshotRequiresAuthorization", "BrowserProbePointsRequiresAuthorization", "BrowserAutomationSecurityTools"]),
            ("configs/mcp_servers/web_tools.yaml", ["web_tools", "max_concurrent_calls"]),
            ("docs/mcp.md", ["browser_snapshot", "browser_probe_points", "active_probe_approved"]),
            ("docs/mcp.zh-CN.md", ["browser_snapshot", "browser_probe_points", "active_probe_approved"]),
            ("skills/web-vulnerability-research/SKILL.md", ["authorized_scope", "allowed_hosts", "active_probe_approved"]),
            ("scripts/validate_release_archives.py", ["web_tools"]),
            ("scripts/validate_deployment_assets.py", ["web_tools"]),
        ],
        "contract_terms": [
            "authorized_scope",
            "allowed_hosts",
            "active_probe_approved",
            "approval",
            "evidence",
            "rate",
            "non-destructive",
            "screenshot",
        ],
    },
}


class DomainCapabilityRecord:
    def __init__(
        self,
        *,
        domain: str,
        maturity: str,
        source_kind: str,
        name: str,
        label: str,
        fields: dict[str, list[str]],
        vertical_lists: dict[str, list[str]],
        vertical_text: dict[str, str],
        text: str,
    ) -> None:
        self.domain = domain
        self.maturity = maturity
        self.source_kind = source_kind
        self.name = name
        self.label = label
        self.fields = fields
        self.vertical_lists = vertical_lists
        self.vertical_text = vertical_text
        self.text = text


class DomainCapability:
    def __init__(self, domain: str) -> None:
        self.domain = domain
        self.records: list[DomainCapabilityRecord] = []

    def add(self, record: DomainCapabilityRecord) -> None:
        self.records.append(record)

    def max_maturity(self) -> str:
        maturity = "generic"
        for record in self.records:
            if MATURITY_RANK.get(record.maturity, 0) > MATURITY_RANK.get(maturity, 0):
                maturity = record.maturity
        return maturity

    def source_count(self, source_kind: str) -> int:
        return sum(1 for record in self.records if record.source_kind == source_kind)

    def refs(self, field: str) -> set[str]:
        values: set[str] = set()
        for record in self.records:
            values.update(normalize(value) for value in record.fields.get(field, []))
        return {value for value in values if value}


def read(path: Path) -> str:
    if not path.exists():
        raise AssertionError(f"missing file: {path.relative_to(ROOT)}")
    return path.read_text(encoding="utf-8")


def normalize(value: str) -> str:
    return value.strip().strip("\"'").lower()


def clean_scalar(value: str) -> str:
    value = value.split(" #", 1)[0].strip()
    if value in {"", "[]", "{}"}:
        return ""
    return value.strip("\"'")


def parse_inline_list(value: str) -> list[str]:
    value = clean_scalar(value)
    if not (value.startswith("[") and value.endswith("]")):
        return []
    inner = value[1:-1].strip()
    if not inner:
        return []
    return [clean_scalar(part) for part in inner.split(",") if clean_scalar(part)]


def leading_spaces(line: str) -> int:
    return len(line) - len(line.lstrip(" "))


def list_field(text: str, key: str, indent: int = 0) -> list[str]:
    lines = text.splitlines()
    prefix = " " * indent + key + ":"
    for index, line in enumerate(lines):
        if indent > 0:
            if not line.startswith(prefix):
                continue
        elif not line.lstrip(" ").startswith(key + ":"):
            continue
        base_indent = leading_spaces(line)
        rest = line.split(":", 1)[1].strip()
        if rest.startswith("["):
            return parse_inline_list(rest)
        if rest:
            return [clean_scalar(rest)]
        values: list[str] = []
        for child in lines[index + 1 :]:
            if not child.strip():
                continue
            child_indent = leading_spaces(child)
            if child_indent <= base_indent:
                break
            stripped = child.strip()
            if not stripped.startswith("- "):
                continue
            item = clean_scalar(stripped[2:])
            if item and ":" not in item:
                values.append(item)
        return values
    return []


def first_scalar_value(text: str, key: str) -> str:
    values = scalar_values(text, key)
    return values[0] if values else ""


def mapping_block(text: str, key: str) -> str:
    lines = text.splitlines()
    for index, line in enumerate(lines):
        if not line.lstrip(" ").startswith(key + ":"):
            continue
        base_indent = leading_spaces(line)
        rest = line.split(":", 1)[1].strip()
        if rest:
            return ""
        block: list[str] = []
        for child in lines[index + 1 :]:
            if not child.strip():
                continue
            child_indent = leading_spaces(child)
            if child_indent <= base_indent:
                break
            block.append(child[base_indent + 2 :] if len(child) >= base_indent + 2 else child.lstrip(" "))
        return "\n".join(block)
    return ""


def vertical_pack_text(text: str) -> str:
    return mapping_block(text, "vertical_pack")


def vertical_pack_model_routes(text: str) -> dict[str, list[str]]:
    pack = vertical_pack_text(text)
    routes = mapping_block(pack, "model_routes") if pack else ""
    return {
        "strong": list_field(routes, "strong") if routes else [],
        "worker": list_field(routes, "worker") if routes else [],
        "verifier": list_field(routes, "verifier") if routes else [],
    }


def scalar_values(text: str, key: str) -> list[str]:
    values = []
    pattern = re.compile(rf"^\s*(?:-\s*)?{re.escape(key)}:\s*(.+?)\s*$", re.MULTILINE)
    for match in pattern.finditer(text):
        value = clean_scalar(match.group(1))
        if value:
            values.append(value)
    return values


def maps_field(text: str, key: str, indent: int = 0) -> list[dict[str, str]]:
    lines = text.splitlines()
    prefix = " " * indent + key + ":"
    for index, line in enumerate(lines):
        if indent > 0:
            if not line.startswith(prefix):
                continue
        elif not line.lstrip(" ").startswith(key + ":"):
            continue
        base_indent = leading_spaces(line)
        items: list[dict[str, str]] = []
        current: dict[str, str] | None = None
        for child in lines[index + 1 :]:
            if not child.strip():
                continue
            child_indent = leading_spaces(child)
            if child_indent <= base_indent:
                break
            stripped = child.strip()
            if stripped.startswith("- "):
                current = {}
                items.append(current)
                rest = stripped[2:].strip()
                if ":" in rest:
                    sub_key, sub_value = rest.split(":", 1)
                    current[sub_key.strip()] = clean_scalar(sub_value)
                continue
            if current is not None and ":" in stripped:
                sub_key, sub_value = stripped.split(":", 1)
                current[sub_key.strip()] = clean_scalar(sub_value)
        return items
    return []


def names_from_files(pattern: str) -> set[str]:
    return {normalize(path.stem) for path in ROOT.glob(pattern) if path.is_file()}


def names_from_dirs(pattern: str, marker: str | None = None) -> set[str]:
    values = set()
    for path in ROOT.glob(pattern):
        if not path.is_dir():
            continue
        if marker and not (path / marker).exists():
            continue
        values.add(normalize(path.name))
    return values


def policy_rule_names() -> set[str]:
    names = set()
    for path in [
        ROOT / "internal" / "agent" / "templates" / "policy_rules" / "builtins.yaml",
        *ROOT.glob("policies/workflow_rules/*.yaml"),
        *ROOT.glob("policies/workflow_rules/*.yml"),
    ]:
        if path.exists():
            names.update(normalize(value) for value in scalar_values(read(path), "name"))
    return names


def tool_names_from_go(path: Path, server: str) -> set[str]:
    if not path.exists():
        return set()
    names = set(re.findall(r'Name:\s*"([A-Za-z0-9_]+)"', read(path)))
    return {f"{server}/{name}" for name in names}


def tool_names_from_python_notes() -> set[str]:
    path = ROOT / "mcp_servers" / "python_notes.py"
    if not path.exists():
        return set()
    names = set(re.findall(r'"name":\s*"([A-Za-z0-9_]+)"', read(path)))
    return {f"python_notes/{name}" for name in names}


def known_tools() -> set[str]:
    tools = set()
    tools.update(tool_names_from_go(ROOT / "mcp_servers" / "file_tools" / "main.go", "file_tools"))
    tools.update(tool_names_from_go(ROOT / "mcp_servers" / "web_tools" / "main.go", "web_tools"))
    tools.update(tool_names_from_go(ROOT / "mcp_servers" / "network_tools" / "main.go", "network_tools"))
    tools.update(tool_names_from_go(ROOT / "mcp_servers" / "skill_runner" / "main.go", "skill_runner"))
    tools.update(tool_names_from_python_notes())
    return {normalize(tool) for tool in tools}


def known_resources() -> dict[str, set[str]]:
    return {
        "providers": names_from_files("configs/providers/*.yaml"),
        "agents": names_from_files("configs/agents/*.yaml"),
        "skills": names_from_dirs("skills/*", "SKILL.md"),
        "tools": known_tools(),
        "workflows": names_from_dirs("workflows/*", "workflow.yaml"),
        "workflow_templates": names_from_files("internal/agent/templates/workflows/*.yaml")
        | names_from_files("templates/workflows/*.yaml"),
        "team_templates": names_from_files("internal/agent/templates/teams/*.yaml")
        | names_from_files("templates/teams/*.yaml"),
        "policy_rules": policy_rule_names(),
    }


def split_preset_blocks(text: str) -> list[str]:
    lines = text.splitlines()
    blocks: list[list[str]] = []
    current: list[str] | None = None
    for line in lines:
        if line.startswith("  - name:"):
            if current:
                blocks.append(current)
            current = [line]
            continue
        if current is not None:
            if line.startswith("  - ") and not line.startswith("  - name:"):
                blocks.append(current)
                current = None
                continue
            current.append(line)
    if current:
        blocks.append(current)
    return ["\n".join(block) for block in blocks]


def first_named_value(text: str) -> str:
    values = scalar_values(text, "name")
    return values[0] if values else "preset"


def validate_membership(
    errors: list[str],
    label: str,
    key: str,
    refs: list[str],
    known: set[str],
) -> None:
    for ref in refs:
        ref_key = normalize(ref)
        if ref_key not in known:
            errors.append(f"{label}: {key} references missing resource {ref!r}")


def validate_vertical_pack_contract(errors: list[str], label: str, text: str) -> None:
    pack = vertical_pack_text(text)
    if not pack:
        errors.append(f"{label}: missing vertical_pack contract")
        return
    maturity = normalize(first_scalar_value(pack, "maturity"))
    if maturity not in VERTICAL_PACK_MATURITIES:
        errors.append(f"{label}: vertical_pack.maturity must be one of {sorted(VERTICAL_PACK_MATURITIES)}, got {maturity!r}")
        return
    if maturity not in {"professional", "production-ready"}:
        return
    for field in PROFESSIONAL_VERTICAL_PACK_TEXT_FIELDS:
        if not first_scalar_value(pack, field):
            errors.append(f"{label}: professional vertical_pack missing {field}")
    for field in PROFESSIONAL_VERTICAL_PACK_LIST_FIELDS:
        if not list_field(pack, field):
            errors.append(f"{label}: professional vertical_pack missing {field}")
    routes = vertical_pack_model_routes(text)
    if not routes["strong"] or not routes["worker"]:
        errors.append(f"{label}: professional vertical_pack missing model_routes.strong or model_routes.worker")


def validate_web_security_browser_tools(errors: list[str], label: str, text: str) -> None:
    names = {normalize(value) for value in scalar_values(text, "name")}
    skills = {normalize(value) for value in scalar_values(text, "skill")}
    skills.update(normalize(value) for value in list_field(text, "skills"))
    requires_browser_tools = (
        "web-vulnerability-research" in skills
        or names
        & {
            "web-vulnerability-research",
            "web-research-risk",
            "web-research-team",
            "web-security-kit",
            "web-security",
        }
    )
    if not requires_browser_tools:
        return
    for tool in WEB_SECURITY_BROWSER_TOOLS:
        if tool not in text:
            errors.append(f"{label}: web security resource should reference {tool}")


def validate_document(label: str, text: str, known: dict[str, set[str]], *, require_complete: bool) -> list[str]:
    errors: list[str] = []
    fields = {key: list_field(text, key) for key in LIST_FIELDS}
    if require_complete:
        for key in ["agents", "skills", "workflow_templates", "team_templates", "policy_rules"]:
            if not fields[key]:
                errors.append(f"{label}: expected at least one {key} reference")
    for key, refs in fields.items():
        validate_membership(errors, label, key, refs, known[key])
    validate_vertical_pack_contract(errors, label, text)
    validate_web_security_browser_tools(errors, label, text)

    agent_refs = {normalize(value) for value in fields["agents"]}
    skill_refs = {normalize(value) for value in fields["skills"]}
    team_refs = {normalize(value) for value in fields["team_templates"]}
    runnable_refs = {normalize(value) for value in fields["workflows"] + fields["workflow_templates"]}

    for value in scalar_values(text, "recommended_agent"):
        if normalize(value) not in known["agents"]:
            errors.append(f"{label}: recommended_agent references missing agent {value!r}")
        elif agent_refs and normalize(value) not in agent_refs:
            errors.append(f"{label}: recommended_agent {value!r} is not included in agents")
    for value in scalar_values(text, "recommended_workflow"):
        ref = normalize(value)
        if ref not in known["workflows"] and ref not in known["workflow_templates"]:
            errors.append(f"{label}: recommended_workflow references missing workflow/template {value!r}")
        elif runnable_refs and ref not in runnable_refs:
            errors.append(f"{label}: recommended_workflow {value!r} is not included in workflows or workflow_templates")
    for value in scalar_values(text, "recommended_team"):
        if normalize(value) not in known["team_templates"]:
            errors.append(f"{label}: recommended_team references missing team template {value!r}")
        elif team_refs and normalize(value) not in team_refs:
            errors.append(f"{label}: recommended_team {value!r} is not included in team_templates")
    for value in scalar_values(text, "primary_skill"):
        if normalize(value) not in known["skills"]:
            errors.append(f"{label}: primary_skill references missing skill {value!r}")
        elif skill_refs and normalize(value) not in skill_refs:
            errors.append(f"{label}: primary_skill {value!r} is not included in skills")

    for index, example in enumerate(maps_field(text, "examples"), start=1):
        workflow = example.get("workflow", "")
        if workflow:
            ref = normalize(workflow)
            if ref not in known["workflows"] and ref not in known["workflow_templates"]:
                errors.append(f"{label}: example {index} workflow references missing workflow/template {workflow!r}")
            elif runnable_refs and ref not in runnable_refs:
                errors.append(f"{label}: example {index} workflow {workflow!r} is not included in this resource")
        agent = example.get("agent", "")
        if agent:
            ref = normalize(agent)
            if ref not in known["agents"]:
                errors.append(f"{label}: example {index} agent references missing agent {agent!r}")
            elif agent_refs and ref not in agent_refs:
                errors.append(f"{label}: example {index} agent {agent!r} is not included in this resource")
    return errors


def domain_record_from_document(label: str, text: str, source_kind: str) -> DomainCapabilityRecord | None:
    pack = vertical_pack_text(text)
    if not pack:
        return None
    domain = normalize(first_scalar_value(pack, "domain"))
    maturity = normalize(first_scalar_value(pack, "maturity"))
    if not domain:
        domain = normalize(first_named_value(text)).removesuffix("-kit")
    fields = {key: list_field(text, key) for key in LIST_FIELDS}
    vertical_lists = {key: list_field(pack, key) for key in PROFESSIONAL_VERTICAL_PACK_LIST_FIELDS}
    routes = vertical_pack_model_routes(text)
    vertical_lists["model_routes.strong"] = routes["strong"]
    vertical_lists["model_routes.worker"] = routes["worker"]
    vertical_lists["model_routes.verifier"] = routes["verifier"]
    vertical_text = {key: first_scalar_value(pack, key) for key in PROFESSIONAL_VERTICAL_PACK_TEXT_FIELDS}
    return DomainCapabilityRecord(
        domain=domain,
        maturity=maturity,
        source_kind=source_kind,
        name=normalize(first_named_value(text)),
        label=label,
        fields=fields,
        vertical_lists=vertical_lists,
        vertical_text=vertical_text,
        text=text,
    )


def add_domain_record(matrix: dict[str, DomainCapability], record: DomainCapabilityRecord | None) -> None:
    if record is None or not record.domain:
        return
    matrix.setdefault(record.domain, DomainCapability(record.domain)).add(record)


def collect_kit_files(known: dict[str, set[str]], matrix: dict[str, DomainCapability]) -> list[str]:
    errors: list[str] = []
    for path in sorted(ROOT.glob("kits/*/kit.yaml")):
        label = str(path.relative_to(ROOT))
        text = read(path)
        errors.extend(validate_document(label, text, known, require_complete=True))
        add_domain_record(matrix, domain_record_from_document(label, text, "kit"))
    return errors


def collect_kit_scaffold_presets(known: dict[str, set[str]], matrix: dict[str, DomainCapability]) -> list[str]:
    path = ROOT / "internal" / "scaffold" / "templates" / "kits" / "scaffolds" / "presets.yaml"
    errors: list[str] = []
    for block in split_preset_blocks(read(path)):
        label = f"{path.relative_to(ROOT)}:{first_named_value(block)}"
        errors.extend(validate_document(label, block, known, require_complete=True))
        add_domain_record(matrix, domain_record_from_document(label, block, "scaffold_preset"))
    return errors


def validate_domain_capability_matrix(matrix: dict[str, DomainCapability]) -> list[str]:
    errors: list[str] = []
    if not matrix:
        return ["domain capability matrix: no vertical_pack records found"]
    for domain in sorted(matrix):
        capability = matrix[domain]
        if capability.source_count("kit") == 0:
            errors.append(f"domain capability matrix:{domain}: missing saved Kit resource")
        if capability.source_count("scaffold_preset") == 0:
            errors.append(f"domain capability matrix:{domain}: missing scaffold preset")
        maturities = {record.maturity for record in capability.records if record.maturity}
        if len(maturities) > 1:
            errors.append(f"domain capability matrix:{domain}: inconsistent maturities {sorted(maturities)}")

        max_maturity = capability.max_maturity()
        if max_maturity in {"professional", "production-ready"}:
            for field in RESOURCE_GROUP_FIELDS:
                if not capability.refs(field):
                    errors.append(f"domain capability matrix:{domain}: professional pack has no {field}")
            for record in capability.records:
                missing = []
                for field in PROFESSIONAL_VERTICAL_PACK_TEXT_FIELDS:
                    if not record.vertical_text.get(field):
                        missing.append(field)
                for field in PROFESSIONAL_VERTICAL_PACK_LIST_FIELDS + ["model_routes.strong", "model_routes.worker"]:
                    if not record.vertical_lists.get(field):
                        missing.append(field)
                if missing:
                    errors.append(f"domain capability matrix:{domain}: {record.label} missing {', '.join(missing)}")

        if max_maturity == "production-ready":
            required_prefix = PRODUCTION_READY_DOMAIN_TOOL_PREFIXES.get(domain)
            if required_prefix and not any(tool.startswith(required_prefix) for tool in capability.refs("tools")):
                errors.append(f"domain capability matrix:{domain}: production-ready pack missing domain tool prefix {required_prefix!r}")
            errors.extend(validate_production_ready_domain(domain, capability))

        kit_records = [record for record in capability.records if record.source_kind == "kit"]
        preset_records = [record for record in capability.records if record.source_kind == "scaffold_preset"]
        if max_maturity in {"professional", "production-ready"} and kit_records and preset_records:
            kit_refs = {field: set().union(*(record.fields.get(field, []) for record in kit_records)) for field in RESOURCE_GROUP_FIELDS}
            preset_refs = {field: set().union(*(record.fields.get(field, []) for record in preset_records)) for field in RESOURCE_GROUP_FIELDS}
            for field in RESOURCE_GROUP_FIELDS:
                missing_from_preset = sorted(normalize(value) for value in kit_refs[field] if normalize(value) not in {normalize(item) for item in preset_refs[field]})
                missing_from_kit = sorted(normalize(value) for value in preset_refs[field] if normalize(value) not in {normalize(item) for item in kit_refs[field]})
                if missing_from_preset:
                    errors.append(f"domain capability matrix:{domain}: scaffold preset missing {field} from Kit: {missing_from_preset}")
                if missing_from_kit:
                    errors.append(f"domain capability matrix:{domain}: saved Kit missing {field} from scaffold preset: {missing_from_kit}")
    return errors


def validate_production_ready_domain(domain: str, capability: DomainCapability) -> list[str]:
    requirements = PRODUCTION_READY_DOMAIN_REQUIREMENTS.get(domain)
    if not requirements:
        return [f"domain capability matrix:{domain}: production-ready requirements are not declared"]
    errors: list[str] = []
    refs = capability.refs("tools")
    for tool in requirements.get("tools", []):
        if normalize(tool) not in refs:
            errors.append(f"domain capability matrix:{domain}: production-ready pack missing required tool {tool!r}")

    for rel_path, required_terms in requirements.get("files", []):
        path = ROOT / rel_path
        if not path.exists():
            errors.append(f"domain capability matrix:{domain}: production-ready evidence file missing {rel_path}")
            continue
        text = read(path).lower()
        missing = [term for term in required_terms if term.lower() not in text]
        if missing:
            errors.append(f"domain capability matrix:{domain}: {rel_path} missing production-ready evidence terms {missing}")

    contract_text = "\n".join(record.text for record in capability.records).lower()
    for term in requirements.get("contract_terms", []):
        if term.lower() not in contract_text:
            errors.append(f"domain capability matrix:{domain}: production-ready vertical contract missing {term!r}")
    return errors


def summarize_domain_capability_matrix(matrix: dict[str, DomainCapability]) -> str:
    maturities: dict[str, int] = {}
    production_ready = 0
    professional_or_better = 0
    for capability in matrix.values():
        maturity = capability.max_maturity()
        maturities[maturity] = maturities.get(maturity, 0) + 1
        if MATURITY_RANK.get(maturity, 0) >= MATURITY_RANK["professional"]:
            professional_or_better += 1
        if maturity == "production-ready":
            production_ready += 1
    maturity_parts = ", ".join(f"{key}={maturities[key]}" for key in sorted(maturities, key=lambda item: MATURITY_RANK.get(item, 0)))
    return (
        f"domain capability matrix passed ({len(matrix)} domains, "
        f"professional_or_better={professional_or_better}, production_ready={production_ready}, {maturity_parts})"
    )


def is_dynamic_ref(value: str) -> bool:
    value = clean_scalar(value)
    return (
        not value
        or value.startswith("result.")
        or value.startswith("params.")
        or value.startswith("stages.")
        or value.startswith("workflow.")
        or value.startswith("previous.")
    )


def validate_ref(errors: list[str], label: str, key: str, value: str, known: set[str]) -> None:
    value = clean_scalar(value)
    if is_dynamic_ref(value):
        return
    if normalize(value) not in known:
        errors.append(f"{label}: {key} references missing resource {value!r}")


def skill_frontmatter(path: Path) -> str:
    text = read(path)
    if not text.startswith("---"):
        return ""
    parts = text.split("---", 2)
    return parts[1] if len(parts) >= 3 else ""


def validate_skill_files(known: dict[str, set[str]]) -> list[str]:
    errors: list[str] = []
    for path in sorted(ROOT.glob("skills/*/SKILL.md")):
        label = str(path.relative_to(ROOT))
        frontmatter = skill_frontmatter(path)
        if not frontmatter:
            errors.append(f"{label}: missing YAML frontmatter")
            continue
        for value in scalar_values(frontmatter, "preferred_agent"):
            validate_ref(errors, label, "preferred_agent", value, known["agents"])
        for value in list_field(frontmatter, "next_skills"):
            validate_ref(errors, label, "next_skills", value, known["skills"])
        for value in scalar_values(frontmatter, "recommended_workflow"):
            workflow = normalize(value)
            if workflow not in known["workflows"] and workflow not in known["workflow_templates"]:
                errors.append(f"{label}: recommended_workflow references missing workflow/template {value!r}")
        for value in scalar_values(frontmatter, "recommended_team"):
            validate_ref(errors, label, "recommended_team", value, known["team_templates"])
        for item in maps_field(frontmatter, "tools"):
            name = item.get("name", "")
            validate_ref(errors, label, "tools.name", name, known["tools"])
        validate_web_security_browser_tools(errors, label, frontmatter + "\n" + read(path))
    return errors


def validate_team_templates(known: dict[str, set[str]]) -> list[str]:
    errors: list[str] = []
    for path in sorted(ROOT.glob("internal/agent/templates/teams/*.yaml")) + sorted(ROOT.glob("templates/teams/*.yaml")):
        label = str(path.relative_to(ROOT))
        text = read(path)
        for value in scalar_values(text, "recommended_workflow"):
            workflow = normalize(value)
            if workflow not in known["workflows"] and workflow not in known["workflow_templates"]:
                errors.append(f"{label}: recommended_workflow references missing workflow/template {value!r}")
        for value in scalar_values(text, "recommended_entry_agent"):
            validate_ref(errors, label, "recommended_entry_agent", value, known["agents"])
        for role in maps_field(text, "role_templates"):
            validate_ref(errors, label, "role_templates.agent", role.get("agent", ""), known["agents"])
            validate_ref(errors, label, "role_templates.skill", role.get("skill", ""), known["skills"])
        validate_web_security_browser_tools(errors, label, text)
    return errors


def validate_workflow_templates(known: dict[str, set[str]]) -> list[str]:
    errors: list[str] = []
    for path in sorted(ROOT.glob("internal/agent/templates/workflows/*.yaml")) + sorted(ROOT.glob("templates/workflows/*.yaml")):
        label = str(path.relative_to(ROOT))
        text = read(path)
        for value in scalar_values(text, "agent"):
            validate_ref(errors, label, "stage.agent", value, known["agents"])
        for value in scalar_values(text, "skill"):
            validate_ref(errors, label, "stage.skill", value, known["skills"])
        for value in scalar_values(text, "team"):
            validate_ref(errors, label, "params.team", value, known["team_templates"])
        for value in scalar_values(text, "rule") + scalar_values(text, "policy_rule"):
            validate_ref(errors, label, "policy_rule", value, known["policy_rules"])
        validate_web_security_browser_tools(errors, label, text)
    return errors


def main() -> int:
    known = known_resources()
    matrix: dict[str, DomainCapability] = {}
    errors = []
    errors.extend(collect_kit_files(known, matrix))
    errors.extend(collect_kit_scaffold_presets(known, matrix))
    errors.extend(validate_skill_files(known))
    errors.extend(validate_team_templates(known))
    errors.extend(validate_workflow_templates(known))
    errors.extend(validate_domain_capability_matrix(matrix))
    if errors:
        raise AssertionError("resource link validation failed:\n" + "\n".join(f"- {error}" for error in errors))
    print(
        "resource link validation passed "
        f"({len(known['agents'])} agents, {len(known['skills'])} skills, "
        f"{len(known['workflow_templates'])} workflow templates, {len(known['team_templates'])} teams)"
    )
    print(summarize_domain_capability_matrix(matrix))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
