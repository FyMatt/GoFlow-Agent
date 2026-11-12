#!/usr/bin/env python3
"""Run the mandatory real-Provider screenshot-to-engineering acceptance.

The fixture keeps the behavioral contract exclusively in a generated PNG. A
successful run must inspect the screenshot, modify an isolated Go workspace,
pass hidden tests added only after delivery, and persist real tool evidence.
Literal API keys are never accepted, printed, or written by this script.
"""

from __future__ import annotations

import argparse
import binascii
import datetime as dt
import hashlib
import json
import os
import re
import shutil
import stat
import struct
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zlib
from pathlib import Path
from typing import Any, Mapping

sys.path.insert(0, str(Path(__file__).resolve().parent))

from real_provider_acceptance import (  # noqa: E402
    collect_run_evidence,
    drive_run,
    free_port,
    init_git,
    provider_probe,
    request_json,
    run_checked,
    start_server,
    stop_server,
    write_text,
)
from real_provider_matrix import (  # noqa: E402
    apply_staged_changes_if_available,
    evaluate_terminal_quality,
    inspect_persisted_run,
)


ROOT = Path(__file__).resolve().parents[1]
TASK_PROMPT = (
    "截图是本任务唯一的需求来源。请读取截图，定位其中指向的项目文件并完成修复；"
    "保持现有公开 API，补充必要测试，运行 gofmt、go test ./... 和 go vet ./...，"
    "通过质量门后提供真实 Diff、验证证据和可写回产物。"
)
SCREENSHOT_LINES = (
    "GOFLOW VISION ACCEPTANCE",
    "BUG REPORT",
    "FILE: PORTCFG/PORT.GO",
    "FUNCTION: EFFECTIVEPORT(REQUESTED, FALLBACK)",
    "RULE 1: REQUESTED 1..65535 RETURNS REQUESTED",
    "RULE 2: OUTSIDE RANGE RETURNS FALLBACK",
    "RULE 3: INVALID FALLBACK RETURNS 8080",
    "OBSERVED: EFFECTIVEPORT(70000, 9090) = 70000",
    "EXPECTED: 9090",
)

