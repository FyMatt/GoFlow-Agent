# 端到端扩展示例

[English](./README.md) | [简体中文](./README.zh-CN.md)

这个示例展示如何组合自定义 Python MCP server、自定义 Agent、自定义 Skill 和图工作流。

它默认不启用。需要时把需要的部分复制到 runtime home，检查权限，然后重新加载发现结果。

## 文件

```text
examples/extension-workflow/
  mcp_servers/workspace_report.py
  configs/agents/workspace-reporter.yaml
  configs/mcp_servers/workspace_report.yaml
  skills/workspace-report/SKILL.md
  workflows/workspace-review/workflow.yaml
```

## 安装到 runtime home

在 GoFlow 仓库根目录执行：

```powershell
Copy-Item examples/extension-workflow/mcp_servers/workspace_report.py mcp_servers/workspace_report.py
Copy-Item -Recurse examples/extension-workflow/skills/workspace-report skills/workspace-report
Copy-Item -Recurse examples/extension-workflow/workflows/workspace-review workflows/workspace-review
Copy-Item examples/extension-workflow/configs/agents/workspace-reporter.yaml configs/agents/workspace-reporter.yaml
Copy-Item examples/extension-workflow/configs/mcp_servers/workspace_report.yaml configs/mcp_servers/workspace_report.yaml
```

复制后检查 Agent 和 MCP server 配置，再重启或 reload。

## 验证发现

复制前可以运行 smoke test：

```bash
python scripts/validate_extension_workflow.py
```

复制后在 CLI 中执行：

```text
/reload
/reload-tools
/skills
/tools
/agents
```

预期名称：

- skill：`workspace-report`
- tool：`workspace_report/workspace_summary`
- agent：`workspace-reporter`
- workflow：`workspace-review`

## 运行工作流

```text
/workflow workspace-review summarize this repository structure and identify risky large files
```

工作流包含两个阶段：

1. `inventory`：使用 `workspace-reporter`、`workspace-report` 和自定义只读 MCP tool。
2. `audit`：使用内置 `auditor` 和 `code-audit` 审计 inventory 输出。

## 为什么这样设计

- MCP server 只负责一个窄能力。
- Agent 只拥有该能力需要的读权限。
- Skill 定义输出契约。
- Workflow 把自定义阶段和内置审计阶段组合起来。
