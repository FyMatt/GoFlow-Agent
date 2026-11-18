# 安装

GoFlow 只提供 Windows 10/11 x64 原生发布包。

## 安装步骤

1. 下载 Windows amd64 ZIP 压缩包。
2. 使用 `SHA256SUMS` 和发布签名验证文件。
3. 解压到用户可写的应用目录。
4. 启动 GoFlow，并在浏览器中选择工作区。

Windows：

```powershell
.\run-goflow.cmd
```

运行时提示 HTTP 服务就绪后，打开 `http://127.0.0.1:8080`。

## 首次使用

- 设置页只填写模型服务 API Key。
- 文件、工具和工作流任务开始前选择并确认工作区。
- 启用未知第三方连接前查看原生沙箱能力报告。

高级本机能力使用明确的 `GOFLOW_HOST_MANAGED_CONFIG_ROOTS`、`GOFLOW_HOST_CHANGE_BACKUP_ROOT` 和 `GOFLOW_SSH_TOOLS_CMD` 契约；普通用户不需要配置这些字段。

## 升级

先创建数据备份并验证新发布包，再停止旧进程、替换应用文件并重启。用户数据保留在配置的数据目录中；迁移失败时必须恢复升级前备份。

使用发布包内置的原生安装管理器，不需要 Python 或其他开发环境：

先把新发布包解压到安装目录之外，再从该解压目录运行以下安装器。Windows 不要从正要升级、回滚或卸载的安装目录内部运行 `goflow-install.exe`，否则正在执行的文件会阻止目录原子切换。

```powershell
.\bin\goflow-install.exe install --archive goflow-agent_VERSION_windows_amd64.zip --prefix "$env:LOCALAPPDATA\Programs\GoFlow" --data-root "$env:LOCALAPPDATA\GoFlow\data"
.\bin\goflow-install.exe upgrade --archive goflow-agent_VERSION_windows_amd64.zip --prefix "$env:LOCALAPPDATA\Programs\GoFlow" --data-root "$env:LOCALAPPDATA\GoFlow\data"
.\bin\goflow-install.exe rollback --prefix "$env:LOCALAPPDATA\Programs\GoFlow"
```

升级会替换应用文件并保留数据目录；应用目录旁会留下可回滚的升级备份。`rollback` 默认恢复该安装目录最近一次有效备份，并保持数据目录不变。

## 卸载

停止 GoFlow 后删除应用目录。只有在确认不再需要会话、资源、备份和恢复记录时，才删除数据目录。

```powershell
.\bin\goflow-install.exe uninstall --prefix "$env:LOCALAPPDATA\Programs\GoFlow"
.\bin\goflow-install.exe uninstall --prefix "$env:LOCALAPPDATA\Programs\GoFlow" --purge-data
```
