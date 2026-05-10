#!/usr/bin/env python3
"""Validate static Web Studio translation keys.

The embedded UI intentionally has no build step, so missed i18n keys otherwise
show up only when a user opens a specific panel. This script checks the static
keys that can be resolved without executing browser code.
"""

from __future__ import annotations

import ast
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ASSET_ROOT = ROOT / "internal" / "api" / "web" / "assets"
I18N_FILE = ASSET_ROOT / "i18n.js"


DICT_ENTRY_RE = re.compile(r'"([^"\\]*(?:\\.[^"\\]*)*)"\s*:')
DICT_STRING_ENTRY_RE = re.compile(r'"([^"\\]*(?:\\.[^"\\]*)*)"\s*:\s*"([^"\\]*(?:\\.[^"\\]*)*)"')
T_CALL_RE = re.compile(r"""\bt\(\s*["']([^"'{}$`]+)["']""")
STATIC_ATTR_RE = re.compile(r"""data-i18n(?:-[a-z-]+)?=["']([^"'{}$`]+)["']""")
ZH_ALLOWED_ASCII_TERMS = {
    "API",
    "CI",
    "CLI",
    "CRUD",
    "Docker",
    "GitHub",
    "Go",
    "GoFlow",
    "HTTP",
    "JSON",
    "Kit",
    "MCP",
    "OpenAI",
    "Podman",
    "SBOM",
    "SSE",
    "Sigstore",
    "Studio",
    "YAML",
}
ZH_ALLOWED_ASCII_LOWER = {term.lower() for term in ZH_ALLOWED_ASCII_TERMS}
ZH_UNTRANSLATED_WORD_RE = re.compile(
    r"\b(Agent|Agents|Skill|Skills|Workflow|Workflows|Team|Teams|Policy|Policies|Kit|Kits|Tool|Tools|"
    r"Studio|Console|Resource|Resources|Provider|Providers|Node|Nodes|Stage|Stages|Settings|Workspace|"
    r"Run|Runs|Approval|Approvals|Release|Update|Check|Create|Open|Save|Edit|Delete|Validate|Import|Export)\b"
)


def js_string(raw: str) -> str:
    return ast.literal_eval(f'"{raw}"')


def block_after(text: str, marker: str, start: int) -> tuple[str, int]:
    marker_at = text.find(marker, start)
    if marker_at < 0:
        return "", -1
    brace_at = text.find("{", marker_at)
    if brace_at < 0:
        raise AssertionError(f"{marker} has no object body")
    depth = 0
    in_string = ""
    escaped = False
    for index in range(brace_at, len(text)):
        ch = text[index]
        if in_string:
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == in_string:
                in_string = ""
            continue
        if ch in ('"', "'", "`"):
            in_string = ch
            continue
        if ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                return text[brace_at + 1 : index], index + 1
    raise AssertionError(f"{marker} object body is not closed")


def dictionary_keys(language: str) -> set[str]:
    text = I18N_FILE.read_text(encoding="utf-8")
    keys: set[str] = set()
    if language == "en":
        block, _ = block_after(text, "en:", 0)
        keys.update(js_string(match.group(1)) for match in DICT_ENTRY_RE.finditer(block))
    elif language == "zh":
        block, _ = block_after(text, "zh:", 0)
        keys.update(js_string(match.group(1)) for match in DICT_ENTRY_RE.finditer(block))
    else:
        raise ValueError(language)

    offset = 0
    marker = f"Object.assign(dictionaries.{language}"
    while True:
        block, next_offset = block_after(text, marker, offset)
        if next_offset < 0:
            break
        keys.update(js_string(match.group(1)) for match in DICT_ENTRY_RE.finditer(block))
        offset = next_offset
    return keys


def dictionary_strings(language: str) -> dict[str, str]:
    text = I18N_FILE.read_text(encoding="utf-8")
    values: dict[str, str] = {}
    if language == "en":
        block, _ = block_after(text, "en:", 0)
        values.update(
            (js_string(match.group(1)), js_string(match.group(2))) for match in DICT_STRING_ENTRY_RE.finditer(block)
        )
    elif language == "zh":
        block, _ = block_after(text, "zh:", 0)
        values.update(
            (js_string(match.group(1)), js_string(match.group(2))) for match in DICT_STRING_ENTRY_RE.finditer(block)
        )
    else:
        raise ValueError(language)

    offset = 0
    marker = f"Object.assign(dictionaries.{language}"
    while True:
        block, next_offset = block_after(text, marker, offset)
        if next_offset < 0:
            break
        values.update(
            (js_string(match.group(1)), js_string(match.group(2))) for match in DICT_STRING_ENTRY_RE.finditer(block)
        )
        offset = next_offset
    return values


def untranslated_zh_strings(values: dict[str, str]) -> list[str]:
    issues: list[str] = []
    for key, value in sorted(values.items()):
        if not value or not any("\u4e00" <= ch <= "\u9fff" for ch in value):
            continue
        matches = [match.group(1) for match in ZH_UNTRANSLATED_WORD_RE.finditer(value)]
        blocked = [
            word
            for word in matches
            if word.lower() not in ZH_ALLOWED_ASCII_LOWER and not key.endswith("ExampleRequest")
        ]
        if blocked:
            issues.append(f"  - {key}: {value} ({', '.join(blocked)})")
    return issues


def static_key_references() -> dict[str, set[str]]:
    references: dict[str, set[str]] = {}
    for path in sorted(ASSET_ROOT.rglob("*.js")):
        if path == I18N_FILE:
            continue
        text = path.read_text(encoding="utf-8")
        rel = str(path.relative_to(ROOT))
        for regex in (T_CALL_RE, STATIC_ATTR_RE):
            for match in regex.finditer(text):
                references.setdefault(js_string(match.group(1)), set()).add(rel)
    return references


def main() -> int:
    en_keys = dictionary_keys("en")
    zh_keys = dictionary_keys("zh")
    zh_strings = dictionary_strings("zh")
    references = static_key_references()
    missing_en = sorted(key for key in references if key not in en_keys)
    missing_zh = sorted(key for key in references if key not in zh_keys)
    untranslated_zh = untranslated_zh_strings(zh_strings)
    if missing_en or missing_zh or untranslated_zh:
        lines = []
        if missing_en:
            lines.append("missing English keys:")
            lines.extend(f"  - {key} ({', '.join(sorted(references[key]))})" for key in missing_en)
        if missing_zh:
            lines.append("missing Chinese keys:")
            lines.extend(f"  - {key} ({', '.join(sorted(references[key]))})" for key in missing_zh)
        if untranslated_zh:
            lines.append("Chinese values contain likely untranslated UI words:")
            lines.extend(untranslated_zh)
        raise AssertionError("\n".join(lines))
    print(f"web i18n validation passed ({len(references)} static keys checked)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
