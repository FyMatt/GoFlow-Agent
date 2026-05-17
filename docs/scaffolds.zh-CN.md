# 脚手架命令

[English](./scaffolds.md) | [简体中文](./scaffolds.zh-CN.md)

GoFlow 提供一组保守的生成器，用于常见二开任务。生成文件是可编辑示例，默认保持权限边界清晰。

## 命令

```text
/skill-templates
/new-skill <template> <name>
/new-tool python <name>
/new-agent <name>
/new-provider <name>
/new-workflow <name> [--template <template>]
/new-workflow-template <source-template> <name>
/new-policy-rule <preset> <name>
/new-team <preset> <name>
/new-kit <preset> <name> [--materialize|--full]
```

生成路径相对 runtime home：

- `skills/<name>/SKILL.md`
- `mcp_servers/<name>.py`
- `configs/mcp_servers/<name>.yaml`
- `configs/agents/<name>.yaml`
- `configs/providers/<name>.yaml`
- `workflows/<name>/workflow.yaml`
- `templates/workflows/<name>.yaml`
- `policies/workflow_rules/<name>.yaml`
- `templates/teams/<name>.yaml`
- `kits/<name>/kit.yaml`

active workspace 仍然只用于文件工具和 `@file` 引用。

## Skill

```text
/skill-templates
/new-skill code-audit dependency-audit
```

生成的 `SKILL.md` 包含 metadata、工具声明、激活关键词、推荐 Agent、输出类型和扩展说明。

Skill scaffold template 已经文件化。内置模板从 `internal/scaffold/templates/skills/scaffolds/templates.yaml` 内嵌进二进制；运行目录可以通过 `templates/skills/scaffolds/*.yaml` 添加或覆盖模板。`/skill-templates` 和 `/new-skill` 使用同一套加载逻辑，所以自定义模板会先出现在列表中，再用于生成新的 Skill。

## MCP Tool

```text
/new-tool python workspace-helper
```

生成 Python MCP server 和对应配置。保存后运行：

```text
/reload-tools
/tools
```

生成的 Python server 与模块化 MCP 配置已经文件化。内置模板从以下路径内嵌进二进制：

- `internal/scaffold/templates/tools/python/server.py.tmpl`
- `internal/scaffold/templates/tools/python/config.yaml.tmpl`

运行目录可以通过以下路径覆盖：

- `templates/tools/python/server.py.tmpl`
- `templates/tools/python/config.yaml.tmpl`

`/new-tool python` 和 HTTP Studio 默认 Python Tool 资源都使用同一套渲染器。模板是 Go
`text/template`，可使用：

- `.Name`：规范化后的资源名，例如 `workspace-helper`
- `.ServerName`：由名称生成的 MCP server id，例如 `workspace_helper`

### HTTP Tool Preset

Web Studio 可以通过资源 API 创建工具脚手架：

```text
GET /api/resources/tools/scaffolds
GET /api/resources/tools/scaffolds/{preset}?name=workspace-helper
POST /api/resources/tools/scaffolds/{preset}
```

当前后端提供这些常用 preset：

- `python-local`：本地 Python MCP starter，使用 `process_group` 作为生命周期隔离。
- `python-container-readonly`：Docker/Podman 容器化只读工具，workspace 只读、网络关闭、默认资源限制、只读根文件系统、非 root 用户、隔离 IPC、tmpfs scratch 和 init。
- `python-container-writer`：Docker/Podman 容器化写入工具，workspace 可写、网络关闭，并保留资源限制和容器硬化默认值。
- `python-container-network`：Docker/Podman 容器化网络工具，workspace 只读、显式允许网络访问，并保留容器硬化默认值。
- `python-container-production`：生产环境只读容器工具，使用 `isolation_profile: production`、`pull_policy: never`，要求提前在部署主机预拉取可信的 digest-pinned 镜像。

Tool scaffold preset 已经文件化。内置 preset 从 `internal/scaffold/templates/tools/scaffolds/presets.yaml` 内嵌进二进制；运行目录可以通过 `templates/tools/scaffolds/*.yaml` 添加或覆盖 preset。容器 preset 可以不写 `default_image` 和 `default_isolation_options`，后端会根据当前 GoFlow 版本和 `isolation_profile` 自动补齐后再返回给 Studio。

