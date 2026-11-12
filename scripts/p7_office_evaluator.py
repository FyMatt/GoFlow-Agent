#!/usr/bin/env python3
"""Independent structural evaluator for the P7 Office/WPS fixture."""

from __future__ import annotations

import hashlib
import html
import json
import posixpath
import re
import struct
import zipfile
from pathlib import Path
from typing import Any
from xml.etree import ElementTree

try:
    from .p7_office_fixture import FIXED_DECLARATION, MASTER_DECLARATION
except ImportError:  # direct script execution
    from p7_office_fixture import FIXED_DECLARATION, MASTER_DECLARATION


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def _read_zip(path: Path) -> dict[str, str]:
    with zipfile.ZipFile(path) as archive:
        return {name: archive.read(name).decode("utf-8", errors="replace") for name in archive.namelist()}


def _check_zip(path: Path, required: tuple[str, ...], needles: tuple[str, ...]) -> list[str]:
    errors: list[str] = []
    if not path.is_file():
        return [f"missing {path.as_posix()}"]
    try:
        entries = _read_zip(path)
    except (OSError, zipfile.BadZipFile) as exc:
        return [f"invalid OOXML {path.name}: {exc}"]
    for entry in required:
        if entry not in entries:
            errors.append(f"{path.name}: missing {entry}")
    joined = "\n".join(entries.values())
    for needle in needles:
        if needle not in joined:
            errors.append(f"{path.name}: missing marker {needle}")
    return errors


def _relationship_errors(path: Path) -> list[str]:
    errors: list[str] = []
    try:
        with zipfile.ZipFile(path) as archive:
            names = set(archive.namelist())
            for rel_name in sorted(name for name in names if name.endswith(".rels")):
                try:
                    root = ElementTree.fromstring(archive.read(rel_name))
                except (KeyError, ElementTree.ParseError) as exc:
                    errors.append(f"{path.name}: invalid relationships part {rel_name}: {exc}")
                    continue
                if rel_name == "_rels/.rels":
                    source_dir = ""
                else:
                    prefix, filename = rel_name.rsplit("/_rels/", 1)
                    source_dir = posixpath.dirname(posixpath.join(prefix, filename[:-5]))
                for relation in root:
                    if relation.attrib.get("TargetMode") == "External":
                        continue
                    target = relation.attrib.get("Target", "")
                    resolved = posixpath.normpath(posixpath.join(source_dir, target)).lstrip("/")
                    if not target or resolved not in names:
                        errors.append(f"{path.name}: broken relationship {rel_name} -> {target}")
    except (OSError, zipfile.BadZipFile) as exc:
        errors.append(f"invalid OOXML {path.name}: {exc}")
    return errors


def _visible_xml_text(value: str) -> str:
    return " ".join(html.unescape(re.sub(r"<[^>]+>", " ", value)).split())


def _pdf_errors(path: Path) -> list[str]:
    if not path.is_file():
        return ["report.pdf is missing"]
    data = path.read_bytes()
    errors: list[str] = []
    if not data.startswith(b"%PDF-") or b"%%EOF" not in data[-1024:]:
        errors.append("report.pdf does not contain a complete PDF envelope")
    if len(data) < 4096:
        errors.append(f"report.pdf is a placeholder-sized file: {len(data)} bytes")
    return errors


def _preview_errors(path: Path) -> list[str]:
    try:
        data = path.read_bytes()
    except OSError as exc:
        return [f"preview cannot be read: {path.name}: {exc}"]
    if len(data) < 24 or data[:8] != b"\x89PNG\r\n\x1a\n":
        return [f"preview is not a PNG: {path.name}"]
    width, height = struct.unpack(">II", data[16:24])
    errors: list[str] = []
    if width < 640 or height < 480:
        errors.append(f"preview dimensions are too small: {path.name}={width}x{height}")
    if len(data) < 1024:
        errors.append(f"preview is placeholder-sized: {path.name}={len(data)} bytes")
    return errors


