import ast
import base64
import hashlib
import json
import math
import os
import re
import struct
import string
import sys
from urllib.parse import unquote, urlparse
from pathlib import Path


MAX_TEXT_BYTES = 1024 * 1024
MAX_BINARY_BYTES = 8 * 1024 * 1024
MAX_EXTRACT_BYTES = 4096


def workspace_root() -> Path:
    value = os.environ.get("GOFLOW_WORKSPACE_ROOT", "").strip()
    if value:
        return Path(value).resolve()
    return Path.cwd().resolve()


ROOT = workspace_root()


TOOLS = [
    {
        "name": "write_note",
        "description": "Write a UTF-8 text note into the workspace.",
        "kind": "write",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "content": {"type": "string"},
            },
            "required": ["path", "content"],
            "additionalProperties": False,
        },
    },
    {
        "name": "read_note",
        "description": "Read a UTF-8 text note from the workspace.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "python_ast_summary",
        "description": "Parse a Python source file and return imports, classes, functions, and top-level structure.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "json_query",
        "description": "Read a JSON file and optionally return a dotted path inside the JSON document.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "query": {"type": "string"},
            },
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "binary_file_info",
        "description": "Return binary metadata including size, SHA-256, magic bytes, and entropy sample.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "binary_strings",
        "description": "Extract ASCII and UTF-16LE strings from a binary file inside the workspace.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "min_length": {"type": "integer", "minimum": 3, "maximum": 64},
                "max_results": {"type": "integer", "minimum": 1, "maximum": 1000},
            },
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "hex_preview",
        "description": "Return a bounded hex/ascii preview of a binary file.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "offset": {"type": "integer", "minimum": 0},
                "length": {"type": "integer", "minimum": 1, "maximum": 4096},
            },
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "binary_format_summary",
        "description": "Identify a binary format and return bounded header, section, import/export, and entry-point hints without executing the file.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "binary_entropy_map",
        "description": "Return a bounded per-block entropy map and packing/compression hints for static binary triage.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "block_size": {"type": "integer", "minimum": 256, "maximum": 65536},
                "max_blocks": {"type": "integer", "minimum": 1, "maximum": 256},
            },
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "binary_symbol_hints",
        "description": "Extract bounded symbol-like strings, import/export hints, suspicious API names, URLs, and credential markers from a binary without executing it.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "max_results": {"type": "integer", "minimum": 1, "maximum": 500},
            },
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "binary_extract_window",
        "description": "Return a bounded hex/base64 artifact window from a binary for offset-specific evidence capture.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {
                "path": {"type": "string"},
                "offset": {"type": "integer", "minimum": 0},
                "length": {"type": "integer", "minimum": 1, "maximum": 4096},
                "encoding": {"type": "string", "enum": ["hex", "base64"]},
            },
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "markdown_link_check",
        "description": "Check local Markdown headings and links without network access, reporting missing files or anchors.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "support_case_summary",
        "description": "Summarize a support case from a JSON or text file with priority, customer impact, escalation, privacy, and missing-information hints.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
            "additionalProperties": False,
        },
    },
    {
        "name": "redaction_check",
        "description": "Detect common sensitive data patterns in a text, JSON, Markdown, or support-case file and return redacted evidence counts.",
        "kind": "read",
        "input_schema": {
            "type": "object",
            "properties": {"path": {"type": "string"}},
            "required": ["path"],
            "additionalProperties": False,
        },
    },
]


def resolve_path(raw: str, must_exist: bool = True) -> Path:
    if not raw or not str(raw).strip():
        raise ValueError("path is required")
    candidate = Path(raw)
    if not candidate.is_absolute():
        candidate = ROOT / candidate
    candidate = candidate.resolve(strict=False)
    try:
        candidate.relative_to(ROOT)
    except ValueError as exc:
        raise ValueError(f'path "{raw}" escapes workspace root') from exc
    if must_exist:
        if not candidate.exists():
            raise FileNotFoundError(str(candidate))
        candidate = candidate.resolve(strict=True)
        try:
            candidate.relative_to(ROOT)
        except ValueError as exc:
            raise ValueError(f'path "{raw}" resolves outside workspace root') from exc
        return candidate
    parent = candidate.parent
    while not parent.exists():
        if parent == parent.parent:
            raise ValueError(f'path "{raw}" has no existing parent inside workspace')
        parent = parent.parent
    parent = parent.resolve(strict=True)
    try:
        parent.relative_to(ROOT)
    except ValueError as exc:
        raise ValueError(f'path "{raw}" resolves outside workspace root') from exc
    return candidate


def read_limited(path: Path, max_bytes: int) -> bytes:
    size = path.stat().st_size
    if size > max_bytes:
        raise ValueError(f"file too large: {size} bytes exceeds {max_bytes}")
    return path.read_bytes()


def read_text_limited(path: Path, max_bytes: int) -> str:
    return read_limited(path, max_bytes).decode("utf-8-sig")


def handle_write_note(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""), must_exist=False)
    content = arguments.get("content")
    if not isinstance(content, str):
        raise ValueError("content must be a string")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")
    return {"path": str(path), "relative_path": relative_path(path), "bytes_written": len(content.encode("utf-8"))}