# Public-domain-style 5x7 bitmap glyphs. Keeping the fixture renderer in the
# standard library makes the hard gate reproducible on a clean native host.
FONT_5X7 = {
    "A": ("01110", "10001", "10001", "11111", "10001", "10001", "10001"),
    "B": ("11110", "10001", "10001", "11110", "10001", "10001", "11110"),
    "C": ("01111", "10000", "10000", "10000", "10000", "10000", "01111"),
    "D": ("11110", "10001", "10001", "10001", "10001", "10001", "11110"),
    "E": ("11111", "10000", "10000", "11110", "10000", "10000", "11111"),
    "F": ("11111", "10000", "10000", "11110", "10000", "10000", "10000"),
    "G": ("01111", "10000", "10000", "10111", "10001", "10001", "01111"),
    "H": ("10001", "10001", "10001", "11111", "10001", "10001", "10001"),
    "I": ("11111", "00100", "00100", "00100", "00100", "00100", "11111"),
    "J": ("00111", "00010", "00010", "00010", "10010", "10010", "01100"),
    "K": ("10001", "10010", "10100", "11000", "10100", "10010", "10001"),
    "L": ("10000", "10000", "10000", "10000", "10000", "10000", "11111"),
    "M": ("10001", "11011", "10101", "10101", "10001", "10001", "10001"),
    "N": ("10001", "11001", "10101", "10011", "10001", "10001", "10001"),
    "O": ("01110", "10001", "10001", "10001", "10001", "10001", "01110"),
    "P": ("11110", "10001", "10001", "11110", "10000", "10000", "10000"),
    "Q": ("01110", "10001", "10001", "10001", "10101", "10010", "01101"),
    "R": ("11110", "10001", "10001", "11110", "10100", "10010", "10001"),
    "S": ("01111", "10000", "10000", "01110", "00001", "00001", "11110"),
    "T": ("11111", "00100", "00100", "00100", "00100", "00100", "00100"),
    "U": ("10001", "10001", "10001", "10001", "10001", "10001", "01110"),
    "V": ("10001", "10001", "10001", "10001", "10001", "01010", "00100"),
    "W": ("10001", "10001", "10001", "10101", "10101", "10101", "01010"),
    "X": ("10001", "10001", "01010", "00100", "01010", "10001", "10001"),
    "Y": ("10001", "10001", "01010", "00100", "00100", "00100", "00100"),
    "Z": ("11111", "00001", "00010", "00100", "01000", "10000", "11111"),
    "0": ("01110", "10001", "10011", "10101", "11001", "10001", "01110"),
    "1": ("00100", "01100", "00100", "00100", "00100", "00100", "01110"),
    "2": ("01110", "10001", "00001", "00010", "00100", "01000", "11111"),
    "3": ("11110", "00001", "00001", "01110", "00001", "00001", "11110"),
    "4": ("00010", "00110", "01010", "10010", "11111", "00010", "00010"),
    "5": ("11111", "10000", "10000", "11110", "00001", "00001", "11110"),
    "6": ("01110", "10000", "10000", "11110", "10001", "10001", "01110"),
    "7": ("11111", "00001", "00010", "00100", "01000", "01000", "01000"),
    "8": ("01110", "10001", "10001", "01110", "10001", "10001", "01110"),
    "9": ("01110", "10001", "10001", "01111", "00001", "00001", "01110"),
    " ": ("00000",) * 7,
    ".": ("00000", "00000", "00000", "00000", "00000", "00110", "00110"),
    ",": ("00000", "00000", "00000", "00000", "00110", "00110", "00100"),
    ":": ("00000", "00110", "00110", "00000", "00110", "00110", "00000"),
    "/": ("00001", "00010", "00010", "00100", "01000", "01000", "10000"),
    "(": ("00010", "00100", "01000", "01000", "01000", "00100", "00010"),
    ")": ("01000", "00100", "00010", "00010", "00010", "00100", "01000"),
    "-": ("00000", "00000", "00000", "11111", "00000", "00000", "00000"),
    "=": ("00000", "00000", "11111", "00000", "11111", "00000", "00000"),
    ">": ("10000", "01000", "00100", "00010", "00100", "01000", "10000"),
}


class ProviderProbeComplete(Exception):
    """Internal control flow used to finalize a successful probe-only report."""


def _png_chunk(kind: bytes, payload: bytes) -> bytes:
    return struct.pack(">I", len(payload)) + kind + payload + struct.pack(">I", binascii.crc32(kind + payload) & 0xFFFFFFFF)


def render_fixture_png(lines: tuple[str, ...] = SCREENSHOT_LINES, scale: int = 4) -> bytes:
    unsupported = sorted({character for line in lines for character in line if character not in FONT_5X7})
    if unsupported:
        raise ValueError(f"screenshot fixture uses unsupported glyphs: {unsupported}")
    margin, line_gap = 18, 8
    character_width, character_height = 6 * scale, 7 * scale
    width = max(len(line) for line in lines) * character_width + margin * 2
    height = len(lines) * character_height + (len(lines) - 1) * line_gap + margin * 2
    pixels = [bytearray([248, 249, 250] * width) for _ in range(height)]

    def fill(left: int, top: int, right: int, bottom: int, color: tuple[int, int, int]) -> None:
        for y in range(max(0, top), min(height, bottom)):
            row = pixels[y]
            for x in range(max(0, left), min(width, right)):
                row[x * 3 : x * 3 + 3] = bytes(color)

    fill(0, 0, width, margin + character_height + 8, (24, 27, 31))
    for line_index, line in enumerate(lines):
        top = margin + line_index * (character_height + line_gap)
        color = (255, 255, 255) if line_index == 0 else (24, 27, 31)
        for character_index, character in enumerate(line):
            left = margin + character_index * character_width
            for row_index, row_bits in enumerate(FONT_5X7[character]):
                for column_index, bit in enumerate(row_bits):
                    if bit == "1":
                        fill(
                            left + column_index * scale,
                            top + row_index * scale,
                            left + (column_index + 1) * scale,
                            top + (row_index + 1) * scale,
                            color,
                        )
    raw = b"".join(b"\x00" + bytes(row) for row in pixels)
    signature = b"\x89PNG\r\n\x1a\n"
    return signature + _png_chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)) + _png_chunk(b"IDAT", zlib.compress(raw, 9)) + _png_chunk(b"IEND", b"")


