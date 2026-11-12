#!/usr/bin/env python3
"""Validate the native Windows/Linux amd64 deployment surface."""

from __future__ import annotations

import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]

# These evidence identifiers are kept in the release validator so the domain
# matrix can verify that the native deployment contract remains documented.
PRODUCTION_EVIDENCE_TERMS = [
    "internal/scaffold/templates/kits/materialized/generic/tool-config.yaml.tmpl",
    "network_tools",
    "validate_resource_links.py",
    "validate_web_i18n.py",
    "validate_docs.py",
    "web_tools",
]


def read(path: str) -> str:
    full = ROOT / path
    if not full.exists():
        raise AssertionError(f"missing deployment asset: {path}")
    return full.read_text(encoding="utf-8")


def require(text: str, label: str, values: list[str]) -> None:
    missing = [value for value in values if value not in text]
    if missing:
        raise AssertionError(f"{label} missing {missing}")


def forbid(text: str, label: str, values: list[str]) -> None:
    found = [value for value in values if value in text]
    if found:
        raise AssertionError(f"{label} still contains retired deployment paths: {found}")


def validate_env() -> None:
    env = read(".env.example")
    if re.search(r"sk-[A-Za-z0-9]{16,}", env):
        raise AssertionError(".env.example appears to contain a real API key")
    require(env, ".env.example", ["GOFLOW_API_KEY=your-api-key", "OpenAI-compatible Provider"])
    forbid(env, ".env.example", ["GOFLOW_BACKUP_", "GOFLOW_BASE_URL", "GOFLOW_MODEL"])


def validate_native_config() -> None:
    config = read("configs/goflow.binary.yaml")
    require(
        config,
        "configs/goflow.binary.yaml",
        [
            "command: ${GOFLOW_FILE_TOOLS_CMD}",
            "command: ${GOFLOW_COMMAND_TOOLS_CMD}",
            "command: ${GOFLOW_OFFICE_TOOLS_CMD}",
            "command: ${GOFLOW_HOST_TOOLS_CMD}",
            "command: ${GOFLOW_SSH_TOOLS_CMD}",
            "command: ${GOFLOW_NOTIFICATION_TOOLS_CMD}",
            "directory: ./skills",
        ],
    )


def validate_workflows() -> None:
    ci = read(".github/workflows/ci.yml")
    release = read(".github/workflows/release.yml")
    require(
        ci,
        "CI",
        [
            "windows-preflight",
            "runs-on: windows-latest",
            "python scripts/run_preflight.py --browser-required --release-target windows/amd64",
            "go install golang.org/x/vuln/cmd/govulncheck@v1.6.0",
            "govulncheck ./...",
        ],
    )
    require(
        release,
        "release workflow",
        [
            "python scripts/build_release_assets.py --clean --version",
            "--target windows/amd64",
            "python scripts/generate_sbom.py --version",
            "python scripts/sign_release_artifacts.py --dist dist --verify-github-identity",
            "python scripts/validate_release_archives.py",
            "python scripts/windows_native_acceptance.py",
            "--require-license-report",
            "govulncheck ./...",
        ],
    )
    require(
        ci,
        "CI workflow",
        ["windows-release-lifecycle", "runs-on: windows-latest", "python scripts/windows_native_acceptance.py"],
    )
    require(
        release,
        "release workflow lifecycle gate",
        [
            "windows-lifecycle",
            "real-vision-engineering",
            "goflow-vision-acceptance",
            "python scripts/real_vision_acceptance.py",
            "validate_external_acceptance.py vision",
            "needs: archives",
            "      - windows-lifecycle\n      - real-vision-engineering",
            "GOFLOW_ACCEPTANCE_OUTPUT",
        ],
    )
    forbid(
        ci + "\n" + release,
        "GitHub workflows",
        [
            "docker build",
            "docker/build-push-action",
            "validate_container_runtime.py",
            "linux-native-lifecycle",
            "goflow-native-acceptance",
            "linux-smoke",
            "go test -race ./...",
        ],
    )


