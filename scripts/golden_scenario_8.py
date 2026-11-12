#!/usr/bin/env python3
"""Run deterministic internet-research delivery and recovery scenarios."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ARTIFACT_ROOT = ROOT / ".artifacts" / "golden-scenario-8"


class ResearchSourceServer(BaseHTTPRequestHandler):
    flaky_calls = 0
    lock = threading.Lock()

    def log_message(self, _format: str, *_args: object) -> None:
        return

    def do_GET(self) -> None:  # noqa: N802
        if self.path.startswith("/flaky"):
            with self.lock:
                type(self).flaky_calls += 1
                current = type(self).flaky_calls
            if current == 1:
                self.send_response(503)
                self.end_headers()
                self.wfile.write("临时不可用".encode("utf-8"))
                return
            self._page("恢复来源", "资料服务已恢复，版本 2.0 于 2026 年发布。", "Mon, 02 Feb 2026 00:00:00 GMT")
            return
        if self.path.startswith("/official") or self.path.startswith("/mirror"):
            self._page("官方发布说明", "产品 2.0 已于 2026 年发布，并提供结构化导出。", "Thu, 01 Jan 2026 00:00:00 GMT")
            return
        if self.path.startswith("/standard"):
            self._page("行业标准说明", "标准记录确认产品 2.0 在 2026 年进入稳定发布阶段。", "Fri, 02 Jan 2026 00:00:00 GMT")
            return
        if self.path.startswith("/conflict"):
            self._page("历史社区记录", "社区记录声称产品 2.0 尚未正式发布。", "Mon, 01 Jan 2024 00:00:00 GMT")
            return
        self.send_error(404)

    def _page(self, title: str, body: str, modified: str) -> None:
        content = f"<html><head><title>{title}</title></head><body><main>{body}</main></body></html>".encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Last-Modified", modified)
        self.send_header("Content-Length", str(len(content)))
        self.end_headers()
        self.wfile.write(content)


class MCPProcess:
    def __init__(self, cache_dir: Path) -> None:
        env = os.environ.copy()
        env["GOFLOW_RESEARCH_CACHE_DIR"] = str(cache_dir)
        self.process = subprocess.Popen(
            ["go", "run", "./mcp_servers/web_tools"],
            cwd=ROOT,
            env=env,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
        )
        self.next_id = 1

    def call(self, name: str, arguments: dict) -> dict:
        if self.process.stdin is None or self.process.stdout is None:
            raise RuntimeError("MCP process streams are unavailable")
        request_id = self.next_id
        self.next_id += 1
        payload = {"jsonrpc": "2.0", "id": request_id, "method": "tools/call", "params": {"name": name, "arguments": arguments}}
        self.process.stdin.write(json.dumps(payload, ensure_ascii=False) + "\n")
        self.process.stdin.flush()
        line = self.process.stdout.readline()
        if not line:
            stderr = self.process.stderr.read() if self.process.stderr is not None else ""
            raise RuntimeError(f"MCP process stopped before replying: {stderr}")
        response = json.loads(line)
        result = response.get("result") or {}
        if result.get("is_error"):
            raise RuntimeError(str(result.get("content")))
        return json.loads(str(result.get("content") or "{}"))

    def close(self) -> None:
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)


def source_from_snapshot(item: dict, source_id: str, source_type: str, *, domain: str = "", supports: list[str] | None = None, contradicts: list[str] | None = None) -> dict:
    return {
        "id": source_id,
        "url": item["canonical_url"],
        "title": item.get("title", ""),
        "domain": domain or item.get("domain", ""),
        "published_at": item.get("published_at", ""),
        "fetched_at": item.get("fetched_at", ""),
        "source_type": source_type,
        "excerpt": item.get("excerpt", ""),
        "content_sha256": item.get("content_sha256", ""),
        "supports": supports or [],
        "contradicts": contradicts or [],
    }


def write_deliverables(root: Path, compiled: dict) -> dict[str, str]:
    root.mkdir(parents=True, exist_ok=True)
    paths = {
        "markdown": root / "report.md",
        "json": root / "evidence.json",
        "csv": root / "claim-sources.csv",
    }
    for key, path in paths.items():
        path.write_text(str(compiled[key]), encoding="utf-8-sig" if key == "csv" else "utf-8")
    return {key: str(path.relative_to(ROOT)) for key, path in paths.items()}


def run_scenario() -> dict:
    ARTIFACT_ROOT.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), ResearchSourceServer)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    base_url = f"http://127.0.0.1:{server.server_port}"

    with tempfile.TemporaryDirectory(prefix="goflow-research-cache-") as cache_raw:
        mcp = MCPProcess(Path(cache_raw))
        try:
            simple_snapshot = mcp.call(
                "research_snapshot",
                {"urls": [f"{base_url}/official", f"{base_url}/official#release", f"{base_url}/standard", f"{base_url}/mirror"], "cache_ttl_seconds": 21600},
            )
            if simple_snapshot["unique_url_count"] != 3 or simple_snapshot["unique_content_count"] != 2:
                raise AssertionError(f"simple deduplication failed: {simple_snapshot}")
            snapshots = simple_snapshot["snapshots"]
            official = next(item for item in snapshots if item["canonical_url"].endswith("/official"))
            standard = next(item for item in snapshots if item["canonical_url"].endswith("/standard"))
            mirror = next(item for item in snapshots if item["canonical_url"].endswith("/mirror"))
            if not mirror.get("duplicate_of"):
                raise AssertionError(f"mirror content was not marked duplicate: {mirror}")

            simple_sources = [
                source_from_snapshot(official, "S001", "official", domain="official.example", supports=["C001"]),
                source_from_snapshot(standard, "S002", "standard", domain="standards.example", supports=["C001"]),
            ]
            simple_compiled = mcp.call(
                "compile_research_evidence",
                {
                    "topic": "产品 2.0 发布状态核验",
                    "claims": [{"id": "C001", "text": "产品 2.0 已于 2026 年进入正式发布阶段。", "importance": "关键", "source_ids": ["S001", "S002"], "confidence": "高"}],
                    "sources": simple_sources,
                    "stale_after_days": 3650,
                },
            )
            if simple_compiled["review_status"] != "通过":
                raise AssertionError(f"simple research should pass: {simple_compiled}")
            simple_paths = write_deliverables(ARTIFACT_ROOT / "simple", simple_compiled)

            conflict_snapshot = mcp.call("research_snapshot", {"urls": [f"{base_url}/conflict"]})["snapshots"][0]
            conflict_sources = simple_sources + [source_from_snapshot(conflict_snapshot, "S003", "community", contradicts=["C001"])]
            complex_compiled = mcp.call(
                "compile_research_evidence",
                {
                    "topic": "产品 2.0 多来源冲突研究",
                    "claims": [{"id": "C001", "text": "产品 2.0 已正式发布。", "importance": "关键", "source_ids": ["S001", "S002", "S003"], "confidence": "中"}],
                    "sources": conflict_sources,
                    "stale_after_days": 365,
                },
            )
            if complex_compiled["review_status"] == "通过" or complex_compiled["summary"]["conflicted_claims"] != 1:
                raise AssertionError(f"conflicting research should not pass: {complex_compiled}")
            complex_paths = write_deliverables(ARTIFACT_ROOT / "complex-conflict", complex_compiled)

            failed_snapshot = mcp.call("research_snapshot", {"urls": [f"{base_url}/flaky"]})["snapshots"][0]
            if not failed_snapshot.get("is_error") or failed_snapshot.get("cache_hit"):
                raise AssertionError(f"temporary failure was not isolated: {failed_snapshot}")
            recovered_snapshot = mcp.call("research_snapshot", {"urls": [f"{base_url}/flaky"]})["snapshots"][0]
            if recovered_snapshot.get("is_error") or recovered_snapshot.get("cache_hit"):
                raise AssertionError(f"source did not recover through a live retry: {recovered_snapshot}")
            recovered_source = source_from_snapshot(recovered_snapshot, "S004", "official", supports=["C002"])
            recovered_compiled = mcp.call(
                "compile_research_evidence",
                {
                    "topic": "临时失败来源恢复",
                    "claims": [{"id": "C002", "text": "资料服务恢复后提供了 2026 年版本信息。", "importance": "关键", "source_ids": ["S004"], "confidence": "中", "notes": "单一官方来源，保留限制。"}],
                    "sources": [recovered_source],
                    "stale_after_days": 3650,
                },
            )
            if recovered_compiled["review_status"] not in {"通过", "需复核"}:
                raise AssertionError(f"recovered source should be deliverable with limitation: {recovered_compiled}")
            recovery_paths = write_deliverables(ARTIFACT_ROOT / "failure-recovery", recovered_compiled)

            cached = mcp.call("research_snapshot", {"urls": [f"{base_url}/official", f"{base_url}/standard"]})
            if cached["cache_hit_count"] != 2:
                raise AssertionError(f"expected cache reuse after research delivery: {cached}")

            result = {
                "status": "passed",
                "simple": {"review_status": simple_compiled["review_status"], "snapshot_metrics": simple_snapshot, "deliverables": simple_paths},
                "complex_conflict": {"review_status": complex_compiled["review_status"], "summary": complex_compiled["summary"], "deliverables": complex_paths},
                "failure_recovery": {"initial_status": failed_snapshot.get("status"), "recovered_status": recovered_snapshot.get("status"), "review_status": recovered_compiled["review_status"], "deliverables": recovery_paths},
                "cache_reuse": {"cache_hit_count": cached["cache_hit_count"], "unique_content_count": cached["unique_content_count"]},
            }
            (ARTIFACT_ROOT / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
            return result
        finally:
            mcp.close()
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--required", action="store_true", help="保留与统一验收脚本一致的调用方式。")
    parser.parse_args()
    result = run_scenario()
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