def png_dimensions(payload: bytes) -> tuple[int, int]:
    if len(payload) < 24 or payload[:8] != b"\x89PNG\r\n\x1a\n" or payload[12:16] != b"IHDR":
        raise ValueError("fixture is not a PNG")
    return struct.unpack(">II", payload[16:24])


def upload_attachment(base_url: str, name: str, payload: bytes) -> dict[str, Any]:
    boundary = "----GoFlowVision" + uuid.uuid4().hex
    body = (
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"files\"; filename=\"{name}\"\r\n"
        "Content-Type: image/png\r\n\r\n"
    ).encode("utf-8") + payload + f"\r\n--{boundary}--\r\n".encode("ascii")
    request = urllib.request.Request(
        base_url + "/api/attachments",
        data=body,
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            result = json.loads(response.read().decode("utf-8"))
            if response.status != 201:
                raise RuntimeError(f"attachment upload returned {response.status}")
    except urllib.error.HTTPError as error:
        detail = error.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"attachment upload returned {error.code}: {detail}") from error
    attachments = result.get("attachments", []) if isinstance(result, dict) else []
    if len(attachments) != 1 or not isinstance(attachments[0], dict):
        raise RuntimeError(f"attachment upload response is invalid: {result}")
    return attachments[0]


def create_workspace(workspace: Path, env: dict[str, str]) -> None:
    write_text(workspace / "go.mod", "module vision.local/portfixture\n\ngo 1.22\n")
    write_text(
        workspace / "portcfg" / "port.go",
        """package portcfg

// EffectivePort returns the port selected for the current configuration.
func EffectivePort(requested, fallback int) int {
\treturn requested
}
""",
    )
    write_text(
        workspace / "portcfg" / "port_test.go",
        """package portcfg

import "testing"

func TestEffectivePortKeepsValidRequestedPort(t *testing.T) {
\tif got := EffectivePort(8443, 8080); got != 8443 {
\t\tt.Fatalf("EffectivePort valid request = %d, want 8443", got)
\t}
}
""",
    )
    init_git(workspace, env)


def install_hidden_tests(workspace: Path) -> None:
    write_text(
        workspace / "portcfg" / "vision_hidden_test.go",
        """package portcfg

import "testing"

func TestVisionAcceptanceContract(t *testing.T) {
\ttests := []struct {
\t\trequested int
\t\tfallback  int
\t\twant      int
\t}{
\t\t{1, 8080, 1},
\t\t{65535, 8080, 65535},
\t\t{0, 9090, 9090},
\t\t{70000, 9090, 9090},
\t\t{-1, 0, 8080},
\t\t{65536, 70000, 8080},
\t}
\tfor _, test := range tests {
\t\tif got := EffectivePort(test.requested, test.fallback); got != test.want {
\t\t\tt.Fatalf("EffectivePort(%d, %d) = %d, want %d", test.requested, test.fallback, got, test.want)
\t\t}
\t}
}
""",
    )


def verify_workspace(workspace: Path, env: dict[str, str]) -> dict[str, Any]:
    go_files = sorted(str(path.relative_to(workspace)) for path in workspace.rglob("*.go"))
    format_result = run_checked(["gofmt", "-l", *go_files], workspace, env, timeout=120)
    if format_result["output"].strip():
        raise RuntimeError(f"gofmt found unformatted files: {format_result['output']}")
    return {
        "go_test": run_checked(["go", "test", "./..."], workspace, env, timeout=180),
        "go_vet": run_checked(["go", "vet", "./..."], workspace, env, timeout=180),
        "gofmt": format_result,
    }


def report_path_for(output: Path) -> Path:
    return output / "report.json"


def acceptance_workspace_data_root(data_root: Path, workspace: Path) -> Path:
    # Mirrors config.resolveWorkspaceDataRoot for an external GOFLOW_DATA_ROOT.
    cleaned = os.path.normpath(str(workspace))
    key = hashlib.sha256(cleaned.encode("utf-8")).hexdigest()[:16]
    return data_root / "workspaces" / key


