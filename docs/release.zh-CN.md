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

校验单个平台的本地产物：

```bash
python scripts/build_release_assets.py --clean --version dev --target linux/amd64
python scripts/validate_release_archives.py --dist dist --version dev --target linux/amd64
```

## GitHub Release

推送 `v*` 标签会触发 `.github/workflows/release.yml`：

```bash
git tag v0.1.3
git push origin v0.1.3
```

Release workflow 会：

1. 先运行 release preflight：`go test ./...`、Python MCP 校验、资源引用校验、
   Web Studio i18n/docs 校验、HTTP Studio 冒烟测试、强制真实浏览器 Studio 冒烟测试，
   以及部署资源校验。
2. 构建二进制压缩包。
3. 生成 `SBOM.spdx.json`。
4. 签名压缩包、`SHA256SUMS` 和 SBOM。
5. 校验归档命名、校验和、SBOM 和签名。
6. 上传 GitHub Actions artifacts。
7. 附加文件到 GitHub Release。
8. 构建主 Docker 镜像和 MCP Python 工具运行时镜像。
9. 推送镜像到 GHCR。
10. 签名 Docker image digest。

压缩包和 Docker 镜像 job 都依赖 preflight。浏览器烟测、资源校验、文档校验或部署
校验失败时，不会继续发布。

这些脚本运行在 GitHub-hosted runner 上，不是在本地机器上运行。

## 压缩包内容

每个压缩包包含：

- `bin/goflow`
- `bin/file_tools`
- `bin/web_tools`
- `mcp_servers/python_notes.py`
- `configs/goflow.binary.yaml`
- `skills/`
- `kits/`
- `examples/`
- `docs/`
- 启动脚本：`run-goflow.sh` 或 `run-goflow.cmd`

启动脚本默认启动 HTTP/Web Studio，地址为 `http://127.0.0.1:8080/console`，并自动设置内置 MCP 工具路径。需要交互式 CLI 时，直接运行 `bin/goflow` 或 `bin/goflow.exe`。

启动脚本会设置 MCP 工具路径，并在备份 Provider 变量为空时复制主 Provider 变量。

`kits/` 和 `examples/` 会随压缩包一起发布，确保下载包内也包含 README 和
Web Studio 中提到的起步 Kit 目录和可复制二开示例。
`scripts/validate_release_archives.py` 会校验产物命名、`SHA256SUMS`、可选
SBOM、可选签名 bundle，以及压缩包内部布局，包括代表性的 Kit 和示例文件，
避免发布包缺少启动脚本、配置、文档或二进制文件。

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

打 tag 前先做密钥检查。提交到仓库的 Provider 文件应保留 `${GOFLOW_API_KEY}` 这类
环境变量引用，不要写入真实 API Key：

```bash
python scripts/validate_no_secrets.py
```

如需额外手动扫描，也可以运行：

```bash
rg --hidden --glob '!.git/**' --glob '!*.png' --glob '!*.jpg' --glob '!*.jpeg' --glob '!*.gif' --glob '!*.webp' --glob '!*.exe' --glob '!*.dll' --glob '!*.zip' --glob '!*.gz' --glob '!*.bin' 'sk-[A-Za-z0-9_-]{20,}' .
```

如果任一命令命中了真实密钥，先从文件中移除，再到模型服务商后台轮换或吊销该密钥，然后重新
扫描。Web Studio 会把 Provider 资源保存到 `configs/providers/<name>.yaml`，因此
页面里粘贴的密钥属于本地明文配置，不应进入发布提交。

```bash
python scripts/run_preflight.py --browser-required
```

如果希望同一条预检命令同时构建并校验本地发布压缩包，可以增加一个或多个发布目标：

```bash
python scripts/run_preflight.py --browser-required --release-target windows/amd64
```

如果本地没有 Chrome、Edge 或 Chromium，可以使用
`python scripts/run_preflight.py --skip-browser` 跑非浏览器检查。

`scripts/smoke_http_studio.py` 会临时构建 `goflow`，用占位模型配置启动 HTTP 模式，并检查嵌入式 Studio 页面、静态资源和 API 契约。它是轻量级 HTTP/静态资源/API 冒烟测试，不等同于完整浏览器交互测试。

`scripts/smoke_http_browser.py` 是可选的真实浏览器执行冒烟测试。它会使用本机
Chrome、Edge 或 Chromium 渲染 `/console`、`/workflows`、
`/console#playground`、`/console#approvals`、`/console#catalog`、
`/console#status`、`/console#workspace`、`/console#settings`，以及中文
`?lang=zh` 深链，用于发现概览页、工作流编排、任务运行、审批、资源页、观测页、
工作区页和设置页的 JavaScript 白屏、递归爆栈和中英文混排回归。默认没有浏览器时
跳过；CI 和发布前检查应加 `--required`，这样缺少浏览器或浏览器无法执行时会直接
失败，而不是静默跳过 Web Studio 执行覆盖。