def handle_read_note(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    content = read_text_limited(path, MAX_TEXT_BYTES)
    return {"path": str(path), "relative_path": relative_path(path), "content": content}


def handle_python_ast_summary(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    source = read_text_limited(path, MAX_TEXT_BYTES)
    tree = ast.parse(source, filename=str(path))
    imports = []
    functions = []
    classes = []
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            imports.extend(alias.name for alias in node.names)
        elif isinstance(node, ast.ImportFrom):
            module = "." * node.level + (node.module or "")
            imports.extend(f"{module}.{alias.name}".strip(".") for alias in node.names)
        elif isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            functions.append({"name": node.name, "line": node.lineno, "async": isinstance(node, ast.AsyncFunctionDef), "args": [arg.arg for arg in node.args.args]})
        elif isinstance(node, ast.ClassDef):
            methods = [item.name for item in node.body if isinstance(item, (ast.FunctionDef, ast.AsyncFunctionDef))]
            classes.append({"name": node.name, "line": node.lineno, "methods": methods})
    return {
        "path": str(path),
        "relative_path": relative_path(path),
        "module_docstring": ast.get_docstring(tree) or "",
        "imports": sorted(set(imports)),
        "classes": sorted(classes, key=lambda item: item["line"]),
        "functions": sorted(functions, key=lambda item: item["line"]),
    }


def handle_json_query(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    data = json.loads(read_text_limited(path, MAX_TEXT_BYTES))
    query = str(arguments.get("query", "") or "").strip()
    value = data
    if query:
        value = select_json_path(data, query)
    return {"path": str(path), "relative_path": relative_path(path), "query": query, "value": value}


def handle_binary_file_info(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    data = read_limited(path, MAX_BINARY_BYTES)
    sample = data[:65536]
    return {
        "path": str(path),
        "relative_path": relative_path(path),
        "size": len(data),
        "sha256": hashlib.sha256(data).hexdigest(),
        "magic_hex": data[:32].hex(" "),
        "magic_ascii": printable_ascii(data[:32]),
        "entropy_sample": round(shannon_entropy(sample), 4),
        "looks_text": looks_text(sample),
    }


def handle_binary_strings(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    data = read_limited(path, MAX_BINARY_BYTES)
    min_length = bounded_int(arguments.get("min_length", 4), 3, 64)
    max_results = bounded_int(arguments.get("max_results", 200), 1, 1000)
    strings_found = []
    strings_found.extend(extract_ascii_strings(data, min_length, max_results))
    if len(strings_found) < max_results:
        strings_found.extend(extract_utf16le_strings(data, min_length, max_results - len(strings_found)))
    return {"path": str(path), "relative_path": relative_path(path), "min_length": min_length, "max_results": max_results, "strings": strings_found[:max_results]}


def handle_hex_preview(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    offset = bounded_int(arguments.get("offset", 0), 0, MAX_BINARY_BYTES)
    length = bounded_int(arguments.get("length", 256), 1, 4096)
    with path.open("rb") as handle:
        handle.seek(offset)
        data = handle.read(length)
    lines = []
    for index in range(0, len(data), 16):
        chunk = data[index : index + 16]
        lines.append({"offset": offset + index, "hex": chunk.hex(" "), "ascii": printable_ascii(chunk)})
    return {"path": str(path), "relative_path": relative_path(path), "offset": offset, "length": len(data), "lines": lines}


def handle_binary_format_summary(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    data = read_limited(path, MAX_BINARY_BYTES)
    summary = identify_binary_format(data)
    summary.update(
        {
            "path": str(path),
            "relative_path": relative_path(path),
            "size": len(data),
            "sha256": hashlib.sha256(data).hexdigest(),
            "magic_hex": data[:16].hex(" "),
            "static_only": True,
            "execution_performed": False,
        }
    )
    return summary


def handle_binary_entropy_map(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    data = read_limited(path, MAX_BINARY_BYTES)
    block_size = bounded_int(arguments.get("block_size", 4096), 256, 65536)
    max_blocks = bounded_int(arguments.get("max_blocks", 64), 1, 256)
    blocks = []
    high_entropy_blocks = 0
    low_entropy_blocks = 0
    for block_index, offset in enumerate(range(0, len(data), block_size)):
        if block_index >= max_blocks:
            break
        chunk = data[offset : offset + block_size]
        entropy = round(shannon_entropy(chunk), 4)
        if entropy >= 7.2:
            high_entropy_blocks += 1
        if entropy <= 1.0 and chunk:
            low_entropy_blocks += 1
        blocks.append(
            {
                "offset": offset,
                "length": len(chunk),
                "entropy": entropy,
                "printable_ratio": round(printable_ratio(chunk), 4),
                "sample_ascii": printable_ascii(chunk[:32]),
            }
        )
    coverage_bytes = min(len(data), block_size * max_blocks)
    high_ratio = high_entropy_blocks / len(blocks) if blocks else 0.0
    hints = []
    if high_ratio >= 0.6 and len(data) >= block_size:
        hints.append("many high-entropy blocks; packed, compressed, or encrypted content is possible")
    if low_entropy_blocks:
        hints.append("low-entropy padding or sparse regions are present")
    if coverage_bytes < len(data):
        hints.append("entropy map is truncated by max_blocks; increase max_blocks for wider coverage")
    return {
        "path": str(path),
        "relative_path": relative_path(path),
        "size": len(data),
        "block_size": block_size,
        "blocks_analyzed": len(blocks),
        "coverage_bytes": coverage_bytes,
        "truncated": coverage_bytes < len(data),
        "high_entropy_blocks": high_entropy_blocks,
        "low_entropy_blocks": low_entropy_blocks,
        "packing_hints": hints,
        "blocks": blocks,
    }


def handle_binary_symbol_hints(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    data = read_limited(path, MAX_BINARY_BYTES)
    max_results = bounded_int(arguments.get("max_results", 100), 1, 500)
    strings_found = extract_ascii_strings(data, 4, max_results * 4)
    if len(strings_found) < max_results * 4:
        strings_found.extend(extract_utf16le_strings(data, 4, max_results * 4 - len(strings_found)))

    symbol_like = []
    suspicious = []
    urls = []
    credential_markers = []
    import_export_hints = []
    seen = set()
    for item in strings_found:
        value = item["value"]
        lower = value.lower()
        if value in seen:
            continue
        seen.add(value)
        record = {"offset": item["offset"], "encoding": item["encoding"], "value": value}
        if looks_symbol_like(value) and len(symbol_like) < max_results:
            symbol_like.append(record)
        if any(marker in lower for marker in SUSPICIOUS_BINARY_MARKERS) and len(suspicious) < max_results:
            suspicious.append(record)
        if ("http://" in lower or "https://" in lower) and len(urls) < max_results:
            urls.append(record)
        if any(marker in lower for marker in CREDENTIAL_MARKERS) and len(credential_markers) < max_results:
            credential_markers.append(redacted_string_record(record))
        if any(marker in lower for marker in IMPORT_EXPORT_MARKERS) and len(import_export_hints) < max_results:
            import_export_hints.append(record)

    return {
        "path": str(path),
        "relative_path": relative_path(path),
        "max_results": max_results,
        "symbol_like": symbol_like[:max_results],
        "suspicious_apis": suspicious[:max_results],
        "urls": urls[:max_results],
        "credential_markers": credential_markers[:max_results],
        "import_export_hints": import_export_hints[:max_results],
        "static_only": True,
        "execution_performed": False,
    }


def handle_binary_extract_window(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    offset = bounded_int(arguments.get("offset", 0), 0, MAX_BINARY_BYTES)
    length = bounded_int(arguments.get("length", 256), 1, MAX_EXTRACT_BYTES)
    encoding = str(arguments.get("encoding", "hex") or "hex").lower()
    if encoding not in {"hex", "base64"}:
        raise ValueError("encoding must be hex or base64")
    with path.open("rb") as handle:
        handle.seek(offset)
        data = handle.read(length)
    if encoding == "base64":
        encoded = base64.b64encode(data).decode("ascii")
    else:
        encoded = data.hex(" ")
    return {
        "path": str(path),
        "relative_path": relative_path(path),
        "offset": offset,
        "length": len(data),
        "encoding": encoding,
        "content": encoded,
        "sha256": hashlib.sha256(data).hexdigest(),
        "ascii_preview": printable_ascii(data[:128]),
        "bounded_artifact": True,
    }


def handle_markdown_link_check(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    text = read_text_limited(path, MAX_TEXT_BYTES)
    headings = markdown_heading_anchors(text)
    links = []
    for match in re.finditer(r"(?<!!)\[[^\]]+\]\(([^)]+)\)", text):
        raw_target = match.group(1).strip()
        if not raw_target or raw_target.startswith("mailto:"):
            continue
        line = text.count("\n", 0, match.start()) + 1
        links.append(check_markdown_link(path, raw_target, headings, line))
    broken = [item for item in links if item["status"] != "ok"]
    return {
        "path": str(path),
        "relative_path": relative_path(path),
        "heading_count": len(headings),
        "link_count": len(links),
        "broken_count": len(broken),
        "links": links,
        "publish_ready": len(broken) == 0,
        "network_checked": False,
    }


def handle_support_case_summary(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    text = read_text_limited(path, MAX_TEXT_BYTES)
    case = parse_case_text(text, path.suffix.lower())
    combined = json.dumps(case, ensure_ascii=False) if isinstance(case, (dict, list)) else str(case)
    lowered = combined.lower()
    fields = case if isinstance(case, dict) else {}
    summary = {
        "path": str(path),
        "relative_path": relative_path(path),
        "case_id": first_present(fields, ["id", "case_id", "ticket_id", "ticket"]) or "",
        "product_area": first_present(fields, ["product_area", "product", "component", "service"]) or infer_product_area(lowered),
        "priority": first_present(fields, ["priority", "severity", "sla"]) or infer_priority(lowered),
        "customer_impact": first_present(fields, ["customer_impact", "impact"]) or infer_customer_impact(combined),
        "privacy_flags": detect_privacy_flags(combined),
        "escalation_flags": detect_escalation_flags(lowered),
        "missing_information": missing_support_information(fields, lowered),
        "customer_visible_notes": extract_named_text(fields, ["customer_visible_notes", "customer_reply", "reply", "message"]),
        "internal_notes": extract_named_text(fields, ["internal_notes", "notes", "analysis"]),
        "source_attribution": "local workspace file",
    }
    summary["needs_escalation"] = bool(summary["escalation_flags"])
    summary["needs_redaction"] = bool(summary["privacy_flags"])
    return summary


def handle_redaction_check(arguments: dict) -> dict:
    path = resolve_path(arguments.get("path", ""))
    text = read_text_limited(path, MAX_TEXT_BYTES)
    findings = []
    for name, pattern in REDACTION_PATTERNS:
        matches = list(pattern.finditer(text))
        examples = []
        for match in matches[:5]:
            examples.append({"line": text.count("\n", 0, match.start()) + 1, "sample": redact_value(match.group(0))})
        findings.append({"type": name, "count": len(matches), "examples": examples})
    risky = [item for item in findings if item["count"] > 0]
    return {
        "path": str(path),
        "relative_path": relative_path(path),
        "findings": risky,
        "finding_count": sum(item["count"] for item in risky),
        "redaction_required": bool(risky),
    }


def select_json_path(data, query: str):
    value = data
    for part in query.split("."):
        if part == "":
            continue
        if isinstance(value, list):
            value = value[int(part)]
        elif isinstance(value, dict):
            value = value[part]
        else:
            raise ValueError(f'cannot select "{part}" from non-container value')
    return value


def extract_ascii_strings(data: bytes, min_length: int, limit: int) -> list[dict]:
    results = []
    current = bytearray()
    start = 0
    printable = set(bytes(string.printable, "ascii")) - {0x0b, 0x0c}
    for index, byte in enumerate(data + b"\x00"):
        if byte in printable and byte not in b"\r\n\t":
            if not current:
                start = index
            current.append(byte)
            continue
        if len(current) >= min_length:
            results.append({"encoding": "ascii", "offset": start, "value": current.decode("ascii", errors="replace")})
            if len(results) >= limit:
                return results
        current = bytearray()
    return results


def extract_utf16le_strings(data: bytes, min_length: int, limit: int) -> list[dict]:
    results = []
    pattern = re.compile((rb"(?:[\x20-\x7e]\x00){" + str(min_length).encode("ascii") + rb",}"))
    for match in pattern.finditer(data):
        value = match.group(0).decode("utf-16le", errors="replace")
        results.append({"encoding": "utf-16le", "offset": match.start(), "value": value})
        if len(results) >= limit:
            break
    return results


def identify_binary_format(data: bytes) -> dict:
    if data.startswith(b"MZ"):
        return parse_pe_summary(data)
    if data.startswith(b"\x7fELF"):
        return parse_elf_summary(data)
    if data[:4] in {b"\xfe\xed\xfa\xce", b"\xfe\xed\xfa\xcf", b"\xce\xfa\xed\xfe", b"\xcf\xfa\xed\xfe"}:
        return parse_macho_summary(data)
    return {"format": "unknown", "sections": [], "imports": [], "exports": [], "entry_point": None, "warnings": ["unknown file format"]}


def parse_pe_summary(data: bytes) -> dict:
    warnings = []
    sections = []
    imports = []
    exports = []
    entry_point = None
    try:
        if len(data) < 0x40:
            raise ValueError("truncated DOS header")
        pe_offset = read_u32(data, 0x3C)
        if pe_offset + 24 > len(data) or data[pe_offset : pe_offset + 4] != b"PE\x00\x00":
            raise ValueError("missing PE signature")
        machine = read_u16(data, pe_offset + 4)
        section_count = read_u16(data, pe_offset + 6)
        opt_size = read_u16(data, pe_offset + 20)
        opt_offset = pe_offset + 24
        magic = read_u16(data, opt_offset) if opt_offset + 2 <= len(data) else 0
        pe32_plus = magic == 0x20B
        if opt_offset + opt_size > len(data):
            warnings.append("optional header is truncated")
        if opt_offset + 20 <= len(data):
            entry_rva = read_u32(data, opt_offset + 16)
            entry_point = {"rva": entry_rva}
        data_directory_offset = opt_offset + (112 if pe32_plus else 96)
        import_rva = import_size = export_rva = export_size = 0
        if data_directory_offset + 16 <= len(data):
            export_rva = read_u32(data, data_directory_offset)
            export_size = read_u32(data, data_directory_offset + 4)
            import_rva = read_u32(data, data_directory_offset + 8)
            import_size = read_u32(data, data_directory_offset + 12)
        section_offset = opt_offset + opt_size
        for index in range(min(section_count, 64)):
            offset = section_offset + index * 40
            if offset + 40 > len(data):
                warnings.append("section table is truncated")
                break
            name = data[offset : offset + 8].split(b"\x00", 1)[0].decode("ascii", errors="replace")
            virtual_size = read_u32(data, offset + 8)
            virtual_address = read_u32(data, offset + 12)
            raw_size = read_u32(data, offset + 16)
            raw_pointer = read_u32(data, offset + 20)
            characteristics = read_u32(data, offset + 36)
            raw = data[raw_pointer : raw_pointer + min(raw_size, 65536)] if raw_pointer < len(data) else b""
            sections.append(
                {
                    "name": name,
                    "virtual_address": virtual_address,
                    "virtual_size": virtual_size,
                    "raw_pointer": raw_pointer,
                    "raw_size": raw_size,
                    "entropy_sample": round(shannon_entropy(raw), 4),
                    "characteristics": f"0x{characteristics:08x}",
                }
            )
        if import_rva:
            imports = parse_pe_imports(data, sections, import_rva, limit=128)
        if export_rva:
            exports = parse_pe_exports(data, sections, export_rva, export_size, limit=128)
    except (ValueError, struct.error) as exc:
        warnings.append(str(exc))
    return {
        "format": "pe",
        "architecture": pe_machine_name(machine) if "machine" in locals() else "unknown",
        "sections": sections,
        "imports": imports,
        "exports": exports,
        "entry_point": entry_point,
        "warnings": warnings,
    }


def parse_elf_summary(data: bytes) -> dict:
    warnings = []
    sections = []
    symbols = []
    entry_point = None
    try:
        if len(data) < 64:
            raise ValueError("truncated ELF header")
        elf_class = data[4]
        endian = "<" if data[5] == 1 else ">"
        if elf_class == 1:
            header = struct.unpack_from(endian + "HHIIIIIHHHHHH", data, 16)
            machine = header[1]
            entry_point = {"virtual_address": header[3]}
            shoff, shentsize, shnum, shstrndx = header[5], header[10], header[11], header[12]
        elif elf_class == 2:
            header = struct.unpack_from(endian + "HHIQQQIHHHHHH", data, 16)
            machine = header[1]
            entry_point = {"virtual_address": header[3]}
            shoff, shentsize, shnum, shstrndx = header[5], header[10], header[11], header[12]
        else:
            raise ValueError("unsupported ELF class")
        section_headers = []
        for index in range(min(shnum, 128)):
            offset = shoff + index * shentsize
            if offset + shentsize > len(data):
                warnings.append("section headers are truncated")
                break
            if elf_class == 1:
                values = struct.unpack_from(endian + "IIIIIIIIII", data, offset)
                header_item = {
                    "name_offset": values[0],
                    "type": values[1],
                    "flags": values[2],
                    "address": values[3],
                    "offset": values[4],
                    "size": values[5],
                    "link": values[6],
                    "entry_size": values[9],
                }
            else:
                values = struct.unpack_from(endian + "IIQQQQIIQQ", data, offset)
                header_item = {
                    "name_offset": values[0],
                    "type": values[1],
                    "flags": values[2],
                    "address": values[3],
                    "offset": values[4],
                    "size": values[5],
                    "link": values[6],
                    "entry_size": values[9],
                }
            section_headers.append(header_item)
        name_table = b""
        if 0 <= shstrndx < len(section_headers):
            shstr = section_headers[shstrndx]
            name_table = data[shstr["offset"] : shstr["offset"] + shstr["size"]]
        for header_item in section_headers:
            name = read_c_string(name_table, header_item["name_offset"])
            raw = data[header_item["offset"] : header_item["offset"] + min(header_item["size"], 65536)] if header_item["offset"] < len(data) else b""
            sections.append(
                {
                    "name": name,
                    "type": elf_section_type(header_item["type"]),
                    "address": header_item["address"],
                    "offset": header_item["offset"],
                    "size": header_item["size"],
                    "entropy_sample": round(shannon_entropy(raw), 4),
                }
            )
        symbols = parse_elf_symbols(data, section_headers, sections, endian, elf_class, limit=128)
    except (ValueError, struct.error) as exc:
        warnings.append(str(exc))
    return {
        "format": "elf",
        "architecture": elf_machine_name(machine) if "machine" in locals() else "unknown",
        "sections": sections,
        "imports": symbols.get("imports", []) if isinstance(symbols, dict) else [],
        "exports": symbols.get("exports", []) if isinstance(symbols, dict) else [],
        "entry_point": entry_point,
        "warnings": warnings,
    }


def parse_macho_summary(data: bytes) -> dict:
    magic = data[:4]
    endian = "big" if magic in {b"\xfe\xed\xfa\xce", b"\xfe\xed\xfa\xcf"} else "little"
    bits = 64 if magic in {b"\xfe\xed\xfa\xcf", b"\xcf\xfa\xed\xfe"} else 32
    return {
        "format": "mach-o",
        "architecture": f"{bits}-bit {endian}-endian",
        "sections": [],
        "imports": [],
        "exports": [],
        "entry_point": None,
        "warnings": ["Mach-O magic identified; detailed load-command parsing is not implemented"],
    }


def parse_pe_imports(data: bytes, sections: list[dict], import_rva: int, limit: int) -> list[dict]:
    imports = []
    offset = rva_to_offset(sections, import_rva)
    if offset is None:
        return imports
    for descriptor_offset in range(offset, min(len(data), offset + 20 * limit), 20):
        if descriptor_offset + 20 > len(data):
            break
        original_thunk = read_u32(data, descriptor_offset)
        name_rva = read_u32(data, descriptor_offset + 12)
        first_thunk = read_u32(data, descriptor_offset + 16)
        if original_thunk == 0 and name_rva == 0 and first_thunk == 0:
            break
        name_offset = rva_to_offset(sections, name_rva)
        dll = read_c_string(data, name_offset) if name_offset is not None else ""
        imports.append({"library": dll, "original_thunk_rva": original_thunk, "first_thunk_rva": first_thunk})
        if len(imports) >= limit:
            break
    return imports


def parse_pe_exports(data: bytes, sections: list[dict], export_rva: int, export_size: int, limit: int) -> list[dict]:
    exports = []
    offset = rva_to_offset(sections, export_rva)
    if offset is None or offset + 40 > len(data):
        return exports
    name_count = read_u32(data, offset + 24)
    names_rva = read_u32(data, offset + 32)
    names_offset = rva_to_offset(sections, names_rva)
    if names_offset is None:
        return exports
    for index in range(min(name_count, limit)):
        entry_offset = names_offset + index * 4
        if entry_offset + 4 > len(data):
            break
        name_offset = rva_to_offset(sections, read_u32(data, entry_offset))
        if name_offset is not None:
            exports.append({"name": read_c_string(data, name_offset)})
    return exports


def parse_elf_symbols(data: bytes, section_headers: list[dict], sections: list[dict], endian: str, elf_class: int, limit: int) -> dict:
    imports = []
    exports = []
    for index, section in enumerate(section_headers):
        if section["type"] not in {2, 11} or not section["entry_size"]:
            continue
        if section["link"] >= len(section_headers):
            continue
        strtab_header = section_headers[section["link"]]
        strtab = data[strtab_header["offset"] : strtab_header["offset"] + strtab_header["size"]]
        count = min(section["size"] // section["entry_size"], limit)
        for item_index in range(count):
            offset = section["offset"] + item_index * section["entry_size"]
            if offset + section["entry_size"] > len(data):
                break
            if elf_class == 1:
                name_offset, value, size, info, _other, shndx = struct.unpack_from(endian + "IIIBBH", data, offset)
            else:
                name_offset, info, _other, shndx, value, size = struct.unpack_from(endian + "IBBHQQ", data, offset)
            name = read_c_string(strtab, name_offset)
            if not name:
                continue
            record = {"name": name, "value": value, "size": size, "binding": info >> 4}
            if shndx == 0:
                imports.append(record)
            else:
                exports.append(record)
            if len(imports) >= limit and len(exports) >= limit:
                return {"imports": imports[:limit], "exports": exports[:limit]}
    return {"imports": imports[:limit], "exports": exports[:limit]}


def markdown_heading_anchors(text: str) -> set[str]:
    anchors = set()
    for line in text.splitlines():
        match = re.match(r"^\s{0,3}(#{1,6})\s+(.+?)\s*$", line)
        if not match:
            continue
        heading = re.sub(r"\s+#+\s*$", "", match.group(2).strip())
        anchors.add(markdown_anchor(heading))
    return anchors


def check_markdown_link(path: Path, raw_target: str, headings: set[str], line: int) -> dict:
    target = raw_target.split()[0].strip("<>")
    parsed = urlparse(target)
    if parsed.scheme in {"http", "https"}:
        return {"line": line, "target": raw_target, "kind": "external", "status": "ok", "network_checked": False}
    if target.startswith("#"):
        anchor = markdown_anchor(unquote(target[1:]))
        return {"line": line, "target": raw_target, "kind": "anchor", "status": "ok" if anchor in headings else "missing_anchor"}
    file_part, _, anchor_part = target.partition("#")
    decoded_file = unquote(file_part)
    try:
        target_path = resolve_relative_workspace_path(path.parent, decoded_file)
    except ValueError as exc:
        return {"line": line, "target": raw_target, "kind": "local", "status": "invalid", "reason": str(exc)}
    if not target_path.exists():
        return {"line": line, "target": raw_target, "kind": "local", "status": "missing_file"}
    if anchor_part:
        try:
            target_text = read_text_limited(target_path, MAX_TEXT_BYTES)
            target_anchors = markdown_heading_anchors(target_text)
        except UnicodeDecodeError:
            target_anchors = set()
        anchor = markdown_anchor(unquote(anchor_part))
        if anchor not in target_anchors:
            return {"line": line, "target": raw_target, "kind": "local", "status": "missing_anchor"}
    return {"line": line, "target": raw_target, "kind": "local", "status": "ok", "relative_target": relative_path(target_path)}


def parse_case_text(text: str, suffix: str):
    if suffix == ".json":
        return json.loads(text)
    fields: dict[str, str] = {}
    current_key = ""
    current_lines: list[str] = []
    for line in text.splitlines():
        match = re.match(r"^\s*([A-Za-z][A-Za-z0-9 _-]{1,40})\s*:\s*(.*)$", line)
        if match:
            if current_key:
                fields[current_key] = "\n".join(current_lines).strip()
            current_key = normalize_key(match.group(1))
            current_lines = [match.group(2).strip()]
            continue
        if current_key:
            current_lines.append(line)
    if current_key:
        fields[current_key] = "\n".join(current_lines).strip()
    if fields:
        fields["_raw"] = text
        return fields
    return {"_raw": text}


def first_present(fields: dict, keys: list[str]):
    for key in keys:
        for candidate in {key, normalize_key(key)}:
            value = fields.get(candidate)
            if value not in (None, ""):
                return str(value)
    return ""


def extract_named_text(fields: dict, keys: list[str]) -> str:
    value = first_present(fields, keys)
    if value:
        return value[:1000]
    return ""


def infer_product_area(text: str) -> str:
    for marker, area in [
        ("login", "authentication"),
        ("billing", "billing"),
        ("invoice", "billing"),
        ("api", "api"),
        ("dashboard", "web-app"),
        ("mobile", "mobile"),
        ("security", "security"),
    ]:
        if marker in text:
            return area
    return "unknown"


def infer_priority(text: str) -> str:
    if any(term in text for term in ["sev1", "p0", "critical", "outage", "data loss", "security incident"]):
        return "critical"
    if any(term in text for term in ["sev2", "p1", "urgent", "blocked", "enterprise"]):
        return "high"
    if any(term in text for term in ["sev3", "p2", "workaround"]):
        return "medium"
    return "unknown"


def infer_customer_impact(text: str) -> str:
    patterns = [
        r"(?im)^.*(?:impact|blocked|cannot|can't|unable|outage|error).*$",
    ]
    for pattern in patterns:
        match = re.search(pattern, text)
        if match:
            return match.group(0).strip()[:500]
    return ""


def detect_privacy_flags(text: str) -> list[str]:
    flags = []
    for name, pattern in REDACTION_PATTERNS:
        if pattern.search(text):
            flags.append(name)
    return flags


def detect_escalation_flags(lowered: str) -> list[str]:
    flags = []
    escalation_terms = {
        "security": ["security", "vulnerability", "breach", "token leak", "xss", "sqli"],
        "legal": ["legal", "subpoena", "gdpr", "ccpa", "compliance"],
        "billing": ["refund", "chargeback", "invoice", "billing"],
        "sla": ["sla", "outage", "sev1", "critical", "enterprise"],
    }
    for name, terms in escalation_terms.items():
        if any(term in lowered for term in terms):
            flags.append(name)
    return flags


def missing_support_information(fields: dict, lowered: str) -> list[str]:
    missing = []
    required = [
        ("customer issue", ["issue", "summary", "description", "_raw"]),
        ("product area", ["product_area", "product", "component", "service"]),
        ("customer impact", ["customer_impact", "impact"]),
        ("priority or SLA", ["priority", "severity", "sla"]),
    ]
    for label, keys in required:
        if not any(first_present(fields, [key]) for key in keys):
            missing.append(label)
    if "error" in lowered and "timestamp" not in lowered and "time" not in lowered:
        missing.append("timestamp")
    if "account" in lowered and not any(first_present(fields, [key]) for key in ["account_id", "tenant", "org"]):
        missing.append("account identifier")
    return missing


def resolve_relative_workspace_path(base: Path, raw: str) -> Path:
    candidate = (base / raw).resolve(strict=False)
    try:
        candidate.relative_to(ROOT)
    except ValueError as exc:
        raise ValueError(f'link "{raw}" escapes workspace root') from exc
    return candidate


def markdown_anchor(value: str) -> str:
    value = value.strip().lower()
    value = re.sub(r"<[^>]+>", "", value)
    value = re.sub(r"[^\w\s-]", "", value, flags=re.UNICODE)
    value = re.sub(r"\s+", "-", value)
    return value.strip("-")


def normalize_key(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "_", value.strip().lower()).strip("_")


def printable_ratio(data: bytes) -> float:
    if not data:
        return 1.0
    allowed = set(bytes(string.printable, "ascii")) | {0x0a, 0x0d, 0x09}
    return sum(1 for byte in data if byte in allowed) / len(data)


def looks_symbol_like(value: str) -> bool:
    if len(value) < 4 or len(value) > 160:
        return False
    return bool(re.search(r"[A-Za-z_][A-Za-z0-9_@$?.:-]{3,}", value)) and not value.isspace()


def redacted_string_record(record: dict) -> dict:
    clone = dict(record)
    clone["value"] = redact_value(str(clone.get("value", "")))
    return clone


def redact_value(value: str) -> str:
    if len(value) <= 6:
        return "***"
    return f"{value[:3]}***{value[-3:]}"


def rva_to_offset(sections: list[dict], rva: int) -> int | None:
    for section in sections:
        start = int(section.get("virtual_address", 0))
        size = max(int(section.get("virtual_size", 0)), int(section.get("raw_size", 0)))
        if start <= rva < start + size:
            return int(section.get("raw_pointer", 0)) + (rva - start)
    return None


def read_c_string(data: bytes, offset: int | None, limit: int = 256) -> str:
    if offset is None or offset < 0 or offset >= len(data):
        return ""
    end = min(len(data), offset + limit)
    chunk = data[offset:end].split(b"\x00", 1)[0]
    return chunk.decode("utf-8", errors="replace")


def read_u16(data: bytes, offset: int) -> int:
    if offset + 2 > len(data):
        raise ValueError("unexpected EOF while reading u16")
    return struct.unpack_from("<H", data, offset)[0]


def read_u32(data: bytes, offset: int) -> int:
    if offset + 4 > len(data):
        raise ValueError("unexpected EOF while reading u32")
    return struct.unpack_from("<I", data, offset)[0]


def pe_machine_name(value: int) -> str:
    return {
        0x014C: "x86",
        0x8664: "x86_64",
        0x01C0: "arm",
        0xAA64: "arm64",
    }.get(value, f"unknown-0x{value:04x}")


def elf_machine_name(value: int) -> str:
    return {
        3: "x86",
        40: "arm",
        62: "x86_64",
        183: "arm64",
    }.get(value, f"unknown-{value}")


def elf_section_type(value: int) -> str:
    return {
        0: "NULL",
        1: "PROGBITS",
        2: "SYMTAB",
        3: "STRTAB",
        4: "RELA",
        8: "NOBITS",
        9: "REL",
        11: "DYNSYM",
    }.get(value, str(value))


SUSPICIOUS_BINARY_MARKERS = [
    "createremotethread",
    "virtualalloc",
    "writeprocessmemory",
    "winexec",
    "powershell",
    "cmd.exe",
    "socket",
    "connect",
    "execve",
    "ptrace",
    "dlopen",
    "mprotect",
    "system(",
]

IMPORT_EXPORT_MARKERS = [
    ".dll",
    ".so",
    ".dylib",
    "kernel32",
    "advapi32",
    "libc.",
    "import",
    "export",
]

CREDENTIAL_MARKERS = [
    "password",
    "passwd",
    "secret",
    "token",
    "apikey",
    "api_key",
    "private key",
]

REDACTION_PATTERNS = [
    ("email", re.compile(r"\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b")),
    ("api_key", re.compile(r"(?i)\b(?:api[_-]?key|token|secret|password)\s*[:=]\s*['\"]?([A-Za-z0-9_\-./+=]{8,})")),
    ("bearer_token", re.compile(r"(?i)\bbearer\s+[A-Za-z0-9_\-./+=]{12,}")),
    ("private_key", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----")),
    ("credit_card_like", re.compile(r"\b(?:\d[ -]*?){13,19}\b")),
]


def printable_ascii(data: bytes) -> str:
    return "".join(chr(byte) if 32 <= byte <= 126 else "." for byte in data)


def shannon_entropy(data: bytes) -> float:
    if not data:
        return 0.0
    counts = {}
    for byte in data:
        counts[byte] = counts.get(byte, 0) + 1
    entropy = 0.0
    total = len(data)
    for count in counts.values():
        probability = count / total
        entropy -= probability * math.log2(probability)
    return entropy


def looks_text(data: bytes) -> bool:
    if not data:
        return True
    allowed = set(bytes(string.printable, "ascii")) | {0x0a, 0x0d, 0x09}
    printable_count = sum(1 for byte in data if byte in allowed)
    return printable_count / len(data) > 0.85


def bounded_int(value, minimum: int, maximum: int) -> int:
    try:
        parsed = int(value)
    except (TypeError, ValueError):
        parsed = minimum
    return max(minimum, min(maximum, parsed))


def relative_path(path: Path) -> str:
    try:
        return path.relative_to(ROOT).as_posix()
    except ValueError:
        return str(path)


def respond(req_id: int, result=None, error=None) -> None:
    payload = {"jsonrpc": "2.0", "id": req_id}
    if error is not None:
        payload["error"] = error
    else:
        payload["result"] = result
    sys.stdout.write(json.dumps(payload, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def handle_call(params: dict) -> dict:
    name = params.get("name", "")
    arguments = params.get("arguments", {}) or {}
    handlers = {
        "write_note": handle_write_note,
        "read_note": handle_read_note,
        "python_ast_summary": handle_python_ast_summary,
        "json_query": handle_json_query,
        "binary_file_info": handle_binary_file_info,
        "binary_strings": handle_binary_strings,
        "hex_preview": handle_hex_preview,
        "binary_format_summary": handle_binary_format_summary,
        "binary_entropy_map": handle_binary_entropy_map,
        "binary_symbol_hints": handle_binary_symbol_hints,
        "binary_extract_window": handle_binary_extract_window,
        "markdown_link_check": handle_markdown_link_check,
        "support_case_summary": handle_support_case_summary,
        "redaction_check": handle_redaction_check,
    }
    try:
        handler = handlers.get(name)
        if handler is None:
            return {"content": "unknown tool", "is_error": True}
        body = handler(arguments)
        return {"content": json.dumps(body, ensure_ascii=False), "is_error": False}
    except Exception as exc:  # noqa: BLE001
        return {"content": str(exc), "is_error": True}


for raw_line in sys.stdin:
    line = raw_line.strip()
    if not line:
        continue
    try:
        request = json.loads(line)
    except json.JSONDecodeError as exc:
        respond(0, error={"code": -32700, "message": str(exc)})
        continue

    method = request.get("method")
    req_id = request.get("id", 0)
    if method == "tools/list":
        respond(req_id, result={"tools": TOOLS})
    elif method == "tools/call":
        respond(req_id, result=handle_call(request.get("params", {})))
    else:
        respond(req_id, error={"code": -32601, "message": "method not found"})
