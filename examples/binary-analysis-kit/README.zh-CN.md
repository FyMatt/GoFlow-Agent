# 二进制分析 Kit 示例

[English](./README.md) | [简体中文](./README.zh-CN.md)

这个示例是一个可复制的二进制分析 materialized kit，用来展示一个领域包如何把 Agent、Skill、Docker/Podman 隔离的 MCP helper、Workflow、Workflow Template、Team Template 和 Policy Rule 串联起来。

它默认不会启用。你需要只复制自己需要的部分到 runtime home，检查权限，设置工具脚本路径，然后重启或 reload GoFlow。

## 文件结构

```text
examples/binary-analysis-kit/
  configs/agents/binary-analysis-kit-agent.yaml
  configs/mcp_servers/binary-analysis-kit-helper.yaml
  kits/binary-analysis-kit/kit.yaml
  mcp_servers/binary-analysis-kit-helper.py
  policies/workflow_rules/binary-analysis-kit-gate.yaml
  skills/binary-analysis-kit-skill/SKILL.md
  templates/teams/binary-analysis-kit-team.yaml
  templates/workflows/binary-analysis-kit-template.yaml
  workflows/binary-analysis-kit-workflow/workflow.yaml
```

## 安装到 runtime home

在 GoFlow 仓库根目录执行：

```powershell
Copy-Item examples/binary-analysis-kit/mcp_servers/binary-analysis-kit-helper.py mcp_servers/binary-analysis-kit-helper.py
Copy-Item -Recurse examples/binary-analysis-kit/skills/binary-analysis-kit-skill skills/binary-analysis-kit-skill
Copy-Item -Recurse examples/binary-analysis-kit/workflows/binary-analysis-kit-workflow workflows/binary-analysis-kit-workflow
Copy-Item -Recurse examples/binary-analysis-kit/kits/binary-analysis-kit kits/binary-analysis-kit
Copy-Item examples/binary-analysis-kit/configs/agents/binary-analysis-kit-agent.yaml configs/agents/binary-analysis-kit-agent.yaml
Copy-Item examples/binary-analysis-kit/configs/mcp_servers/binary-analysis-kit-helper.yaml configs/mcp_servers/binary-analysis-kit-helper.yaml
Copy-Item examples/binary-analysis-kit/policies/workflow_rules/binary-analysis-kit-gate.yaml policies/workflow_rules/binary-analysis-kit-gate.yaml
Copy-Item examples/binary-analysis-kit/templates/teams/binary-analysis-kit-team.yaml templates/teams/binary-analysis-kit-team.yaml
Copy-Item examples/binary-analysis-kit/templates/workflows/binary-analysis-kit-template.yaml templates/workflows/binary-analysis-kit-template.yaml
```

MCP 配置使用容器单文件挂载。启动前要设置 helper 的绝对路径：

```powershell
setx GOFLOW_BINARY_ANALYSIS_KIT_TOOL_SOURCE "C:\path\to\goflow\mcp_servers\binary-analysis-kit-helper.py"
```

Linux/macOS shell 示例：

```bash
export GOFLOW_BINARY_ANALYSIS_KIT_TOOL_SOURCE=/opt/goflow/mcp_servers/binary-analysis-kit-helper.py
```

helper 默认使用 `isolation: container`、只读 workspace、禁用网络、非 root 用户、资源限制、只读根文件系统、丢弃 capabilities 和 tmpfs 临时目录。除非工具确实需要更多权限，否则不要放宽这个边界。

## 验证发现

重启 GoFlow，或在 CLI 中 reload 可热加载的资源：

```text
/reload
/reload-tools
/agents
/skills
/tools
/workflow-templates
/teams
/kits
```

预期名称：

- agent: `binary-analysis-kit-agent`
- skill: `binary-analysis-kit-skill`
- tools:
  - `binary-analysis-kit-helper/binary_file_info`
  - `binary-analysis-kit-helper/binary_strings`
  - `binary-analysis-kit-helper/hex_preview`
  - `binary-analysis-kit-helper/read_text`
- workflow: `binary-analysis-kit-workflow`
- workflow template: `binary-analysis-kit-template`
- team template: `binary-analysis-kit-team`
- policy rule: `binary-analysis-kit-gate`
- kit: `binary-analysis-kit`

## 运行工作流

```text
/workflow binary-analysis-kit-workflow inspect sample.bin and report likely attack surfaces
```

这里应当把二进制路径当作普通文本参数传入，不要写成 `@sample.bin`。`@file`
引用只适用于 UTF-8 文本内容，原始二进制应交给二进制分析工具读取。

这个示例工作流会在节点之间传递输出：

1. `team` 提供可复用的协作上下文。
2. `plan` 使用 `binary-analysis-kit-skill`，调用二进制 helper 工具，并输出 `summary`、`plan` 和 `findings`。
3. `gate` 使用 `binary-analysis-kit-gate` 检查计划摘要是否可用。
4. `report` 在 gate 通过时生成最终交接报告。
5. `revise` 在 gate 拒绝时生成修订路径。

如果只需要内置轻量路径，可以使用根目录 `kits/` 下的 `binary-analysis-kit`，配合内置 `binary-triage` 模板和 `binary-vulnerability-research` Skill。当前示例是更完整的 materialized 版本，适合学习资源之间如何联动。

可在仓库根目录验证这个打包示例：

```bash
python scripts/validate_binary_analysis_kit.py
```