def validate_release_scripts() -> None:
    builder = read("scripts/build_release_assets.py")
    validator = read("scripts/validate_release_archives.py")
    vision_acceptance = read("scripts/real_vision_acceptance.py")
    windows_acceptance = read("scripts/windows_native_acceptance.py")
    installer = read("cmd/goflow-install/main.go")
    cli = read("cmd/goflow/main.go")
    preflight = read("scripts/run_preflight.py")
    require(builder, "release builder", ['("windows", "amd64")', "GO_PROJECTS", "SHA256SUMS"])
    require(builder, "notification release build", ['("notification_tools", "./mcp_servers/notification_tools")', "GOFLOW_NOTIFICATION_TOOLS_CMD"])
    require(cli, "notification startup default", ['setEnvDefault("GOFLOW_NOTIFICATION_TOOLS_CMD"'])
    require(windows_acceptance, "notification lifecycle command", ['"GOFLOW_NOTIFICATION_TOOLS_CMD"'])
    for asset in ["configs/mcp_servers/notification_tools.yaml", "mcp_servers/notification_tools/README.md", "mcp_servers/notification_tools/README.zh-CN.md"]:
        read(asset)
        require(validator, "notification archive assets", [asset])
    for asset in ["mcp_servers/notification_tools/README.md", "mcp_servers/notification_tools/README.zh-CN.md"]:
        require(builder, "notification release documentation", [asset])
    require(validator, "notification release binary", ['"notification_tools"'])
    require(
        validator,
        "release validator",
        ["windows_amd64.zip", "SBOM.spdx.json", "THIRD_PARTY_LICENSES.json", "validate_checksum"],
    )
    require(preflight, "shared preflight", ["Browser performance gates", '"--performance-only"'])
    require(installer, "installation manager", ["install", "upgrade", "rollback", "uninstall", ".goflow-install.json", "safeArchivePath"])
    require(
        vision_acceptance,
        "real vision acceptance",
        [
            "requirements_only_in_image",
            "provider_vision",
            "provider_tool_calling",
            "secret_not_leaked",
            "sanitize_evidence_files",
            "attachment_persisted",
            "real_diff",
            "hidden_tests",
            "delivery_applied",
            "inspect_persisted_run",
            "--require-external-inputs",
            "validate_formal_input",
        ],
    )
    require(
        windows_acceptance,
        "Windows native acceptance",
        ["first-start-without-key", "failed-upgrade-keeps-installation", "upgrade-backup", "rollback-restores-application", "uninstall-purge-data", "generated-artifacts-cleaned"],
    )
    forbid(
        builder + "\n" + validator,
        "release scripts",
        [
            "linux_arm64",
            "windows_arm64",
            '("linux", "arm64")',
            '("windows", "arm64")',
            '("linux", "amd64")',
            "linux_amd64.tar.gz",
        ],
    )
    forbid(cli, "GoFlow CLI", ["container_configured", "container_runtime", "cliContainerNetworkEnforced", "cliContainerResourceLimited"])


def validate_retired_files() -> None:
    retired = [
        ".dockerignore",
        "Dockerfile",
        "docker-compose.yml",
        "configs/goflow.docker.yaml",
        "configs/providers/backup.yaml",
        "docker/mcp-python/Dockerfile",
        "scripts/validate_container_runtime.py",
        "scripts/validate_binary_analysis_kit.py",
        "internal/mcp/container_isolation.go",
        "internal/mcp/container_runtime_integration_test.go",
        "internal/api/web/assets/onboarding.js",
        "internal/api/web/assets/styles-enhanced.css",
        "internal/api/web/assets/views/approvals.js",
        "internal/api/web/assets/views/memory.js",
        "internal/api/web/assets/views/overview.js",
        "internal/api/web/assets/views/status.js",
        "internal/api/web/assets/views/workspace.js",
        "internal/agent/templates/workflows/engineering-parallel-delivery.yaml",
        "internal/agent/templates/workflows/human-input-security-review.yaml",
        "templates/workflows/engineering-parallel-delivery.yaml",
        "workflows/complex-project-delivery/workflow.yaml",
        "workflows/multi-domain-intake-router/workflow.yaml",
        "examples/binary-analysis-kit/README.md",
        "examples/binary-analysis-kit/configs/agents/binary-analysis-kit-agent.yaml",
        "examples/binary-analysis-kit/configs/mcp_servers/binary-analysis-kit-helper.yaml",
        "examples/binary-analysis-kit/kits/binary-analysis-kit/kit.yaml",
        "examples/binary-analysis-kit/mcp_servers/binary-analysis-kit-helper.py",
        "examples/binary-analysis-kit/policies/workflow_rules/binary-analysis-kit-gate.yaml",
        "examples/binary-analysis-kit/skills/binary-analysis-kit-skill/SKILL.md",
        "examples/binary-analysis-kit/templates/teams/binary-analysis-kit-team.yaml",
        "examples/binary-analysis-kit/templates/workflows/binary-analysis-kit-template.yaml",
        "examples/binary-analysis-kit/workflows/binary-analysis-kit-workflow/workflow.yaml",
    ]
    remaining = [path for path in retired if (ROOT / path).exists()]
    if remaining:
        raise AssertionError(f"retired deployment files still exist: {remaining}")


def main() -> int:
    validate_env()
    validate_native_config()
    validate_workflows()
    validate_release_scripts()
    validate_retired_files()
    print("native deployment asset validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