def install_provider_override(data_root: Path, configured_path: str) -> Path | None:
    configured_path = configured_path.strip()
    if not configured_path:
        return None
    source = Path(configured_path).expanduser().resolve()
    if not source.is_file() or source.stat().st_size == 0:
        raise RuntimeError("configured vision Provider module is missing or empty")
    payload = source.read_bytes()
    validate_provider_module_contract(payload.decode("utf-8"))
    target = data_root / "resources" / "providers" / "primary.yaml"
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(payload)
    return target


def provider_module_scalar(content: str, field: str) -> str:
    values = re.findall(rf"(?m)^[ \t]+{re.escape(field)}\s*:\s*([^#\r\n]+)", content)
    if len(values) != 1:
        raise RuntimeError(f"vision Provider module must define exactly one nonempty {field}")
    value = values[0].strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {'"', "'"}:
        value = value[1:-1].strip()
    if not value:
        raise RuntimeError(f"vision Provider module must define exactly one nonempty {field}")
    return value


def validate_provider_module_contract(content: str) -> dict[str, str]:
    if len(re.findall(r"(?m)^primary\s*:\s*(?:#.*)?$", content)) != 1:
        raise RuntimeError("vision Provider module must define exactly one top-level primary")
    provider = provider_module_scalar(content, "provider").lower()
    if provider not in {"openai-compatible", "anthropic"}:
        raise RuntimeError(f"vision Provider module uses unsupported provider {provider!r}")
    base_url = provider_module_scalar(content, "base_url")
    parsed_url = urllib.parse.urlsplit(base_url)
    if parsed_url.scheme.lower() not in {"http", "https"} or not parsed_url.hostname:
        raise RuntimeError("vision Provider module base_url must be an absolute HTTP(S) URL")
    if parsed_url.username or parsed_url.password or parsed_url.query or parsed_url.fragment:
        raise RuntimeError("vision Provider module base_url must not contain credentials, query, or fragment")
    model = provider_module_scalar(content, "model")
    vision_values = re.findall(r"(?mi)^[ \t]+vision\s*:\s*([^#\r\n]+)", content)
    if len(vision_values) != 1 or vision_values[0].strip().lower() != "true":
        raise RuntimeError("vision Provider module must explicitly set exactly one vision: true")
    secret_reference = r"secret://providers/primary/api_key"
    api_key_lines = re.findall(r"(?m)^[ \t]+api_key\s*:\s*([^#\r\n]+)", content)
    if len(api_key_lines) != 1 or api_key_lines[0].strip() != secret_reference:
        raise RuntimeError("vision Provider module must use the isolated primary secret reference")
    return {"provider": provider, "base_url": base_url, "model": model}


def validate_formal_input(path: Path, label: str, *, private: bool = False) -> Path:
    if path.is_symlink():
        raise RuntimeError(f"{label} must not be a symbolic link")
    resolved = path.resolve()
    try:
        resolved.relative_to(ROOT.resolve())
    except ValueError:
        pass
    else:
        raise RuntimeError(f"{label} must be outside the repository checkout")
    if not resolved.is_file() or resolved.stat().st_size == 0:
        raise RuntimeError(f"{label} is missing or empty")
    if resolved.stat().st_mode & (stat.S_IWUSR | stat.S_IWGRP | stat.S_IWOTH):
        raise RuntimeError(f"{label} must be read-only")
    if private and os.name != "nt" and resolved.stat().st_mode & (stat.S_IRWXG | stat.S_IRWXO):
        raise RuntimeError(f"{label} must not be accessible by group or other users")
    return resolved


