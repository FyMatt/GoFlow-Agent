"""Shared decoding helpers for deterministic Golden Scenario providers."""

from __future__ import annotations

import json


UNTRUSTED_TOOL_DATA_MARKER = "[GoFlow untrusted tool data:"


def decode_tool_result(content: object) -> tuple[dict | None, bool]:
    """Decode a tool result while preserving whether promptguard framed it."""
    if isinstance(content, dict):
        return content, False
    if not isinstance(content, str):
        return None, False
    candidate = content.strip()
    protected = candidate.startswith(UNTRUSTED_TOOL_DATA_MARKER)
    if protected:
        _, separator, candidate = candidate.partition("\n")
        if not separator:
            return None, True
        candidate = candidate.strip()
    try:
        decoded = json.loads(candidate)
    except json.JSONDecodeError:
        return None, protected
    return (decoded if isinstance(decoded, dict) else None), protected
