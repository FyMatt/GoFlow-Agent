#!/usr/bin/env python3
"""Validate public Markdown documentation links and bilingual pairs."""

from __future__ import annotations

import re
from pathlib import Path
from urllib.parse import unquote


ROOT = Path(__file__).resolve().parents[1]
PUBLIC_DOC_ROOTS = [ROOT / "README.md", ROOT / "README.zh-CN.md", ROOT / "docs", ROOT / "examples"]
LINK_RE = re.compile(r"!?\[[^\]]*]\(([^)]+)\)")
PLAN_RE = re.compile(r"\b(?:FULLSTACK|BACKEND|WEB|FRONTEND|MASTER)-[A-Z-]+\.md\b", re.IGNORECASE)


def public_markdown_files() -> list[Path]:
    files: list[Path] = []
    for root in PUBLIC_DOC_ROOTS:
        if root.is_file():
            files.append(root)
        elif root.exists():
            files.extend(path for path in root.rglob("*.md") if path.is_file())
    return sorted(set(files))


def strip_anchor(target: str) -> str:
    return target.split("#", 1)[0]


def local_link_target(raw: str) -> str:
    target = raw.strip()
    if not target or target.startswith(("#", "http://", "https://", "mailto:")):
        return ""
    return strip_anchor(target)


def resolve_link(path: Path, raw: str) -> Path:
    target = unquote(local_link_target(raw))
    return (path.parent / target).resolve()


def has_english_pair(path: Path) -> bool:
    name = path.name
    if name == "README.zh-CN.md":
        return (path.parent / "README.md").exists()
    if name.endswith(".zh-CN.md"):
        return path.with_name(name.replace(".zh-CN.md", ".md")).exists()
    return True


def has_chinese_pair(path: Path) -> bool:
    name = path.name
    if name == "README.md":
        return (path.parent / "README.zh-CN.md").exists()
    if name.endswith(".md") and not name.endswith(".zh-CN.md"):
        return path.with_name(name[:-3] + ".zh-CN.md").exists()
    return True


def validate_links(path: Path, text: str) -> list[str]:
    errors: list[str] = []
    for match in LINK_RE.finditer(text):
        raw = match.group(1)
        target = local_link_target(raw)
        if not target:
            continue
        resolved = resolve_link(path, raw)
        if not resolved.exists():
            errors.append(f"{path.relative_to(ROOT)}: broken link {raw!r}")
            continue
        try:
            resolved.relative_to(ROOT)
        except ValueError:
            errors.append(f"{path.relative_to(ROOT)}: link escapes repository {raw!r}")
    return errors


def validate_bilingual_pair(path: Path) -> list[str]:
    errors: list[str] = []
    rel = path.relative_to(ROOT)
    if path.name == "SKILL.md":
        return errors
    if path.parent.name != "docs" and not path.name.startswith("README"):
        return errors
    if not has_english_pair(path):
        errors.append(f"{rel}: missing English pair")
    if not has_chinese_pair(path):
        errors.append(f"{rel}: missing zh-CN pair")
    return errors


def validate_no_private_plan_refs(path: Path, text: str) -> list[str]:
    return [f"{path.relative_to(ROOT)}: public docs must not reference {match.group(0)}" for match in PLAN_RE.finditer(text)]


def main() -> int:
    errors: list[str] = []
    files = public_markdown_files()
    for path in files:
        text = path.read_text(encoding="utf-8")
        errors.extend(validate_links(path, text))
        errors.extend(validate_bilingual_pair(path))
        errors.extend(validate_no_private_plan_refs(path, text))
    if errors:
        raise AssertionError("documentation validation failed:\n" + "\n".join(f"- {error}" for error in errors))
    print(f"documentation validation passed ({len(files)} markdown files checked)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
