#!/usr/bin/env python3
"""Independent structural and factual evaluator for the P7 research case."""

from __future__ import annotations

import csv
import hashlib
import json
from pathlib import Path
from typing import Any
from urllib.parse import urlparse


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _load_json(path: Path, errors: list[str]) -> dict[str, Any]:
    if not path.is_file():
        errors.append(f"missing {path.name}")
        return {}
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"invalid {path.name}: {exc}")
        return {}
    if not isinstance(value, dict):
        errors.append(f"{path.name} must contain an object")
        return {}
    return value


def evaluate_research_case(workspace: Path) -> dict[str, Any]:
    errors: list[str] = []
    catalog = _load_json(workspace / "source-catalog.json", errors)
    evidence_path = workspace / "deliverables/evidence.json"
    evidence = _load_json(evidence_path, errors)
    catalog_sources = catalog.get("sources") if isinstance(catalog.get("sources"), list) else []
    actual_sources = evidence.get("sources") if isinstance(evidence.get("sources"), list) else []
    if len(catalog_sources) < 15:
        errors.append(f"catalog has fewer than 15 sources: {len(catalog_sources)}")

    expected_by_id = {
        str(item.get("id")): item
        for item in catalog_sources
        if isinstance(item, dict) and item.get("id")
    }
    actual_by_id = {
        str(item.get("id")): item
        for item in actual_sources
        if isinstance(item, dict) and item.get("id")
    }
    if set(actual_by_id) != set(expected_by_id):
        missing = sorted(set(expected_by_id) - set(actual_by_id))
        extra = sorted(set(actual_by_id) - set(expected_by_id))
        errors.append(f"source coverage mismatch: missing={missing} extra={extra}")

    slug_to_id = {str(item.get("slug")): source_id for source_id, item in expected_by_id.items()}
    for source_id, expected in expected_by_id.items():
        actual = actual_by_id.get(source_id)
        if not actual:
            continue
        relative = str(expected.get("relative_path") or "")
        source_file = workspace / relative
        if not source_file.is_file():
            errors.append(f"catalog source file missing: {relative}")
            continue
        if actual.get("content_sha256") != _sha256(source_file):
            errors.append(f"source hash mismatch: {source_id}")
        parsed = urlparse(str(actual.get("url") or ""))
        if parsed.scheme not in {"http", "https"} or not parsed.path.endswith(f"/{expected.get('slug')}.html"):
            errors.append(f"invalid source URL mapping: {source_id}")
        if str(actual.get("published_at") or "") != str(expected.get("published_at") or ""):
            errors.append(f"published date mismatch: {source_id}")
        if not str(actual.get("accessed_at") or ""):
            errors.append(f"missing access time: {source_id}")
        if actual.get("http_status") != 200:
            errors.append(f"source was not recovered to 200: {source_id}")
        if not isinstance(actual.get("redirects"), list):
            errors.append(f"redirect history is not structured: {source_id}")
        if str(actual.get("source_type") or "") != str(expected.get("source_type") or ""):
            errors.append(f"source type mismatch: {source_id}")

    duplicate_expectations = {
        "mirror-release": "official-release",
        "mirror-notes": "official-notes",
        "duplicate-faq": "faq",
    }
    for duplicate, original in duplicate_expectations.items():
        duplicate_id = slug_to_id.get(duplicate)
        if duplicate_id and actual_by_id.get(duplicate_id, {}).get("duplicate_of") != original:
            errors.append(f"duplicate was not mapped: {duplicate} -> {original}")

    recovery = evidence.get("recovery") if isinstance(evidence.get("recovery"), dict) else {}
    if recovery.get("source_id") != slug_to_id.get("removed-source"):
        errors.append("recovery evidence references the wrong source")
    statuses = recovery.get("attempt_statuses")
    if statuses != [503, 200] or recovery.get("recovered") is not True:
        errors.append("503 recovery sequence is missing or fabricated")

    claims = evidence.get("claims") if isinstance(evidence.get("claims"), list) else []
    claims_by_id = {
        str(item.get("id")): item for item in claims if isinstance(item, dict) and item.get("id")
    }
    for claim_id in ("C-release", "C-stable", "C-conflict", "C-migration", "C-deployments"):
        if claim_id not in claims_by_id:
            errors.append(f"missing atomic claim: {claim_id}")
    valid_source_ids = set(expected_by_id)
    for claim_id, claim in claims_by_id.items():
        supporting = claim.get("supporting_source_ids")
        opposing = claim.get("opposing_source_ids")
        if not isinstance(supporting, list) or not isinstance(opposing, list):
            errors.append(f"claim source mapping is not structured: {claim_id}")
            continue
        unknown = (set(map(str, supporting)) | set(map(str, opposing))) - valid_source_ids
        if unknown:
            errors.append(f"claim references unknown sources: {claim_id} {sorted(unknown)}")
    conflict = claims_by_id.get("C-conflict", {})
    if not conflict.get("supporting_source_ids") or not conflict.get("opposing_source_ids"):
        errors.append("conflict claim does not preserve both sides")
    if "无法确认" not in str(conflict.get("status") or ""):
        errors.append("conflict claim was over-resolved")
    deployments = claims_by_id.get("C-deployments", {})
    if deployments.get("supporting_source_ids") or "无法确认" not in str(deployments.get("status") or ""):
        errors.append("unsupported deployment count was asserted")

    stable = claims_by_id.get("C-stable", {})
    stable_sources = [slug_to_id.get("standard-stable"), slug_to_id.get("vendor-support")]
    expected_stable = {source_id for source_id in stable_sources if source_id}
    if set(map(str, stable.get("supporting_source_ids") or [])) != expected_stable:
        errors.append("stable-state source mapping does not follow the current catalog")
    expected_confidence = "中" if len(expected_stable) >= 2 else "低"
    if stable.get("confidence") != expected_confidence:
        errors.append("stable-state confidence was not recomputed after source mutation")

    csv_path = workspace / "deliverables/claims.csv"
    csv_rows: dict[str, dict[str, str]] = {}
    if not csv_path.is_file():
        errors.append("missing claims.csv")
    else:
        try:
            with csv_path.open(encoding="utf-8-sig", newline="") as stream:
                reader = csv.DictReader(stream)
                required = {"claim_id", "claim", "supporting_source_ids", "opposing_source_ids", "status", "confidence", "limitation"}
                if not reader.fieldnames or set(reader.fieldnames) != required:
                    errors.append("claims.csv columns do not match the contract")
                csv_rows = {str(row.get("claim_id")): row for row in reader if row.get("claim_id")}
        except (OSError, csv.Error) as exc:
            errors.append(f"invalid claims.csv: {exc}")
    if set(csv_rows) != set(claims_by_id):
        errors.append("claims.csv and evidence.json claim IDs differ")
    for claim_id, claim in claims_by_id.items():
        row = csv_rows.get(claim_id)
        if not row:
            continue
        expected_fields = {
            "claim": str(claim.get("claim") or ""),
            "supporting_source_ids": ";".join(map(str, claim.get("supporting_source_ids") or [])),
            "opposing_source_ids": ";".join(map(str, claim.get("opposing_source_ids") or [])),
            "status": str(claim.get("status") or ""),
            "confidence": str(claim.get("confidence") or ""),
            "limitation": str(claim.get("limitation") or ""),
        }
        if any(row.get(key) != value for key, value in expected_fields.items()):
            errors.append(f"claims.csv differs from evidence.json: {claim_id}")

    report_path = workspace / "deliverables/report.md"
    report = report_path.read_text(encoding="utf-8", errors="replace") if report_path.is_file() else ""
    if not report:
        errors.append("missing report.md")
    else:
        for marker in (str(catalog.get("topic") or ""), "来源去重", "冲突", "removed-source", "503", "无法确认"):
            if marker and marker not in report:
                errors.append(f"report.md missing marker: {marker}")

    deliverables = []
    for path in (report_path, evidence_path, csv_path):
        if path.is_file():
            deliverables.append({"path": path.relative_to(workspace).as_posix(), "sha256": _sha256(path), "bytes": path.stat().st_size})
    return {
        "passed": not errors,
        "case_id": "p7-research-source-verification",
        "domain": "research",
        "errors": errors,
        "independent": True,
        "source_count": len(actual_by_id),
        "claim_count": len(claims_by_id),
        "verification": [
            {"name": "research-artifact-contract", "passed": not errors, "errors": list(errors)}
        ],
        "deliverables": deliverables,
    }