容器 preset 会同时生成 `mcp_servers/<name>.py` 和 `configs/mcp_servers/<name>.yaml`，并把生成的工具文件以只读方式挂载进容器。发行版默认使用匹配版本的 `ghcr.io/fymatt/goflow-agent-mcp-python:<version>` 镜像，开发构建会退回 `latest`。生产 preset 应把默认 tag 替换为不可变的 `image@sha256:...` 引用。

运行状态会在 `/api/runtime.mcp_servers[]` 暴露 `container_image_reference_type`、`container_image_digest_pinned`、`container_image_production_ready` 和 `container_pull_policy`；配置诊断会给出对应的字段级 warning，前端可以直接按这些字段展示生产就绪状态。

## Agent 与 Provider

```text
/new-agent domain-reviewer
/new-provider local-llm
```

Agent 会写入 `configs/agents/`，Provider 会写入 `configs/providers/`。这种模块化结构避免 `configs/goflow.yaml` 变得过大。

Provider 里的 `base_url`、`api_key` 和 `model` 可以在首次启动时暂时留空，GoFlow 仍会启动并让 Web Studio 展示配置诊断。真正运行使用该 Provider 的 Agent 前需要补全这些字段；如果运行时已经加载过旧 Provider 配置，补全后需要重启 GoFlow。

如果后续在 Web Studio 里保存 Provider，并直接填写字面量 API Key，这个密钥会以明文
写入生成的 YAML 文件。准备提交或共享脚手架文件时，建议保留 `${DEEPSEEK_API_KEY}`
这类环境变量引用。

Agent 默认脚手架已经文件化。内置模板位于
`internal/scaffold/templates/config/agents/default.yaml.tmpl`，运行目录可以用
`templates/config/agents/default.yaml.tmpl` 覆盖。模板是 Go
`text/template`，可使用：

- `.Name`：规范化后的资源名，例如 `domain-reviewer`
- `.Title`：由名称生成的显示标题

Provider 默认脚手架也已经文件化。内置模板位于
`internal/scaffold/templates/config/providers/default.yaml.tmpl`，运行目录可以用
`templates/config/providers/default.yaml.tmpl` 覆盖。模板可使用：

- `.Name`：规范化后的 provider id
- `.EnvAPIKey`：自动生成的环境变量名，例如 `DEEPSEEK_API_KEY`

建议默认 Agent 保持窄权限。纯聊天或阅读型角色优先使用
`allowed_tool_kinds: [read]`；只有专门的执行型角色才加入 `network`、`write`
或 `exec`，并配合审批与沙箱策略。

## Workflow

```text
/new-workflow release-check
/workflow release-check review this change
```

工作流保存在 `workflows/<name>/workflow.yaml`，可以由 CLI 或 Web Studio 编辑和运行。

## Policy Rule

```text
/new-policy-rule risk-threshold high-risk-gate
```

Policy Rule 用于让 Workflow 的 `policy_guard` 节点复用命名门禁规则，而不是在每个工作流里重复写条件。生成文件位于 `policies/workflow_rules/<name>.yaml`，使用版本化 `goflow.workflow_policy_rule` envelope。

当前内置 preset：

- `risk-threshold`：按风险或严重等级门禁，例如 `high`、`critical`。
- `truthy-reference`：引用的上游输出存在且为真时通过。
- `contains-text`：引用的输出包含指定文本时通过。
- `minimum-count`：列表或计数达到阈值时通过。
- `team-review-quorum`：基于 Team 审批或拒绝 quorum 做门禁。
- `expression`：从自定义表达式开始扩展。

Policy rule scaffold preset 已经文件化。内置 preset 从 `internal/scaffold/templates/policies/scaffolds/presets.yaml` 内嵌进二进制；运行目录可以通过 `templates/policies/scaffolds/*.yaml` 添加或覆盖 preset。CLI 的 `/new-policy-rule` 和 HTTP 的 `GET/POST /api/resources/policy-rules/scaffolds` 使用同一套加载逻辑，所以 Web Studio 和 CLI 看到的 preset 目录一致。

典型用法：

```yaml
- name: gate
  node_type: policy_guard
  params:
    rule: high-risk-gate
    ref: stages.audit.outputs.risk
    minimum: high
  next:
    - fix
  deny_next:
    - end
```

## Team 与 Kit