def evaluate_documents_case(workspace: Path) -> dict[str, Any]:
    errors: list[str] = []
    manifest_path = workspace / "fixture-manifest.json"
    if not manifest_path.is_file():
        return {"passed": False, "errors": ["fixture-manifest.json is missing"], "independent": True}
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    for name, expected in manifest.get("templates", {}).items():
        template = workspace / "templates" / name
        if not template.is_file():
            errors.append(f"template removed: {name}")
        elif _sha256(template) != expected.get("sha256"):
            errors.append(f"template changed: {name}")

    docx = workspace / "deliverables" / "report.docx"
    errors.extend(_check_zip(
        docx,
        ("word/document.xml", "word/styles.xml", "word/header1.xml", "word/footer1.xml", "word/settings.xml"),
        ("星河科技", "Alpha 平台安全整改交付报告", "2026-08-18", FIXED_DECLARATION, "TOC", "SEC-001", "SEC-002"),
    ))
    if docx.is_file():
        entries = _read_zip(docx)
        document = entries.get("word/document.xml", "")
        if "{{" in document:
            errors.append("report.docx contains unresolved placeholder")
        if "w:headerReference" not in document or "w:footerReference" not in document:
            errors.append("report.docx lost header/footer references")
        if len(_visible_xml_text(document)) < 500:
            errors.append("report.docx does not contain a substantive delivery report")
        errors.extend(_relationship_errors(docx))

    xlsx = workspace / "deliverables" / "data.xlsx"
    errors.extend(_check_zip(
        xlsx,
        ("xl/workbook.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml", "xl/styles.xml"),
        ("<f>", "autoFilter", "frozen", "pageSetup", "SUM(", "完成率"),
    ))
    if xlsx.is_file():
        entries = _read_zip(xlsx)
        workbook = entries.get("xl/workbook.xml", "")
        sheet = entries.get("xl/worksheets/sheet1.xml", "")
        if "明细数据" not in workbook or "统计汇总" not in workbook:
            errors.append("data.xlsx lost required sheets")
        if "ySplit=\"1\"" not in sheet or "topLeftCell=\"A2\"" not in sheet:
            errors.append("data.xlsx lost frozen header row")
        if "autoFilter" not in sheet or "pageSetup" not in sheet:
            errors.append("data.xlsx lost filter or print setup")
        if "{{" in "\n".join(entries.values()):
            errors.append("data.xlsx contains unresolved placeholder")
        findings = (workspace / "inputs/findings.csv").read_text(encoding="utf-8", errors="replace")
        for marker in ("SEC-001", "SEC-002"):
            if marker in findings and marker not in "\n".join(entries.values()):
                errors.append(f"data.xlsx does not contain source finding {marker}")
        errors.extend(_relationship_errors(xlsx))

    pptx = workspace / "deliverables" / "review.pptx"
    errors.extend(_check_zip(
        pptx,
        (
            "docProps/core.xml", "docProps/app.xml", "ppt/presentation.xml", "ppt/presProps.xml",
            "ppt/viewProps.xml", "ppt/tableStyles.xml", "ppt/slideMasters/slideMaster1.xml",
            "ppt/slideLayouts/slideLayout1.xml", "ppt/slides/slide1.xml", "ppt/slides/slide2.xml",
            "ppt/slides/slide3.xml",
        ),
        ('cx="12192000"', 'cy="6858000"', MASTER_DECLARATION, "星河科技", "整改结果"),
    ))
    if pptx.is_file():
        entries = _read_zip(pptx)
        if any("{{" in entries.get(f"ppt/slides/slide{index}.xml", "") for index in range(1, 4)):
            errors.append("review.pptx contains unresolved placeholder")
        errors.extend(_relationship_errors(pptx))

    pdf = workspace / "deliverables" / "report.pdf"
    errors.extend(_pdf_errors(pdf))
    preview_dir = workspace / "deliverables" / "preview"
    previews = sorted(preview_dir.glob("*.png")) if preview_dir.is_dir() else []
    if not previews:
        errors.append("preview PNGs are missing")
    for preview in previews:
        errors.extend(_preview_errors(preview))

    return {
        "passed": not errors,
        "case_id": "p7-documents-template-suite",
        "domain": "documents",
        "errors": errors,
        "independent": True,
        "template_manifest_verified": not any(error.startswith("template ") for error in errors),
        "verification": [
            {"name": "office-artifact-usability", "passed": not errors, "errors": list(errors)}
        ],
        "deliverables": [
            {"path": str(path.relative_to(workspace)), "sha256": _sha256(path), "bytes": path.stat().st_size}
            for path in (docx, xlsx, pptx, pdf) if path.is_file()
        ],
    }
