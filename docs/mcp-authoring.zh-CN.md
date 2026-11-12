# MCP 开发

MCP Server 使用标准 stdio 进程。GoFlow 兼容 Codex/Claude 的连接文档和标准 MCP 工具 Schema。

## 推荐的 Server 契约

- 工具名称稳定，并按连接命名空间组织；
- 使用精确 JSON Schema，并设置 `additionalProperties: false`；
- 将工具分类为只读、写入、执行、网络或未知；
- 校验所有工作区路径，拒绝路径穿越和符号链接逃逸；
- 返回有大小上限的 UTF-8 结果，并设置明确错误标志；
- 生成代码可复现、可审查；
- API Key 和私钥不得写入代码、参数、日志或产物。

## 原生执行

连接使用 `isolation: auto`。GoFlow 根据工具能力和宿主探针选择最小原生边界。未知或第三方 Server 只能在任务副本中运行；所需边界不可用时直接拒绝。

## 内置脚手架

`GET /api/resources/tools/scaffolds` 提供本地 Python 和原生辅助工具模板。脚手架包含源码、配置、Schema、路径检查、有界输出和启用说明，不要求普通用户填写底层隔离参数。

内置 `network_tools` 连接是授权网络操作的参考实现：所有变更动作都需要明确审批，并输出可回放、可复核和可回退的结构化证据。

## 兼容性

可以导入 Codex 或 Claude Code 的标准 MCP stdio 定义，然后查看中文能力摘要并启用连接。导出定义保持可移植且不包含密钥。
