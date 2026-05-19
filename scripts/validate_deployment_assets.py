#!/usr/bin/env python3
"""Validate deployment files without requiring Docker to be installed."""

from __future__ import annotations

import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def read(path: str) -> str:
    full = ROOT / path
    if not full.exists():
        raise AssertionError(f"missing deployment asset: {path}")
    return full.read_text(encoding="utf-8")


def assert_contains(label: str, text: str, needles: list[str]) -> None:
    for needle in needles:
        if needle not in text:
            raise AssertionError(f"{label} missing {needle!r}")


def assert_not_contains(label: str, text: str, needles: list[str]) -> None:
    for needle in needles:
        if needle in text:
            raise AssertionError(f"{label} should not contain {needle!r}")


def validate_env_example() -> None:
    env = read(".env.example")
    if re.search(r"sk-[A-Za-z0-9]{16,}", env):
        raise AssertionError(".env.example appears to contain a real API key")
    assert_contains(
        ".env.example",
        env,
        [
            "GOFLOW_BASE_URL=",
            "GOFLOW_API_KEY=your-api-key",
            "GOFLOW_MODEL=",
            "GOFLOW_BACKUP_API_KEY=your-api-key",
        ],
    )


def validate_dockerfile() -> None:
    dockerfile = read("Dockerfile")
    assert_contains(
        "Dockerfile",
        dockerfile,
        [
            "FROM golang:1.25-bookworm AS builder",
            "FROM python:3.13-slim AS runtime",
            "go build -o /out/goflow ./cmd/goflow",
            "go build -o /out/file_tools ./mcp_servers/file_tools",
            "go build -o /out/skill_runner ./mcp_servers/skill_runner",
            "go build -o /out/web_tools ./mcp_servers/web_tools",
            "go build -o /out/network_tools ./mcp_servers/network_tools",
            "COPY --from=builder /out/file_tools /app/bin/file_tools",
            "COPY --from=builder /out/skill_runner /app/bin/skill_runner",
            "COPY --from=builder /out/web_tools /app/bin/web_tools",
            "COPY --from=builder /out/network_tools /app/bin/network_tools",
            'CMD ["--config", "/app/configs/goflow.docker.yaml", "--workspace", "/workspace", "--http", ":8080"]',
        ],
    )
    mcp_python = read("docker/mcp-python/Dockerfile")
    assert_contains(
        "docker/mcp-python/Dockerfile",
        mcp_python,
        [
            "FROM python:3.13-slim",
            "GoFlow MCP Python Tool Runtime",
            "PYTHONDONTWRITEBYTECODE=1",
            "PYTHONUNBUFFERED=1",
            "mkdir -p /workspace /goflow-tools",
            'CMD ["python", "--version"]',
        ],
    )


def validate_docker_config() -> None:
    config = read("configs/goflow.docker.yaml")
    assert_contains(
        "configs/goflow.docker.yaml",
        config,
        [
            "command: /app/bin/file_tools",
            "command: /app/bin/skill_runner",
            "command: /app/bin/web_tools",
            "command: /app/bin/network_tools",
            "command: python3",
            "- /app/mcp_servers/python_notes.py",
            "allowed_command_paths:",
            "- /app/bin",
            "max_concurrent_calls: 1",
            "persist_path: /workspace/.goflow/session.json",
            "directory: /app/skills",
        ],
    )
    assert_not_contains("configs/goflow.docker.yaml", config, ["go run", "command: go"])


def validate_binary_config() -> None:
    config = read("configs/goflow.binary.yaml")
    assert_contains(
        "configs/goflow.binary.yaml",
        config,
        [
            "command: ${GOFLOW_FILE_TOOLS_CMD}",
            "command: ${GOFLOW_SKILL_RUNNER_CMD}",
            "command: ${GOFLOW_WEB_TOOLS_CMD}",
            "command: ${GOFLOW_NETWORK_TOOLS_CMD}",
            "command: ${GOFLOW_PYTHON_CMD}",
            "- ${GOFLOW_PYTHON_NOTES_PATH}",
            "allowed_commands:",
            "max_concurrent_calls: 1",
            "directory: ./skills",
        ],
    )


