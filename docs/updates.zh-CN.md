# 更新策略

[English](./updates.md) | [简体中文](./updates.zh-CN.md)

GoFlow 在推送版本标签时通过 GitHub Actions 发布 release 压缩包和 Docker 镜像。运行时可通过 `GET /api/update-policy` 向 Studio 暴露更新策略。

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
- Studio settings 页面可展示更新策略元数据
- release 构建脚本注入版本号

可选后续：

- opt-in GitHub Releases 检查端点
- checksum/signature 校验流程
- Windows/Linux staged archive replacement
- Docker/source 安装升级助手
- 离线部署禁用更新检查配置
