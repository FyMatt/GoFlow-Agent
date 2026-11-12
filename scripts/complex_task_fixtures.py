#!/usr/bin/env python3
"""Build and independently inspect repository-scale complex-task fixtures.

The generated support files are intentionally boring but real: they are
version-controlled inputs that exercise repository mapping and bounded
context retrieval without encouraging the model to edit unrelated files.
Hidden acceptance metadata is installed only after the run has finished.
"""

from __future__ import annotations

import argparse
import json
import tempfile
from pathlib import Path


MIN_VISIBLE_FILES = 120
MODULE_COUNT = 3

FAILURE_MODES = {
    "development": "验证失败后只重试失败分片",
    "security": "授权范围不足时停止写入并保留审计证据",
    "operations": "目标不可达时停止扩散并精确回滚",
    "research": "来源失效后按新快照重新裁决",
    "documents": "批量文档失败时原子保留源文件",
    "cross-domain": "领域质量门失败时阻止统一交付",
}

MODULE_NAMES = {
    "development": ("api", "persistence", "web"),
    "security": ("identity", "input-boundary", "dependencies"),
    "operations": ("inventory", "rollout", "health"),
    "research": ("sources", "claims", "evidence"),
    "documents": ("templates", "data", "export"),
    "cross-domain": ("software", "security", "delivery"),
}


def fixture_family(case_id: str, domain: str) -> str:
    if case_id.endswith("cross-domain"):
        return "cross-domain"
    return domain if domain in MODULE_NAMES else "cross-domain"


def augment_large_fixture(workspace: Path, case_id: str, domain: str) -> dict[str, object]:
    """Add a deterministic repository-scale input set before git initialization."""

    family = fixture_family(case_id, domain)
    root = workspace / "fixture-modules"
    root.mkdir(parents=True, exist_ok=True)
    modules = MODULE_NAMES[family]
    for module_index, module in enumerate(modules):
        module_root = root / module
        (module_root / "records").mkdir(parents=True, exist_ok=True)
        (module_root / "README.md").write_text(
            f"# {module}\n\n领域：{family}\n依赖：{modules[module_index - 1] if module_index else '无'}\n",
            encoding="utf-8",
        )
        (module_root / "contract.txt").write_text(
            f"module={module}\ndepends_on={modules[module_index - 1] if module_index else 'none'}\n",
            encoding="utf-8",
        )
        for record_index in range(40):
            record = module_root / "records" / f"record-{record_index:03d}.md"
            record.write_text(
                f"fixture={case_id}\nmodule={module}\nrecord={record_index:03d}\n"
                f"input_contract=stable\nverification_boundary={family}\n",
                encoding="utf-8",
            )

    visible_files = sorted(
        path.relative_to(workspace).as_posix()
        for path in root.rglob("*")
        if path.is_file()
    )
    manifest = {
        "schema_version": 1,
        "case_id": case_id,
        "domain": family,
        "minimum_visible_files": MIN_VISIBLE_FILES,
        "visible_files": len(visible_files),
        "module_count": len(modules),
        "modules": list(modules),
        "failure_mode": FAILURE_MODES[family],
        "hidden_acceptance": f"acceptance/hidden/{case_id}.json",
    }
    (workspace / "fixture-manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    return manifest


def install_hidden_acceptance(workspace: Path, case_id: str, domain: str) -> Path:
    """Install acceptance metadata only after the agent has completed delivery."""

    family = fixture_family(case_id, domain)
    target = workspace / "acceptance" / "hidden" / f"{case_id}.json"
    target.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "schema_version": 1,
        "case_id": case_id,
        "checks": [
            "fixture-manifest.json.visible_files >= 120",
            "fixture-manifest.json.module_count == 3",
            "fixture-manifest.json.modules form a dependency chain",
            "delivery does not modify fixture-modules/",
            "failure recovery evidence matches the declared domain contract",
        ],
        "failure_injection": {
            "mode": FAILURE_MODES[family],
            "must_be_bounded": True,
            "must_preserve_completed_side_effects": True,
        },
    }
    target.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return target


def inspect_large_fixture(workspace: Path, case_id: str, domain: str) -> dict[str, object]:
    manifest_path = workspace / "fixture-manifest.json"
    if not manifest_path.is_file():
        return {"passed": False, "failures": ["fixture-manifest.json is missing"]}
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        return {"passed": False, "failures": [f"fixture manifest is invalid: {exc}"]}
    failures: list[str] = []
    family = fixture_family(case_id, domain)
    visible_files = [
        path
        for path in (workspace / "fixture-modules").rglob("*")
        if path.is_file()
    ]
    if len(visible_files) < MIN_VISIBLE_FILES:
        failures.append(f"visible fixture files: {len(visible_files)} < {MIN_VISIBLE_FILES}")
    modules = manifest.get("modules")
    if manifest.get("module_count") != MODULE_COUNT or not isinstance(modules, list) or len(modules) != MODULE_COUNT:
        failures.append("fixture does not expose exactly three modules")
    if manifest.get("domain") != family:
        failures.append("fixture domain does not match case")
    for index, module in enumerate(modules if isinstance(modules, list) else []):
        module_path = workspace / "fixture-modules" / str(module) / "contract.txt"
        if not module_path.is_file():
            failures.append(f"module contract missing: {module}")
        elif index and f"depends_on={modules[index - 1]}" not in module_path.read_text(encoding="utf-8"):
            failures.append(f"module dependency missing: {module}")
    hidden = workspace / "acceptance" / "hidden" / f"{case_id}.json"
    if not hidden.is_file():
        failures.append("hidden acceptance metadata was not installed")
    return {
        "passed": not failures,
        "failures": failures,
        "visible_files": len(visible_files),
        "module_count": manifest.get("module_count"),
        "failure_mode": manifest.get("failure_mode"),
    }


def self_test() -> None:
    for case_id, domain in (
        ("dev-04-ttl-cache", "development"),
        ("sec-05-fix-regression", "security"),
        ("ops-05-controlled-change", "operations"),
        ("research-05-cited-report", "research"),
        ("docs-05-semantic-edit", "documents"),
        ("docs-06-cross-domain", "documents"),
    ):
        with tempfile.TemporaryDirectory(prefix="goflow-complex-fixture-") as raw:
            workspace = Path(raw)
            manifest = augment_large_fixture(workspace, case_id, domain)
            if int(manifest["visible_files"]) < MIN_VISIBLE_FILES:
                raise AssertionError(manifest)
            install_hidden_acceptance(workspace, case_id, domain)
            result = inspect_large_fixture(workspace, case_id, domain)
            if not result["passed"]:
                raise AssertionError(result)
    print("complex task fixture self-test passed (six domains, 120+ files, three-module chains)")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        self_test()
        return 0
    parser.error("use --self-test")
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
