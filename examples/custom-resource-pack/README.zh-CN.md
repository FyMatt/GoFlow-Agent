# 不改主程序的自定义领域资源包

该示例添加 `field-notes` 领域：Agent、Skill、Workflow 和 Kit 都由文件定义。核心不需要增加领域枚举、关键词分支或产物补写逻辑；已有文件 MCP 足够完成任务。

## 安装

将本目录 `configs/agents/`、`skills/`、`workflows/`、`kits/` 中的相应子文件/子目录复制到自己的 GoFlow runtime home 对应目录。示例使用现有 `primary` Provider 和 `file_tools` 连接；不要复制真实 API Key。通过资源页面安装/编辑时按页面提示加载；文件复制后的首次验收可重启服务。

启用 `kits/field-notes-kit/kit.yaml` 中的 routing 合约后，新任务的自动模式可匹配“田野笔记整理”或“field notes review”。显式选择其他智能体/工作流不应被此包覆盖。依赖缺失应在预检报告，不能静默转成软件开发任务。

## 使用

在测试工作区准备自己的 `input/notes.txt`，例如：

```text
地点：样地 A
记录：两次观察到同一标记；第二次测量值缺少单位。
日期未提供。
```

任务：`田野笔记整理：读取 input/notes.txt，生成 deliverables/notes.json，逐项保留证据引用，缺失的日期与单位标为未知，不修改源文件。`

验收：源文件哈希不变；报告实际存在、JSON 可解析；未发明日期/单位；时间线包含成功读取、写入和读回；工作流门禁与实际工具状态一致。重复尝试不得绕过权限，也不能把模型自评视为独立内容质量证明。

本地确定性模拟 Provider 只能证明资源扩展机制，真实模型的内容质量仍需人工验收。新 MCP 执行能力需要独立适配器，不能仅靠修改 YAML 获得。
