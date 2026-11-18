# 发布

GoFlow 只发布 Windows amd64 原生压缩包。Linux 兼容代码和历史测试可以保留，但 Linux 发布包与 Ubuntu 验收不再属于产品发布目标。

## 必需产物

- Windows amd64 压缩包。
- `SHA256SUMS`。
- SBOM。
- 依赖许可证报告。
- 压缩包、校验和、SBOM 及依赖许可证报告的签名文件。
- 数据迁移、能力、限制和恢复说明。

## 发布硬门

1. `go test ./...` 全部通过。
2. JavaScript 和 Python 语法检查通过。
3. 敏感信息、部署资源、资源链接、翻译和发布包校验通过。
4. Windows amd64 原生构建通过。
5. 原生安装、升级、卸载、崩溃恢复和回退通过。
6. 六类复杂任务连续三次通过，产物可直接使用。
7. 桌面/移动端、浅色/深色浏览器截图审查通过。
8. Windows 原生沙箱恶意夹具通过，否则阻止发布。
9. 真实视觉 Provider 完成截图识别、代码定位、修复、隐藏测试和交付写回闭环。
10. 不存在未处理的高危依赖漏洞。

## 构建

```bash
python scripts/build_release_assets.py --target windows/amd64
python scripts/generate_sbom.py --version dev --update-checksums
python scripts/validate_release_archives.py --version dev --require-sbom --require-license-report
```

正式 Release 还会使用 GitHub OIDC 身份逐个签名归档、SBOM、依赖许可证报告与 `SHA256SUMS`，签名后立即执行 `cosign verify-blob`。本地 `dev` 构建没有可信发布身份，不生成或伪造签名。

发布工作流不得生成容器镜像或不支持架构的产物。

Windows x64 发布包生成后执行：

```powershell
python scripts/windows_native_acceptance.py
```

该命令直接消费最终 ZIP 与 `SHA256SUMS`，验收安装、无 Key 首启、无效升级保护、升级备份、重启、回滚、回滚后启动、再次升级、卸载保留数据和显式清理数据，并在 `.artifacts/windows-native-acceptance/` 保存逐项证据。
验收器从最终 ZIP 的外部解压目录运行安装器，与普通用户升级时的受支持路径一致；不得从正在被替换的 Windows 安装目录内部执行安装器。

真实视觉 Provider 上架前必须执行：

```bash
python scripts/real_vision_acceptance.py --check-inputs
python scripts/real_vision_acceptance.py --probe-provider
python scripts/validate_external_acceptance.py vision-probe <probe-report.json>
python scripts/real_vision_acceptance.py --require-external-inputs
```

第一条命令只检查仓库外文件、只读权限、非空密钥和 Provider 模块契约，不构建、不启动服务，也不发起网络请求。模块必须包含唯一的 `primary`，以及完整的 `provider`、绝对 HTTP(S) `base_url`、`model`、`vision: true` 和 `api_key: secret://providers/primary/api_key`；当前正式支持 `openai-compatible` 与 `anthropic`。第二条命令构建并启动隔离服务，只运行真实 Provider 能力探针，要求连接、视觉和工具调用全部通过；报告仍执行密钥扫描和临时状态清理。第三条命令使用纯标准库生成确定性 PNG，任务提示不包含图片中的文件名、函数名、端口边界或回退规则。只有 Provider 真实识图并完成代码修改、真实 Diff、工具证据、质量门、公开写回、隐藏测试、`go vet`、格式检查和持久化检查才通过。正式自托管 runner 使用仓库外非符号链接只读文件，并通过仓库变量 `GOFLOW_VISION_SECRET_FILE` 只传文件路径；POSIX 主机上的 Key 还必须禁止组和其他用户访问。脚本把密钥临时物化到独立数据根的 `secret://` 路径，结束后删除。支持视觉的仓库外 `primary` Provider 模块路径由 `GOFLOW_VISION_PROVIDER_MODULE` 提供，脚本只把它复制到本次隔离数据根的用户资源层。Key 不能通过命令参数、工作流输入、环境变量值或日志传递。

正式发布额外要求 Windows 自托管视觉 runner，标签为 `windows`、`x64`、`goflow-vision-acceptance`，并使用机器本地预配置密钥。runner 缺失、能力不可用或报告失败都会阻止发布，不得改用 Ubuntu runner 或跳过作业。