def validate_compose() -> None:
    compose = read("docker-compose.yml")
    assert_contains(
        "docker-compose.yml",
        compose,
        [
            "goflow-agent:local",
            ".env",
            "./workspace:/workspace",
            "--config",
            "/app/configs/goflow.docker.yaml",
            "--workspace",
            "/workspace",
            "--http",
            ":8080",
        ],
    )


def validate_ci() -> None:
    ci = read(".github/workflows/ci.yml")
    assert_contains(
        ".github/workflows/ci.yml",
        ci,
        [
            "python scripts/run_preflight.py --browser-required",
            "docker build -t goflow-agent:ci .",
            "docker build -f docker/mcp-python/Dockerfile -t goflow-agent-mcp-python:ci .",
            "http://127.0.0.1:18080/api/session",
        ],
    )


def validate_release_workflow() -> None:
    workflow = read(".github/workflows/release.yml")
    assert_contains(
        ".github/workflows/release.yml",
        workflow,
        [
            "preflight:",
            "name: release preflight",
            "python scripts/run_preflight.py --browser-required",
            "needs: preflight",
            "python scripts/build_release_assets.py --clean --version",
            "python scripts/generate_sbom.py --version",
            "python scripts/sign_release_artifacts.py --dist dist",
            "python scripts/validate_release_archives.py --dist dist --version",
            "--require-sbom",
            "--require-signatures",
            "sigstore/cosign-installer@v3",
            "cosign sign --yes",
            "actions/upload-artifact@v4",
            "softprops/action-gh-release@v2",
            "docker/login-action@v3",
            "docker/build-push-action@v6",
            "ghcr.io/${GITHUB_REPOSITORY,,}",
            "mcp_python",
            "docker/mcp-python/Dockerfile",
        ],
    )
    preflight = read("scripts/run_preflight.py")
    assert_contains(
        "scripts/run_preflight.py",
        preflight,
        [
            "go test",
            "validate_no_secrets.py",
            "validate_python_mcp.py",
            "validate_extension_workflow.py",
            "validate_binary_analysis_kit.py",
            "validate_resource_links.py",
            "validate_web_i18n.py",
            "validate_docs.py",
            "smoke_http_studio.py",
            "smoke_http_browser.py",
            "--browser-required",
            "--skip-browser",
            "--release-target",
            "build_release_assets.py",
            "validate_release_archives.py",
            "validate_deployment_assets.py",
        ],
    )


