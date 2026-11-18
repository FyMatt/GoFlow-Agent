# 部署

GoFlow V3 只以本地原生应用形式支持 Windows 10/11 x64。不提供 Linux、Docker、Podman、虚拟机和 arm64 产品路径。

## 运行目录

- `goflow.exe`：主程序。
- `configs/`：内置模型、智能体和 MCP 连接。
- `mcp_servers/`：原生内置工具。
- `skills/`：领域方法、脚本和模板。
- `workflows/`：可执行工作流。
- 用户选择的工作区：用户项目文件；默认源码运行还可能包含 `.goflow/session.json` 等会话数据。
- 数据目录：会话、附件、工作区版本快照、备份、索引和恢复信息。

数据目录应位于工作区之外。如果发生路径重叠，GoFlow 会把工作区恢复快照迁移到独立临时目录。

以当前配置的 `GOFLOW_DATA_ROOT` 和会话路径为准，不要把 `.goflow/`、`task-staging/`、密钥和版本数据当成缓存删除。安全清理与启停方法见[本机操作与维护](./local-operations.zh-CN.md)。

## Windows

从用户可写的安装目录启动发布包：

```powershell
.\goflow.exe --config .\configs\goflow.binary.yaml --workspace D:\Projects\example --http 127.0.0.1:8080
```

Windows 受控执行按宿主能力使用 Job Object、受限令牌、低完整性进程、任务级 ACL 租约和 AppContainer。能力探针失败时，未知代码会被拒绝，不会降级成不受控进程。

## 网络暴露

默认只监听 `127.0.0.1`。绑定其他网卡前，必须放在有认证的本地反向代理后面，并使用系统防火墙限制来源。浏览器 API 面向本机用户，不应匿名暴露到公网。

## 原生 MCP 执行

普通用户不配置底层隔离参数。连接默认使用 `isolation: auto`；可信内置工具按能力使用最小原生适配器。获批的未知和第三方 MCP 仅能在已确认工作区边界内运行，不能访问工作区外路径；写入由任务权限、任务前快照和版本回退共同保护。

## 校验

```bash
python scripts/validate_deployment_assets.py
python scripts/validate_release_archives.py --help
go test ./...
```

最终部署验收必须在 Windows 上完成原生安装、升级、恢复、沙箱、浏览器和复杂任务检查。