def validate_formal_vision_inputs(env: Mapping[str, str]) -> tuple[Path, Path, str]:
    configured_secret = str(env.get("GOFLOW_VISION_SECRET_FILE") or "").strip()
    configured_module = str(env.get("GOFLOW_VISION_PROVIDER_MODULE") or "").strip()
    if not configured_secret or not configured_module:
        raise RuntimeError("formal vision acceptance requires external secret and Provider module paths")
    secret_path = validate_formal_input(
        Path(configured_secret).expanduser(),
        "vision Provider secret",
        private=True,
    )
    module_path = validate_formal_input(
        Path(configured_module).expanduser(),
        "vision Provider module",
    )
    secret_value = secret_path.read_text(encoding="utf-8").strip()
    if not secret_value:
        raise RuntimeError("configured primary Provider secret is empty")
    if len(secret_value.encode("utf-8")) < 16:
        raise RuntimeError("configured primary Provider secret is too short for reliable leak detection")
    validate_provider_module_contract(module_path.read_text(encoding="utf-8"))
    return secret_path, module_path, secret_value


def check_formal_inputs(env: Mapping[str, str]) -> dict[str, Any]:
    try:
        secret_path, module_path, secret_value = validate_formal_vision_inputs(env)
        return {
            "ready": True,
            "message": "视觉 Provider 外部输入检查通过；尚未发起网络请求或能力探针。",
            "secret": {
                "configured": True,
                "outside_repository": True,
                "read_only": True,
                "nonempty": True,
            },
            "provider_module": {
                "configured": True,
                "outside_repository": True,
                "read_only": True,
                "vision_declared": True,
                "isolated_secret_reference": True,
            },
            "required_live_capabilities": ["vision", "tool_calling"],
            "network_request_performed": False,
        }
    except Exception as error:  # noqa: BLE001 - preflight returns one sanitized diagnostic.
        return {
            "ready": False,
            "message": "视觉 Provider 外部输入尚未就绪。",
            "reason": str(error),
            "network_request_performed": False,
        }
    finally:
        if "secret_value" in locals():
            secret_value = ""


def vision_provider_probe_gates(probe: Mapping[str, Any]) -> dict[str, bool]:
    capabilities = probe.get("capabilities") if isinstance(probe.get("capabilities"), Mapping) else {}
    return {
        "provider_configured": bool(probe.get("configured")),
        "provider_valid": bool(probe.get("valid")),
        "provider_vision": bool(capabilities.get("vision")),
        "provider_tool_calling": bool(capabilities.get("tool_calling")),
        "secret_not_leaked": True,
    }


def _make_writable_and_retry(function: Any, path: str, _exc_info: Any) -> None:
    try:
        os.chmod(path, stat.S_IWRITE)
        function(path)
    except OSError:
        pass


def remove_tree_with_retry(path: Path, attempts: int = 50, delay_seconds: float = 0.2) -> bool:
    for attempt in range(max(1, attempts)):
        if path.exists():
            shutil.rmtree(path, onerror=_make_writable_and_retry)
        if not path.exists():
            return True
        if attempt + 1 < attempts:
            time.sleep(delay_seconds)
    return not path.exists()


def remove_file_with_retry(path: Path, attempts: int = 50, delay_seconds: float = 0.2) -> bool:
    for attempt in range(max(1, attempts)):
        try:
            path.unlink(missing_ok=True)
        except OSError:
            pass
        if not path.exists():
            return True
        if attempt + 1 < attempts:
            time.sleep(delay_seconds)
    return not path.exists()


def redact_secret_value(value: Any, secret: str) -> tuple[Any, int]:
    if not secret:
        return value, 0
    if isinstance(value, str):
        occurrences = value.count(secret)
        return value.replace(secret, "[REDACTED]"), occurrences
    if isinstance(value, list):
        output: list[Any] = []
        occurrences = 0
        for item in value:
            redacted, count = redact_secret_value(item, secret)
            output.append(redacted)
            occurrences += count
        return output, occurrences
    if isinstance(value, dict):
        output: dict[Any, Any] = {}
        occurrences = 0
        for key, item in value.items():
            redacted_key, key_count = redact_secret_value(key, secret)
            redacted_item, item_count = redact_secret_value(item, secret)
            output[redacted_key] = redacted_item
            occurrences += key_count + item_count
        return output, occurrences
    return value, 0