Team 模板用于多 Agent 协作组合，Kit 用于打包领域资源。它们应当像 Workflow 一样模块化存储，便于用户复制、编辑和二开。
### 完整关联 Kit

```text
/new-kit multi-domain-agent goflow-starter --materialize
/new-kit software-engineering acme-platform --materialize
/new-kit agent-framework custom-agent-platform --materialize
```

`--materialize` / `--full` 会一次生成互相关联的 Kit、Agent、Skill、Docker/Podman 容器化 MCP Tool、Workflow、Workflow Template、Team Template 和 Policy Rule。生成的 Workflow 会把前一个节点的输出传给后一个节点：Team 上下文进入 Plan 节点，Plan 输出进入 Policy Gate，通过后进入 Report，未通过进入 Revise。

如果需要最适合新手的通用入口，优先使用 `multi-domain-agent`。它会创建或引用一套互相关联的多领域 starter：软件研发、Web 安全、安全研究、二进制分析、文档、运维、客服和框架二开请求都会被路由到对应的 Workflow Template 与 Team Template。这个 preset 的目标是让用户直观看到 Agent、Skill、Tool、Team、Workflow Template、Policy Rule 和 Kit 如何联动，而不是只看到彼此孤立的样例。

`agent-framework` preset 专门用于二开 GoFlow 本身。它会引用 `agent-framework-extension` 工作流模板和 `framework-extension-team` 团队模板，用来设计、审查并生成新的垂直 Agent、Skill、Tool、Workflow、Team、Policy 或 Kit。

Workflow Template 已经从 Go 代码硬编码迁移为文件化资源。发布二进制会内嵌 `internal/agent/templates/workflows/*.yaml` 作为默认模板目录；运行目录可以通过 `templates/workflows/*.yaml` 添加或覆盖工作流模板。例如 `templates/workflows/plan-fix-audit.yaml` 会覆盖内置 `plan-fix-audit` 模板，并同时影响 `/workflow-templates`、Workflow 创建、校验、fork 和 Studio 模板选择。

复杂实现任务优先使用 `complex-project-delivery` 工作流模板。它会先分析用户需求、整理功能需求和验收标准，生成项目计划并等待用户确认；确认后循环执行“实现一个计划切片 -> 验证 -> 审查 -> 更新计划”，直到计划全部完成，再执行整体验证并输出最终完成报告。边界清晰的单个交付任务可以使用 `plan-implement-audit`，它会完成澄清、规划、实现、验证、审计、质量门禁和最终报告。

内置 Workflow Template 和 Team Template 现在也有质量校验：工作流模板必须能通过图校验，产出可回放产物，声明验收标准，经过质量门禁或策略门禁，并最终输出报告、交接、发布摘要或工作流草案；团队模板必须包含角色职责、交接关系、共享黑板引用和输出契约。

Team Template 已经从 Go 代码硬编码迁移为文件化资源。发布二进制会内嵌 `internal/agent/templates/teams/*.yaml` 作为默认团队目录；运行目录可以通过 `templates/teams/*.yaml` 添加或覆盖团队模板。例如 `templates/teams/web-research-team.yaml` 会覆盖内置 `web-research-team`，并同时影响 `/teams`、Workflow 选项、校验、执行、TeamState 和 Studio 表单。

Team scaffold preset 也已经文件化。内置 preset 从 `internal/scaffold/templates/teams/scaffolds/presets.yaml` 内嵌进二进制；运行目录可以通过 `templates/teams/scaffolds/*.yaml` 添加或覆盖 preset。CLI 的 `/new-team` 和 HTTP 的 `GET/POST /api/resources/team-templates/scaffolds` 使用同一套 preset 加载逻辑。

默认 helper 工具使用 `isolation: container`、只读 workspace、资源限制、只读根文件系统、`no_new_privileges`、`cap_drop: all`、`memory_swap` 和 `pull_policy`。

内置 Kit scaffold preset 已经从 CLI/HTTP 的 Go 代码里抽出成 YAML 文件。发布二进制会内嵌 `internal/scaffold/templates/kits/scaffolds/presets.yaml`，所以开箱即可用；运行目录也可以通过 `templates/kits/scaffolds/*.yaml` 添加或覆盖 preset。`/new-kit` 和 `GET/POST /api/resources/kits/scaffolds` 使用同一套 preset 加载逻辑。

