import ast
import hashlib
import json
import math
import os
import re
import string
import sys
from pathlib import Path


MAX_TEXT_BYTES = 1024 * 1024
MAX_BINARY_BYTES = 8 * 1024 * 1024


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