def sanitize_evidence_files(output: Path, secret: str) -> dict[str, Any]:
    secret_bytes = secret.encode("utf-8")
    replacement = b"[REDACTED]"
    files_scanned = 0
    occurrences = 0
    redacted_files: list[str] = []
    scan_errors: list[str] = []
    if not secret_bytes:
        return {
            "scan_performed": False,
            "files_scanned": 0,
            "leaks_detected": 0,
            "redacted_files": [],
            "scan_complete": True,
            "scan_errors": [],
        }
    for path in sorted(candidate for candidate in output.rglob("*") if candidate.is_file()):
        if path.name == "report.json":
            continue
        try:
            payload = path.read_bytes()
        except OSError as error:
            scan_errors.append(f"{path.relative_to(output)}: {error}")
            continue
        count = payload.count(secret_bytes)
        files_scanned += 1
        if count:
            path.write_bytes(payload.replace(secret_bytes, replacement))
            occurrences += count
            redacted_files.append(str(path.relative_to(output)))
    return {
        "scan_performed": True,
        "files_scanned": files_scanned,
        "leaks_detected": occurrences,
        "redacted_files": redacted_files,
        "scan_complete": not scan_errors,
        "scan_errors": scan_errors,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--timeout", type=float, default=900.0)
    parser.add_argument("--output", help="new evidence directory; defaults under .artifacts")
    parser.add_argument("--keep-workspace", action="store_true")
    parser.add_argument("--require-external-inputs", action="store_true")
    parser.add_argument(
        "--check-inputs",
        action="store_true",
        help="validate formal external files without building, starting the service, or calling the Provider",
    )
    parser.add_argument(
        "--probe-provider",
        action="store_true",
        help="run only the real Provider capability probe with formal external inputs",
    )
    args = parser.parse_args()
    if args.check_inputs:
        result = check_formal_inputs(os.environ)
        print(json.dumps(result, ensure_ascii=False, indent=2))
        return 0 if result["ready"] else 1
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d-%H%M%S")
    configured_output = args.output or os.environ.get("GOFLOW_ACCEPTANCE_OUTPUT", "")
    output = Path(configured_output).resolve() if configured_output else ROOT / ".artifacts" / "real-vision-acceptance" / stamp
    output.mkdir(parents=True, exist_ok=False)
    workspace = Path(tempfile.mkdtemp(prefix="workspace-", dir=output))
    started = time.monotonic()
    report: dict[str, Any] = {
        "schema_version": 1,
        "passed": False,
        "generated_at": dt.datetime.now(dt.timezone.utc).isoformat(),
        "provider": {},
        "fixture": {},
        "run": {},
        "evidence": {},
        "verification": {},
        "mode": "provider_probe" if args.probe_provider else "vision_engineering",
        "input_policy": {"external_inputs_required": args.require_external_inputs or args.probe_provider},
        "error": "",
    }
    process = log_handle = None
    env = os.environ.copy()
    data_root = output / "data"
    env["GOFLOW_DATA_ROOT"] = str(data_root)
    env.setdefault("GOCACHE", str(output / "gocache"))
    env.setdefault("GOTMPDIR", str(output / "gotmp"))
    Path(env["GOCACHE"]).mkdir(parents=True, exist_ok=True)
    Path(env["GOTMPDIR"]).mkdir(parents=True, exist_ok=True)
    try:
        configured_secret = str(env.get("GOFLOW_VISION_SECRET_FILE") or "").strip()
        secret_source = Path(configured_secret).expanduser() if configured_secret else ROOT / "secrets" / "providers" / "primary" / "api_key"
        provider_module = str(env.get("GOFLOW_VISION_PROVIDER_MODULE") or "").strip()
        if args.require_external_inputs or args.probe_provider:
            secret_source, provider_module_path, secret_value = validate_formal_vision_inputs(env)
            provider_module = str(provider_module_path)
        report["input_policy"].update(
            {
                "external_secret": bool(configured_secret),
                "external_provider_module": bool(provider_module),
                "read_only_enforced": args.require_external_inputs or args.probe_provider,
            }
        )
        if not secret_source.is_file() or secret_source.stat().st_size == 0:
            raise RuntimeError("configured primary Provider secret is missing")
        if "secret_value" not in locals():
            secret_value = secret_source.read_text(encoding="utf-8").strip()
        if not secret_value:
            raise RuntimeError("configured primary Provider secret is empty")
        if len(secret_value.encode("utf-8")) < 16:
            raise RuntimeError("configured primary Provider secret is too short for reliable leak detection")
        ephemeral_secret = data_root / "secrets" / "providers" / "primary" / "api_key"
        ephemeral_secret.parent.mkdir(parents=True, exist_ok=True)
        ephemeral_secret.write_text(secret_value + "\n", encoding="utf-8")
        install_provider_override(data_root, provider_module)
        create_workspace(workspace, env)
        implementation_before_sha256 = hashlib.sha256((workspace / "portcfg" / "port.go").read_bytes()).hexdigest()
        screenshot = render_fixture_png()
        screenshot_path = output / "vision-bug-report.png"
        screenshot_path.write_bytes(screenshot)
        width, height = png_dimensions(screenshot)
        report["fixture"] = {
            "image": screenshot_path.name,
            "sha256": hashlib.sha256(screenshot).hexdigest(),
            "width": width,
            "height": height,
            "requirements_only_in_image": True,
        }
        binary = output / ("goflow-vision.exe" if os.name == "nt" else "goflow-vision")
        run_checked(["go", "build", "-buildvcs=false", "-o", str(binary), "./cmd/goflow"], ROOT, env, timeout=300)
        port = free_port()
        process, log_handle = start_server(binary, workspace, port, env, output / "server.log")
        base_url = f"http://127.0.0.1:{port}"
        probe = provider_probe(base_url, api_key_reference="secret://providers/primary/api_key")
        report["provider"] = probe
        if args.probe_provider:
            hard_gates = vision_provider_probe_gates(probe)
            report["verification"] = {"hard_gates": hard_gates}
            report["passed"] = all(hard_gates.values())
            if not report["passed"]:
                failed = [name for name, passed in hard_gates.items() if not passed]
                raise RuntimeError("vision Provider capability probe failed: " + ", ".join(failed))
            raise ProviderProbeComplete
        if not probe.get("configured"):
            raise RuntimeError("configured Provider module did not bind the isolated secret reference")
        if not probe.get("valid"):
            raise RuntimeError("configured Provider did not pass the real connectivity and capability probe")
        if not bool((probe.get("capabilities") or {}).get("vision")):
            raise RuntimeError("configured Provider does not advertise verified vision input")
        if not bool((probe.get("capabilities") or {}).get("tool_calling")):
            raise RuntimeError("configured Provider does not advertise verified tool calling")
        attachment = upload_attachment(base_url, "vision-bug-report.png", screenshot)
        if attachment.get("hash") != report["fixture"]["sha256"]:
            raise RuntimeError("uploaded attachment hash does not match the generated screenshot")
        status, accepted = request_json(
            base_url,
            "/api/runs",
            {
                "input": TASK_PROMPT,
                "agent": "software-engineer",
                "skill": "code-writing",
                "background": True,
                "attachments": [attachment],
            },
            timeout=120,
        )
        if status != 202 or not isinstance(accepted, dict) or not accepted.get("run_id"):
            raise RuntimeError(f"vision task start failed: status={status} body={accepted}")
        run_id = str(accepted["run_id"])
        run, approvals, run_id = drive_run(base_url, "agent", run_id, args.timeout, max_auto_retries=1)
        evidence = collect_run_evidence(base_url, "agent", run_id)
        _, delivery_before = request_json(base_url, f"/api/runs/{run_id}/delivery")
        applied = apply_staged_changes_if_available(base_url, "agent", run_id, approvals)
        _, delivery_after = request_json(base_url, f"/api/runs/{run_id}/delivery")
        _, hydrated = request_json(base_url, f"/api/agent-runs/{run_id}")
        persisted = inspect_persisted_run(acceptance_workspace_data_root(data_root, workspace), "agent", run_id)
        terminal_quality = evaluate_terminal_quality(run, "agent")
        install_hidden_tests(workspace)
        verification = verify_workspace(workspace, env)
        implementation_after_sha256 = hashlib.sha256((workspace / "portcfg" / "port.go").read_bytes()).hexdigest()
        attachment_hashes = {
            str(item.get("hash") or item.get("ref") or "").removeprefix("sha256:")
            for item in (hydrated.get("attachments", []) if isinstance(hydrated, dict) else [])
            if isinstance(item, dict)
        }
        hard_gates = {
            "provider_vision": bool((probe.get("capabilities") or {}).get("vision")),
            "provider_tool_calling": bool((probe.get("capabilities") or {}).get("tool_calling")),
            "secret_not_leaked": True,
            "completed": str(run.get("status", "")).lower() in {"completed", "succeeded"},
            "attachment_persisted": report["fixture"]["sha256"] in attachment_hashes,
            "real_diff": "portcfg/port.go" in evidence.get("diff_paths", []),
            "tool_evidence": int(evidence.get("tool_results", 0) or 0) > 0,
            "terminal_quality": bool(terminal_quality.get("passed")),
            "delivery_applied": applied,
            "delivery_success": isinstance(delivery_after, dict) and delivery_after.get("conclusion") == "success",
            "persisted_session": bool(persisted.get("passed")),
            "hidden_tests": bool(verification.get("go_test")),
            "go_vet": bool(verification.get("go_vet")),
            "gofmt": bool(verification.get("gofmt")),
            "implementation_changed": implementation_after_sha256 != implementation_before_sha256,
        }
        report["run"] = {
            "id": run_id,
            "status": run.get("status"),
            "duration_ms": round((time.monotonic() - started) * 1000),
            "approvals": approvals,
            "terminal_quality": terminal_quality,
            "persisted_session": persisted,
            "delivery_before": delivery_before,
            "delivery_after": delivery_after,
        }
        report["evidence"] = evidence
        report["verification"] = {
            "hard_gates": hard_gates,
            "commands": verification,
            "implementation": {
                "before_sha256": implementation_before_sha256,
                "after_sha256": implementation_after_sha256,
            },
        }
        report["passed"] = all(hard_gates.values())
        if not report["passed"]:
            failed = [name for name, passed in hard_gates.items() if not passed]
            raise RuntimeError("vision engineering hard gates failed: " + ", ".join(failed))
    except ProviderProbeComplete:
        pass
    except Exception as error:  # noqa: BLE001 - every hard-gate failure becomes evidence.
        report["error"] = str(error)
    finally:
        stop_server(process, log_handle)
        report["duration_ms"] = round((time.monotonic() - started) * 1000)
        if not args.keep_workspace:
            report["workspace_preserved"] = not remove_tree_with_retry(workspace)
        else:
            report["workspace_preserved"] = True
        regenerated_paths = [output / "gocache", output / "gotmp", data_root]
        if "binary" in locals():
            regenerated_paths.append(binary)
        report["regenerated_artifacts_cleaned"] = all(
            remove_tree_with_retry(path) if path.is_dir() else remove_file_with_retry(path)
            for path in regenerated_paths
            if path.exists()
        )
        if report["workspace_preserved"] is not False or report["regenerated_artifacts_cleaned"] is not True:
            report["passed"] = False
            if not report["error"]:
                report["error"] = "acceptance did not clean sensitive generated state"
        secret = str(locals().get("secret_value") or "")
        sanitized_report, report_leaks = redact_secret_value(report, secret)
        report = sanitized_report
        evidence_scan = sanitize_evidence_files(output, secret)
        total_leaks = report_leaks + int(evidence_scan["leaks_detected"])
        report["secret_safety"] = {
            **evidence_scan,
            "report_leaks_detected": report_leaks,
            "total_leaks_detected": total_leaks,
        }
        gates = report.get("verification", {}).get("hard_gates", {})
        if isinstance(gates, dict) and "secret_not_leaked" in gates:
            gates["secret_not_leaked"] = total_leaks == 0 and evidence_scan["scan_complete"] is True
        if total_leaks or evidence_scan["scan_complete"] is not True:
            report["passed"] = False
            report["error"] = (
                "acceptance evidence contained Provider secret material and was redacted"
                if total_leaks
                else "acceptance evidence secret scan did not complete"
            )
        secret_value = ""
        report_path_for(output).write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"passed": report["passed"], "report": str(report_path_for(output)), "reason": report["error"]}, ensure_ascii=False, indent=2))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
