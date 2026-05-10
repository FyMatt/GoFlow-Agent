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


def validate_document(label: str, text: str, known: dict[str, set[str]], *, require_complete: bool) -> list[str]:
    errors: list[str] = []
    fields = {key: list_field(text, key) for key in LIST_FIELDS}
    if require_complete:
        for key in ["agents", "skills", "workflow_templates", "team_templates", "policy_rules"]:
            if not fields[key]:
                errors.append(f"{label}: expected at least one {key} reference")
    for key, refs in fields.items():
        validate_membership(errors, label, key, refs, known[key])

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


def validate_kit_files(known: dict[str, set[str]]) -> list[str]:
    errors: list[str] = []
    for path in sorted(ROOT.glob("kits/*/kit.yaml")):
        label = str(path.relative_to(ROOT))
        errors.extend(validate_document(label, read(path), known, require_complete=True))
    return errors


def validate_kit_scaffold_presets(known: dict[str, set[str]]) -> list[str]:
    path = ROOT / "internal" / "scaffold" / "templates" / "kits" / "scaffolds" / "presets.yaml"
    errors: list[str] = []
    for block in split_preset_blocks(read(path)):
        label = f"{path.relative_to(ROOT)}:{first_named_value(block)}"
        errors.extend(validate_document(label, block, known, require_complete=True))
    return errors


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
    return errors


def main() -> int:
    known = known_resources()
    errors = []
    errors.extend(validate_kit_files(known))
    errors.extend(validate_kit_scaffold_presets(known))
    errors.extend(validate_skill_files(known))
    errors.extend(validate_team_templates(known))
    errors.extend(validate_workflow_templates(known))
    if errors:
        raise AssertionError("resource link validation failed:\n" + "\n".join(f"- {error}" for error in errors))
    print(
        "resource link validation passed "
        f"({len(known['agents'])} agents, {len(known['skills'])} skills, "
        f"{len(known['workflow_templates'])} workflow templates, {len(known['team_templates'])} teams)"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
