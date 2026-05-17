#!/usr/bin/env python3
"""Fail release preflight when common secret patterns are present."""

from __future__ import annotations

import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]

EXCLUDED_DIRS = {
    ".git",
    ".goflow",
    ".codex",
    ".claude",
    ".gocache",
    ".gomodcache",
    ".gotmp",
    ".pytest_cache",
    ".venv",
    "__pycache__",
    "bin",
    "build",
    "dist",
    "node_modules",
    "tmp",
    "venv",
    "workspace",
}
SKIP_EXTS = {
    ".7z",
    ".bin",
    ".dll",
    ".exe",
    ".gif",
    ".gz",
    ".ico",
    ".jpeg",
    ".jpg",
    ".pdf",
    ".png",
    ".so",
    ".tar",
    ".webp",
    ".zip",
}
MAX_TEXT_BYTES = 2 * 1024 * 1024

SECRET_PATTERNS: list[tuple[str, re.Pattern[str]]] = [
    ("OpenAI-compatible API key", re.compile(r"sk-[A-Za-z0-9_-]{20,}")),
    ("GitHub token", re.compile(r"(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}")),
    ("GitHub fine-grained token", re.compile(r"github_pat_[A-Za-z0-9_]{20,}")),
    ("AWS access key id", re.compile(r"AKIA[0-9A-Z]{16}")),
]
PROVIDER_API_KEY_RE = re.compile(r"^\s*api_key:\s*(?P<value>.+?)\s*(?:#.*)?$")


def should_skip(path: Path) -> bool:
    rel = path.relative_to(ROOT)
    if any(part in EXCLUDED_DIRS for part in rel.parts):
        return True
    if path.suffix.lower() in SKIP_EXTS:
        return True
    try:
        return path.stat().st_size > MAX_TEXT_BYTES
    except OSError:
        return True


def text_files() -> list[Path]:
    return sorted(path for path in ROOT.rglob("*") if path.is_file() and not should_skip(path))


def scan_secret_patterns() -> list[str]:
    findings: list[str] = []
    for path in text_files():
        rel = path.relative_to(ROOT)
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        for line_no, line in enumerate(text.splitlines(), start=1):
            for label, pattern in SECRET_PATTERNS:
                if pattern.search(line):
                    findings.append(f"{rel}:{line_no}: possible {label}")
    return findings


def unquote(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
        return value[1:-1].strip()
    return value


def scan_provider_literals() -> list[str]:
    findings: list[str] = []
    provider_dir = ROOT / "configs" / "providers"
    if not provider_dir.exists():
        return findings
    for path in sorted(provider_dir.glob("*.yaml")):
        rel = path.relative_to(ROOT)
        text = path.read_text(encoding="utf-8")
        for line_no, line in enumerate(text.splitlines(), start=1):
            match = PROVIDER_API_KEY_RE.match(line)
            if not match:
                continue
            value = unquote(match.group("value"))
            if not value or value.startswith("${"):
                continue
            findings.append(f"{rel}:{line_no}: provider api_key should use an environment reference")
    return findings


def main() -> int:
    findings = scan_secret_patterns()
    findings.extend(scan_provider_literals())
    if findings:
        details = "\n".join(f"- {finding}" for finding in findings)
        raise AssertionError(
            "secret validation failed:\n"
            f"{details}\n"
            "Remove real secrets from tracked files. If a real key was exposed, rotate it with the provider."
        )
    print("secret validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
