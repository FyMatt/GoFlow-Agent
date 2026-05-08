# Skill

[English](./skills.md) | [简体中文](./skills.zh-CN.md)

## 概览

Skill 存放在 `skills/<name>/SKILL.md`。

一个 Skill 包含两部分：

1. YAML frontmatter：结构化元数据。
2. Markdown instructions：运行时指导。

## 运行时行为

Skill 从 runtime home 加载，而不是从外部 workspace 加载。

```bash
go run ./cmd/goflow --workspace D:/empty-project
```

此时 GoFlow 仍然从自身 `skills/` 目录加载 Skill，但文件工具会操作 `D:/empty-project`。

## 元数据字段

常见字段：

- `name`
- `description`
- `version`
- `author`
- `format`
- `tools`
- `allowed-tools` / `allowed_tools`
- `params`
- `scripts`
- `activation`
- `mode`
- `preferred_agent`
- `allowed_tool_kinds`
- `output_kind`
- `next_skills`
- `metadata`

GoFlow 同时兼容原生 GoFlow Skill 和 Claude/Codex 风格 Skill。便携 Skill 至少需要 `name` 和 `description`，GoFlow 会在缺少 `activation.keywords` 时自动派生关键词。

## 示例

```markdown
---
name: code-audit
description: Review source code for security risks.
version: 1.0.0
author: GoFlow
tools:
  - name: file_tools/read_file
    required: true
activation:
  keywords: ["audit", "审计", "security review"]
mode: audit
preferred_agent: auditor
allowed_tool_kinds: [read, unknown]
output_kind: findings
next_skills: [code-writing]
metadata:
  domain: security
---

## Workflow

1. Inspect the target.
2. Identify risk boundaries.
3. Produce a structured report.
```

## 资源文件

复杂 Skill 可以包含：

- `references/`
- `scripts/`
- `assets/`
- `templates/`
- `agents/`

HTTP Studio 可通过 Skill resource API 管理这些文件，而不是直接改写整个 `SKILL.md`。

## 声明式辅助脚本

复杂 Skill 可以在 `SKILL.md` 中声明确定性的辅助脚本：

```yaml
scripts:
  - name: collect-assets
    path: scripts/collect_assets.py
    runtime: python
    output: json
    timeout: 30s
    workspace_mount: ro
    approval: required
    args_schema:
      type: object
      additionalProperties: false
      properties:
        url:
          type: string
      required: [url]
```

脚本路径必须位于该 Skill 的 `scripts/` 目录内。Skill 匹配后，GoFlow 可以把
`skill_runner/run_script` 暴露为一个普通 `exec` 工具；是否可用、是否需要审批、
如何隔离，仍由当前 Agent profile 和 runtime policy 决定。

脚本会从 stdin 和 `GOFLOW_SKILL_ARGS_JSON` 接收 JSON 参数。工具返回 JSON
对象，包含 stdout、退出码、耗时、是否超时、声明的元数据，以及声明
`output: json` 时的 JSON 输出校验状态。

如果需要真正的安全隔离，应在 `skill_runner` MCP server 配置中使用
`isolation: container`，让脚本在 Docker/Podman 沙箱中执行。Skill 脚本元数据用于表达意图，真正的执行边界来自 MCP server 配置。

## 匹配

当前 matcher 主要基于关键词。后续可以增加 embedding、语义路由或更强的 Skill 编排能力。
