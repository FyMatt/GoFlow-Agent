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
            "go build -o /out/web_tools ./mcp_servers/web_tools",
            "COPY --from=builder /out/file_tools /app/bin/file_tools",
            "COPY --from=builder /out/web_tools /app/bin/web_tools",
            'CMD ["--config", "/app/configs/agent.docker.yaml", "--workspace", "/workspace", "--http", ":8080"]',
        ],
    )


def validate_docker_config() -> None:
    config = read("configs/agent.docker.yaml")
    assert_contains(
        "configs/agent.docker.yaml",
        config,
        [
            "command: /app/bin/file_tools",
            "command: /app/bin/web_tools",
            "command: python3",
            "- /app/mcp_servers/python_notes.py",
            "allowed_command_paths:",
            "- /app/bin",
            "persist_path: /workspace/.goflow/session.json",
            "directory: /app/skills",
        ],
    )
    assert_not_contains("configs/agent.docker.yaml", config, ["go run", "command: go"])


def validate_binary_config() -> None:
    config = read("configs/agent.binary.yaml")
    assert_contains(
        "configs/agent.binary.yaml",
        config,
        [
            "command: ${GOFLOW_FILE_TOOLS_CMD}",
            "command: ${GOFLOW_WEB_TOOLS_CMD}",
            "command: ${GOFLOW_PYTHON_CMD}",
            "- ${GOFLOW_PYTHON_NOTES_PATH}",
            "allowed_commands:",
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
            "/app/configs/agent.docker.yaml",
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
            "go test ./...",
            "python scripts/validate_python_mcp.py",
            "python scripts/validate_extension_workflow.py",
            "python scripts/validate_deployment_assets.py",
            "docker build -t goflow-agent:ci .",
            "http://127.0.0.1:18080/api/session",
        ],
    )


def validate_release_workflow() -> None:
    workflow = read(".github/workflows/release.yml")
    assert_contains(
        ".github/workflows/release.yml",
        workflow,
        [
            "python scripts/build_release_assets.py --clean --version",
            "python scripts/validate_release_archives.py --dist dist --version",
            "actions/upload-artifact@v4",
            "softprops/action-gh-release@v2",
            "docker/login-action@v3",
            "docker/build-push-action@v6",
            "ghcr.io/${GITHUB_REPOSITORY,,}",
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
            '("web_tools", "./mcp_servers/web_tools")',
            "agent.binary.yaml",
            "run-goflow.sh",
            "run-goflow.cmd",
            "SHA256SUMS",
            "hashlib.sha256",
            "archive_base.parent",
        ],
    )
    release_archive_validator = read("scripts/validate_release_archives.py")
    assert_contains(
        "scripts/validate_release_archives.py",
        release_archive_validator,
        [
            "linux_amd64.tar.gz",
            "linux_arm64.tar.gz",
            "windows_amd64.zip",
            "windows_arm64.zip",
            "SHA256SUMS",
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
            "configs/agent.binary.yaml",
            "SHA256SUMS",
            "ghcr.io/<owner>/<repo>:<tag>",
            "configs/agent.docker.yaml",
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
    print("deployment asset validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
