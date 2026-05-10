# 更新策略

[English](./updates.md) | [简体中文](./updates.zh-CN.md)

GoFlow 在推送版本标签时通过 GitHub Actions 发布 release 压缩包和 Docker 镜像。运行时可通过 `GET /api/update-policy` 向 Studio 暴露更新策略。更新检查是显式触发的：只有用户点击检查按钮，或客户端调用 `POST /api/update-policy/check` 时，GoFlow 才会访问配置的发布源。

## 策略

更新检查应当可见并由用户控制，因为它需要访问 GitHub。

支持的安装类型：

- **Release 压缩包**：比较当前版本和最新 GitHub Release，展示 release notes，经用户同意后下载对应压缩包，校验 `SHA256SUMS` 和 Sigstore bundle，然后 staged replacement 并通过 launcher 重启。
- **Docker**：比较当前 tag 和最新 release，展示 `docker pull` 或 Compose 升级命令。容器默认不应替换自身。
- **源码 checkout**：展示 `git fetch`、tag checkout 和构建命令。除非用户明确审批，否则不要自动修改仓库。

## 版本来源

Release 构建会把版本注入二进制：

```text
-X github.com/FyMatt/GoFlow-Agent/internal/version.Version=<tag>
```

开发构建显示 `dev`。

## 当前基线

已实现：

- `GET /api/update-policy`
- `POST /api/update-policy/check` 显式检查最新 release
- Studio settings 页面可展示更新策略元数据、手动检查按钮、最新版本、发布页、说明预览和资源摘要
- 更新检查响应包含 `asset_summary`，用于识别当前平台安装包、`SHA256SUMS`、`SBOM.spdx.json`、Sigstore 签名包和 `verification_ready` 状态
- `asset_summary.verify_steps` 会给出当前平台可复制的手动校验命令；GoFlow 不会自动执行这些命令，也不会自动替换文件
- `GOFLOW_DISABLE_UPDATE_CHECKS=1` / `true` / `yes` / `on` 会隐藏 Studio 检查按钮，并让 `POST /api/update-policy/check` 返回 `403`
- release 构建脚本注入版本号

可选后续：

- checksum/signature 校验流程
- Windows/Linux staged archive replacement
- Docker/source 安装升级助手

## 发布源覆盖

默认发布源：

```text
https://api.github.com/repos/FyMatt/GoFlow-Agent/releases/latest
```

内部部署或本地测试可以设置 `GOFLOW_RELEASE_FEED`，指向兼容 GitHub release JSON 的私有端点。检查接口会读取 `tag_name`、`html_url`、`body` 和 `assets` 等标准字段。

`asset_summary.expected_archive` 会根据当前运行平台和最新 release tag 推导，例如
Windows 上是 `goflow-agent_v0.1.3_windows_amd64.zip`。只有匹配平台安装包、
`SHA256SUMS`、`SBOM.spdx.json` 和预期 Sigstore 签名包都存在时，
`verification_ready` 才为 true。
`asset_summary.verify_steps` 会提供面向操作者的手动校验命令。这些命令只用于提示：
更新检查不会下载、执行或替换 release 文件。

## 离线部署

离线或强管控环境可以设置：

```text
GOFLOW_DISABLE_UPDATE_CHECKS=1
```

设置页仍会解释更新策略，但会隐藏联网检查按钮；检查接口会返回 `403` 和明确原因。
