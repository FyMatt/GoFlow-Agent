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

## MCP Tool

```text
/new-tool python workspace-helper
```

生成 Python MCP server 和对应配置。保存后运行：

```text
/reload-tools
/tools
```

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

容器 preset 会同时生成 `mcp_servers/<name>.py` 和 `configs/mcp_servers/<name>.yaml`，并把生成的工具文件以只读方式挂载进容器。发行版默认使用匹配版本的 `ghcr.io/fymatt/goflow-agent-mcp-python:<version>` 镜像，开发构建会退回 `latest`。生产 preset 应把默认 tag 替换为不可变的 `image@sha256:...` 引用。

运行状态会在 `/api/runtime.mcp_servers[]` 暴露 `container_image_reference_type`、`container_image_digest_pinned`、`container_image_production_ready` 和 `container_pull_policy`；配置诊断会给出对应的字段级 warning，前端可以直接按这些字段展示生产就绪状态。

## Agent 与 Provider

```text
/new-agent domain-reviewer
/new-provider local-llm
```

Agent 会写入 `configs/agents/`，Provider 会写入 `configs/providers/`。这种模块化结构避免 `configs/goflow.yaml` 变得过大。

## Workflow

```text
/new-workflow release-check
/workflow release-check review this change
```

工作流保存在 `workflows/<name>/workflow.yaml`，可以由 CLI 或 Web Studio 编辑和运行。

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

默认 helper 工具使用 `isolation: container`、只读 workspace、资源限制、只读根文件系统、`no_new_privileges`、`cap_drop: all`、`memory_swap` 和 `pull_policy`。

HTTP Studio 可以通过 `POST /api/resources/kits/scaffolds/{preset}?materialize=1` 创建同样的完整模板包。请求体也可以传 `{"name":"acme-platform","materialize":true,"overwrite":false}`。响应会包含 `resources`、`restart_required` 和 `activation_steps`，前端可以据此展示创建了哪些资源，以及为什么需要重启或 reload。
