# 发布与校验

[English](./release.md) | [简体中文](./release.zh-CN.md)

GoFlow 的发布产物包括：

- Windows/Linux 二进制压缩包。
- HTTP/API 部署用 Docker 镜像。

## 本地构建 Release 产物

```bash
python scripts/build_release_assets.py --clean --version v0.1.3
```

默认目标：

- `linux/amd64`
- `linux/arm64`
- `windows/amd64`
- `windows/arm64`

构建单个平台：

```bash
python scripts/build_release_assets.py --clean --version dev --target linux/amd64
```

校验完整产物：

```bash
python scripts/generate_sbom.py --version v0.1.3 --output dist/SBOM.spdx.json --update-checksums
python scripts/validate_release_archives.py --dist dist --version v0.1.3 --require-sbom
```

## GitHub Release

推送 `v*` 标签会触发 `.github/workflows/release.yml`：

```bash
git tag v0.1.3
git push origin v0.1.3
```

Release workflow 会：

1. 构建二进制压缩包。
2. 生成 `SBOM.spdx.json`。
3. 签名压缩包、`SHA256SUMS` 和 SBOM。
4. 校验归档命名、校验和、SBOM 和签名。
5. 上传 GitHub Actions artifacts。
6. 附加文件到 GitHub Release。
7. 构建主 Docker 镜像和 MCP Python 工具运行时镜像。
8. 推送镜像到 GHCR。
9. 签名 Docker image digest。

这些脚本运行在 GitHub-hosted runner 上，不是在本地机器上运行。

## 压缩包内容

每个压缩包包含：

- `bin/goflow`
- `bin/file_tools`
- `bin/web_tools`
- `mcp_servers/python_notes.py`
- `configs/goflow.binary.yaml`
- `skills/`
- `docs/`
- 启动脚本：`run-goflow.sh` 或 `run-goflow.cmd`

启动脚本会设置 MCP 工具路径，并在备份 Provider 变量为空时复制主 Provider 变量。

## 签名验证

验证压缩包：

```bash
cosign verify-blob \
  --bundle goflow-agent_v0.1.3_linux_amd64.tar.gz.sigstore.json \
  --certificate-identity-regexp "https://github.com/FyMatt/GoFlow-Agent/.github/workflows/release.yml@refs/tags/v.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  goflow-agent_v0.1.3_linux_amd64.tar.gz
```

验证 Docker 镜像：

```bash
cosign verify \
  --certificate-identity-regexp "https://github.com/FyMatt/GoFlow-Agent/.github/workflows/release.yml@refs/tags/v.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/fymatt/goflow-agent:<tag>
```

## 发布镜像

```text
ghcr.io/fymatt/goflow-agent:<tag>
ghcr.io/fymatt/goflow-agent:latest
ghcr.io/fymatt/goflow-agent-mcp-python:<tag>
ghcr.io/fymatt/goflow-agent-mcp-python:latest
```

主镜像默认启动 HTTP 模式：

```text
goflow --config /app/configs/goflow.docker.yaml --workspace /workspace --http :8080
```

## 发布前检查

```bash
go test ./...
python scripts/validate_python_mcp.py
python scripts/validate_extension_workflow.py
python scripts/validate_deployment_assets.py
```