def validate_release_script() -> None:
    script = read("scripts/build_release_assets.py")
    assert_contains(
        "scripts/build_release_assets.py",
        script,
        [
            '("linux", "amd64")',
            '("linux", "arm64")',
            '("windows", "amd64")',
            '("windows", "arm64")',
            '("goflow", "./cmd/goflow")',
            '("file_tools", "./mcp_servers/file_tools")',
            '("skill_runner", "./mcp_servers/skill_runner")',
            '("web_tools", "./mcp_servers/web_tools")',
            '("network_tools", "./mcp_servers/network_tools")',
            'COMMON_DIRS = ["configs", "skills", "docs", "kits", "templates", "examples"]',
            "goflow.binary.yaml",
            "run-goflow.sh",
            "run-goflow.cmd",
            "--http :8080",
            "Starting GoFlow Web Studio",
            "GOFLOW_BACKUP_MODEL",
            "SHA256SUMS",
            "hashlib.sha256",
            "archive_base.parent",
        ],
    )
    engineering_template = read("templates/workflows/engineering-parallel-delivery.yaml")
    assert_contains(
        "templates/workflows/engineering-parallel-delivery.yaml",
        engineering_template,
        ["engineering-parallel-delivery", "worker_contract", "quality_gate"],
    )
    python_tool_config = read("internal/scaffold/templates/tools/python/config.yaml.tmpl")
    assert_contains(
        "internal/scaffold/templates/tools/python/config.yaml.tmpl",
        python_tool_config,
        ["max_concurrent_calls: 1"],
    )
    materialized_tool_config = read("internal/scaffold/templates/kits/materialized/generic/tool-config.yaml.tmpl")
    assert_contains(
        "internal/scaffold/templates/kits/materialized/generic/tool-config.yaml.tmpl",
        materialized_tool_config,
        ["max_concurrent_calls: 1"],
    )
    sbom_generator = read("scripts/generate_sbom.py")
    assert_contains(
        "scripts/generate_sbom.py",
        sbom_generator,
        [
            "SPDX-2.3",
            "go list",
            "PACKAGE-MANAGER",
            "purl",
            "SBOM.spdx.json",
            "SHA256SUMS",
        ],
    )
    release_archive_validator = read("scripts/validate_release_archives.py")
    assert_contains(
        "scripts/validate_release_archives.py",
        release_archive_validator,
        [
            "--target",
            "parse_target",
            "COMMON_ARCHIVE_MEMBERS",
            "configs/goflow.binary.yaml",
            "configs/agents/chat.yaml",
            "configs/providers/primary.yaml",
            "configs/providers/backup.yaml",
            "configs/mcp_servers/python_notes.yaml",
            "configs/mcp_servers/network_tools.yaml",
            "skills/execution-plan/SKILL.md",
            "docs/install.md",
            "mcp_servers/python_notes.py",
            "validate_archive_layout",
            "linux_amd64.tar.gz",
            "linux_arm64.tar.gz",
            "windows_amd64.zip",
            "windows_arm64.zip",
            "SHA256SUMS",
            "SBOM.spdx.json",
            "--require-sbom",
            "--require-signatures",
            ".sigstore.json",
        ],
    )
    signer = read("scripts/sign_release_artifacts.py")
    assert_contains(
        "scripts/sign_release_artifacts.py",
        signer,
        [
            "cosign",
            "sign-blob",
            "--bundle",
            ".sigstore.json",
            "release_artifacts",
        ],
    )


def validate_release_docs() -> None:
    docs = read("docs/release.md")
    assert_contains(
        "docs/release.md",
        docs,
        [
            "python scripts/build_release_assets.py --clean --version",
            "scripts/validate_release_archives.py",
            "--target linux/amd64",
            "configs/goflow.binary.yaml",
            "SHA256SUMS",
            "SBOM.spdx.json",
            ".sigstore.json",
            "cosign verify-blob",
            "ghcr.io/fymatt/goflow-agent:<tag>",
            "ghcr.io/fymatt/goflow-agent-mcp-python:<tag>",
            "configs/goflow.docker.yaml",
            "python scripts/run_preflight.py --browser-required",
            "python scripts/run_preflight.py --browser-required --release-target",
            "python scripts/run_preflight.py --skip-browser",
            "validate_no_secrets.py",
        ],
    )


def validate_install_docs() -> None:
    install = read("docs/install.md")
    assert_contains(
        "docs/install.md",
        install,
        [
            "ghcr.io/fymatt/goflow-agent:<version>",
            "run-goflow.cmd",
            "run-goflow.sh",
            "docker run --rm -it",
            "docker compose up --build",
            "curl http://127.0.0.1:8080/api/session",
            "SBOM.spdx.json",
            ".sigstore.json",
            "<workspace>/.goflow/session.json",
        ],
    )
    install_zh = read("docs/install.zh-CN.md")
    assert_contains(
        "docs/install.zh-CN.md",
        install_zh,
        [
            "ghcr.io/fymatt/goflow-agent:<version>",
            "run-goflow.cmd",
            "run-goflow.sh",
            "docker run --rm -it",
            "docker compose up --build",
            "curl http://127.0.0.1:8080/api/session",
            "SBOM.spdx.json",
            ".sigstore.json",
            "<workspace>/.goflow/session.json",
        ],
    )


def main() -> int:
    validate_env_example()
    validate_dockerfile()
    validate_docker_config()
    validate_binary_config()
    validate_compose()
    validate_ci()
    validate_release_workflow()
    validate_release_script()
    validate_release_docs()
    validate_install_docs()
    print("deployment asset validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