Materialized kit 的输出模板也已经文件化。内置模板从 `internal/scaffold/templates/kits/materialized/generic/*.tmpl` 内嵌进二进制；运行时会先查 `templates/kits/materialized/<preset>/<file>.tmpl`，再查 `templates/kits/materialized/generic/<file>.tmpl`，最后使用内置默认模板。CLI 和 HTTP materialize 使用同一套渲染器。

模板文件名：

- `kit.yaml.tmpl`
- `agent.yaml.tmpl`
- `skill.md.tmpl`
- `tool-config.yaml.tmpl`
- `workflow.yaml.tmpl`
- `workflow-template.yaml.tmpl`
- `team.yaml.tmpl`
- `policy-rule.yaml.tmpl`
- `workflow.md.tmpl`

这些模板是 Go `text/template`，可以使用 `.Preset`、`.Names`、`.PrimaryProvider`、生成的容器 helper 路径 `.Tool.Source` / `.Tool.Target`，以及 `.AgentTools`、`.SkillTools`、`.PlannerTools`、`.ReviewerTools` 等工具列表。模板渲染后的 YAML 应保持兼容 Studio 编辑的同一套资源 schema，因为 HTTP materialize 会先渲染模板，再按正常资源校验链路解析和保存。

自定义 preset 文件可以是单个 preset：

```yaml
name: observability
title: Observability Kit
description: 构建监控、告警和运维交接流程。
category: operations
agents: [operations-specialist]
skills: [execution-plan]
workflow_templates: [operations-runbook]
team_templates: [operations-runbook-team]
examples:
  - title: Build an observability plan
    request: Create an observability rollout checklist.
    workflow: operations-runbook
    agent: operations-specialist
```

也可以批量定义：

```yaml
presets:
  - name: finance-analysis
    title: Finance Analysis Kit
    description: 面向财务分析场景的领域 Agent Kit。
    category: finance
```

`binary-analysis` preset 在 materialize 时会生成领域专用资源：Agent、Skill、Team Template 和 helper MCP server 会使用 `binary_file_info`、`binary_strings`、`hex_preview` 这些静态二进制分析工具，`read_text` 只用于 UTF-8 分析备注、报告或反编译文本。原始二进制文件应传普通路径，例如 `sample.bin`，不要用 `@file` 文本引用。

二进制分析 helper 代码也使用同一套 Python Tool 模板加载器。内置模板位于
`internal/scaffold/templates/tools/python/binary-analysis-server.py.tmpl`，运行目录可以用
`templates/tools/python/binary-analysis-server.py.tmpl` 覆盖。CLI materialize 和 HTTP Studio materialize 都使用这套渲染器。

HTTP Studio 可以通过 `POST /api/resources/kits/scaffolds/{preset}?materialize=1` 创建同样的完整模板包。请求体也可以传 `{"name":"acme-platform","materialize":true,"overwrite":false}`。响应会包含 `resources`、`restart_required` 和 `activation_steps`，前端可以据此展示创建了哪些资源，以及为什么需要重启或 reload。

## 内置案例

`examples/extension-workflow` 是一个紧凑的二开扩展示例，包含：

- `mcp_servers/` 下的 Python MCP server
- `configs/mcp_servers/` 下的模块化 MCP 配置
- `configs/agents/` 下的只读 Agent
- `skills/` 下的 Skill
- `workflows/` 下的图工作流

复制前可以运行：

```bash
python scripts/validate_extension_workflow.py
```

`examples/binary-analysis-kit` 是一个更完整的 materialized kit 示例，包含：

- `configs/agents/` 下的领域 Agent
- `skills/` 下的 GoFlow 原生 Skill
- `mcp_servers/` 下的 Docker/Podman 隔离 Python MCP helper
- `workflows/` 下的 Workflow
- `templates/workflows/` 下的 Workflow Template
- `templates/teams/` 下的 Team Template
- `policies/workflow_rules/` 下的 Policy Rule
- `kits/` 下的 Kit manifest

这个案例默认不会进入启动加载路径。用户复制到 runtime home 后，需要设置 helper 的 `tool_source` 环境变量，再重启或 reload。

复制前可先验证这个打包好的二进制示例：

```bash
python scripts/validate_binary_analysis_kit.py
```
