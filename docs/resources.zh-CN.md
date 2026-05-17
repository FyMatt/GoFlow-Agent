# 资源与设置指南

这份文档说明 GoFlow 的主要资源分别做什么、放在哪里，以及它们在 CLI 和 Web Studio 中如何串联。

如果你现在主要是不清楚“某类资源是什么、该放哪里、和别的资源怎么关联”，看这份文档。
如果你是在 Studio 里配置工作流节点，直接看 [工作流图](./workflows.zh-CN.md)。
如果你是在修改 Provider、Agent 或 MCP 的运行时配置，直接看
[配置说明](./configuration.zh-CN.md)。

## Runtime Home 和 Workspace

GoFlow 会把两个根目录分开：

- `runtime home`：GoFlow 安装目录或仓库目录。这里保存配置、Agent、Skill、MCP 定义、工作流、模板、Schema、Kit 和文档。
- `workspace root`：目标项目目录。只有文件工具、`@file` 引用、生成文件和工作流修改可以作用在这里。

纯聊天可以不确认 workspace。只要涉及读文件、写文件、执行命令、`@file` 引用或运行工作流，都需要先确认 workspace。

## Web Studio 入门路径

新用户建议先从 **资源 -> Kit 快速模板** 开始，而不是一开始就分别编辑每一种资源。

推荐路径是：

1. 选择 `multi-domain-agent`。
2. 点击 **创建联动起步资源**，一次生成 Kit 以及它引用的 Agent、Skill、Tool、Workflow、Workflow Template、Team Template 和 Policy Rule 文件。
3. 打开 Workflow Studio，运行推荐工作流，通常是 `multi-domain-intake-router`。
4. 等联动资源已经存在后，再按需要单独编辑某个 Agent、Skill、Tool 或 Workflow。

Resources 页面中的资源地图可以这样理解：

- Kit 负责打包一个场景。
- Agent 负责模型、权限和工具策略。
- Skill 负责可复用的专家流程。
- Tool 负责外部能力。
- Workflow 负责串联阶段、数据流、审批和证据。
- Team Template 负责多 Agent 角色和交接。
- Policy Rule 负责质量门禁或风险门禁。

Kit 卡片还会显示两类实用引导：

- **推荐运行路径**：列出应该一起使用的 Agent、Workflow Template、Team
  Template 和主 Skill。点击资源块可以在资源页中定位对应资源。
- **资源链路**：展示 Kit 物化后的常见执行顺序：Agent -> Skill -> Tool ->
  Workflow -> Team -> Policy。数字表示这个 Kit 里关联了多少个对应资源。

如果想直接查看推荐工作流，点击 Kit 卡片里的 **打开工作流**。系统会在
Workflow Studio 中把推荐模板打开成一个新的可编辑图，方便你先检查节点输入、
输出、审批和证据规则，再保存自己的副本。

## 内置领域起步包

内置 Kit 是给用户复制、物化和二开的起步包，不是孤立示例。每个 Kit 都会引用对应领域需要的 Agent、Skill、Tool、Team、Workflow Template 和 Policy Rule。

仓库会用 `python scripts/validate_resource_links.py` 检查这些引用关系。新增或修改生产级 starter 时，Kit 和对应 scaffold preset 应该引用同一组具体的 Agent、Skill、Tool、Workflow Template、Team Template 和 Policy Rule。只要示例里说要运行某个 workflow，这个 workflow 或 template 也应该出现在 Kit 引用里，这样 Web Studio 才能在物化资源前解释清楚它们之间的关系。

| Kit | 主 Agent | 主工作流模板 | Team Template | 常见二开方向 |
| --- | --- | --- | --- | --- |
| `multi-domain-agent-kit` | `chat` | `multi-domain-intake-router` | 全部领域 Team | 增删领域、路由分支和最终交接要求 |
| `software-engineering-kit` | `software-engineer` | `plan-fix-audit`、`software-team-review-gate` | `software-task-team` | 调整编码、测试、审计和审批策略 |
| `web-security-kit` | `web-security-researcher` | `web-research-risk` | `web-research-team` | 调整目标录入、证据采集和风险门禁 |
| `security-research-kit` | `security-researcher` | `security-audit-evidence-gate` | `audit-security-team` | 调整授权范围、严重性规则和报告格式 |
| `binary-analysis-kit` | `binary-analyst` | `binary-triage` | `binary-triage-team` | 增加二进制辅助工具、输出章节和复核门禁 |
| `documentation-kit` | `documentation-specialist` | `docs-review-publish` | `documentation-team` | 调整文档风格、发布检查表和验收条件 |
| `operations-runbook-kit` | `operations-specialist` | `operations-runbook` | `operations-runbook-team` | 自定义预检、回滚和审批门禁 |
| `customer-support-kit` | `support-specialist` | `customer-support-triage` | `customer-support-team` | 调整录入字段、回复策略和升级规则 |
| `agent-framework-kit` | `framework-extension-architect` | `agent-framework-extension` | `framework-extension-team` | 创建新的垂直 Agent/Skill/Tool/Workflow/Team/Policy/Kit 包 |

推荐操作顺序：

1. 在 Web Studio 里查看 Kit 卡片，或用 `/kits <kit-name>` 查看引用关系。
2. 用 **创建联动起步资源** 或 `/new-kit <preset> <name> --materialize` 物化资源。
3. 检查生成的文件，并按界面提示 reload 或重启。
4. 如果要改图，优先 fork Workflow Template，不要直接改内嵌默认模板。

如果 Kit 卡片看起来不完整，可以运行：

```bash
python scripts/validate_resource_links.py
```

它会指出具体哪个 Kit 或 scaffold preset 指向了不存在的资源。

## 文件内容规则

GoFlow 读取工作区文本文件时统一按 UTF-8 处理，允许 UTF-8 BOM 并自动去除。如果文件不是合法 UTF-8，就应该使用二进制分析工具，而不是 `read_file` 或 `@file`。

## 核心配置

主配置文件是 `configs/goflow.yaml`。

### `agent`

控制默认运行参数。

- `name`：启动横幅显示名和默认运行名。
- `max_iterations`：最大思考或工具循环次数。
- `timeout`：整体超时时间。

### `default_agent`

用户没有显式切换时默认使用的 Agent。

### `skill`

控制 Skill 发现方式。

- `directory`：`SKILL.md` 包的基础目录。
- `match_threshold`：Skill 匹配严格程度。
- `hot_reload`：是否自动重载 Skill 文件。

### `audit`

控制审计显示。

- `enabled`：是否启用审计日志。
- `redact_content`：是否隐藏敏感内容。
- `show_trace_in_cli`：是否在终端打印 trace。

### `verifier`

把验证工作路由到单独的 Agent / 模型。

- `enabled`：是否启用 verifier。
- `agent`：验证时使用的 Agent。
- `modes`：哪些模式启用 verifier。
- `max_tokens`：验证预算。

### `cost_control`

成本控制辅助功能。

- `router.enabled`：是否使用更便宜的模型做路由分类。
- `router.provider`、`router.model`：可选覆盖目标。
- `router.max_tokens`：保持分类输出简短。
- `summarizer.enabled`：是否对长工具循环或长上下文做摘要。
- `summarizer.provider`、`summarizer.model`：可选覆盖目标。
- `summarizer.max_tokens`：摘要预算。

### `tool_risk_policy`

控制高风险工具的审批。

- `require_approval_for_unsandboxed_risky_tools`：要求审批。
- `disable_remember_for_unsandboxed_risky_tools`：不记住审批。
- `reject_unsandboxed_risky_tools`：直接拒绝不安全工具。

### `session`

控制历史会话持久化。

- `max_history`：保留多少轮上下文。

### `log`

控制终端日志样式。

- `level`：日志级别。
- `format`：文本或结构化输出。

## 资源类型

### Provider

路径：`configs/providers/*.yaml`

用于模型接入。

`base_url`、`api_key` 和 `model` 是 Provider 真正服务 Agent 运行前所需字段，但首次启动时可以暂时留空，这样 Web Studio 可以正常打开并引导配置。设置了 `fallback_provider` 时，它仍必须指向已存在的 Provider。

从 Web Studio 保存的 Provider 资源会写入这个 YAML 路径。字面量 API Key 会明文保存，
因此共享仓库或发布版的 Provider 文件建议使用 `${GOFLOW_API_KEY}` 这类环境变量引用。

常见字段：

- `provider`
- `base_url`
- `api_key`
- `model`
- `fallback_provider`
- `timeout`
- `temperature`
- `max_tokens`
- `retry_count`
- `retry_backoff`

适合把主模型和备份模型分开管理。

### Agent

路径：`configs/agents/*.yaml`

用于定义角色行为。

常见字段：

- `name`
- `mode`
- `provider`
- `model`
- `system_prompt`
- `tool_policy`
- `allowed_tool_kinds`
- `allowed_tools`
- `max_iterations`
- `timeout`

适合拆分 planner、fixer、auditor、support、安全、运维等角色。

### Skill

路径：`skills/<skill-name>/SKILL.md`

用于复用某个领域的标准流程或专家步骤。

常见子目录：

- `agents/`：辅助 Agent 片段
- `assets/`：参考资源
- `references/`：补充资料
- `scripts/`：确定性脚本
- `templates/`：输出模板

Skill 文档通常要写清：

- 这个 Skill 做什么
- 什么时候用
- 需要哪些工具
- 输出应该长什么样
- 是否允许调用脚本

### MCP 工具 / 服务

路径：`configs/mcp_servers/*.yaml`

用于文件工具、Web 工具、Python 工具或自定义工具服务器。

常见字段：

- `command`
- `args`
- `env`
- `enabled`
- `workdir`
- `isolation`
- `isolation_options`
- `allowed_commands`

当工具会写文件、执行命令或访问网络时，建议使用 `isolation: container`。

### Workflow

路径：`workflows/<name>/workflow.yaml`

用于可执行工作流图。

常见节点字段：

- `name`
- `node_type`
- `agent`
- `skill`
- `tool`
- `params`
- `input`
- `outputs`
- `next`
- `routes`
- `cases`
- `condition`
- `switch_on`
- `approval`
- `artifacts`
- `acceptance_criteria`

好的工作流，核心是每个阶段都有明确输入契约和输出契约。

### Workflow Template

路径：`templates/workflows/*.yaml`

用于可复用的工作流蓝图。
GoFlow 的内置 Workflow Template 来自 `internal/agent/templates/workflows/*.yaml`，发布时会内嵌进二进制。运行目录下的 `templates/workflows/*.yaml` 可以新增模板，也可以用同名文件覆盖内置模板。

常用内置模板包括 `complex-project-delivery`、`plan-implement-audit`、`plan-fix-audit`、`task-decomposition-plan` 和 `multi-domain-intake-router`。

适合做这些起步模板：

- 规划
- 软件实现
- Web 安全审查
- 二进制初筛
- 文档交付
- 运维手册
- 客服交接

内置 Workflow Template 按质量门禁式交付起步模板维护：包含验收标准、可回放产物、质量门禁或策略门禁，以及最终报告/交接类输出。用户可以直接运行，也可以 fork 后微调，而不需要先修补图结构。

### Team Template

路径：`templates/teams/*.yaml`

用于可复用的多 Agent 协作形态。
GoFlow 的内置团队模板来自 `internal/agent/templates/teams/*.yaml`，发布时会内嵌进二进制。运行目录下的 `templates/teams/*.yaml` 可以新增团队模板，也可以用同名文件覆盖内置模板。

常见字段：

- `role_templates`
- `handoffs`
- `blackboard_templates`
- `quorum_presets`
- `output_contract`

内置 Team Template 会声明角色职责、产出物、交接产物、共享黑板引用和输出契约，因此 `team` 工作流节点展开后也能形成可审计的多 Agent 阶段。

Team Template 负责定义多个 Agent 如何协作，而不是单个 Agent 如何说话。

### Policy Rule

路径：`policies/workflow_rules/*.yaml`

用于复用型门禁规则。
可以通过 `/new-policy-rule <preset> <name>` 或
`POST /api/resources/policy-rules/scaffolds/{preset}` 生成 starter 规则。内置
scaffold preset 从
`internal/scaffold/templates/policies/scaffolds/presets.yaml` 内嵌进二进制，
运行目录可以通过 `templates/policies/scaffolds/*.yaml` 添加或覆盖。

常见字段：

- `name`
- `label`
- `operator`
- `description`
- `params`

适合审批门禁、风险门禁、质量门禁等控制逻辑。
`/api/workflow-options` 返回的内置 Policy Rule metadata 从
`internal/agent/templates/policy_rules/*.yaml` 内嵌进二进制。它负责定义内置
门禁的 label、description、operator 和可编辑参数。运行目录下
`policies/workflow_rules/*.yaml` 则用于新增真正可执行的自定义规则，并以
`source: custom` 返回给编辑器。

### Workflow Node Metadata

路径：

- `templates/workflow_nodes/*.yaml`
- `metadata/workflow_nodes/*.yaml`

用于扩展工作流编辑器里的节点说明、提示、示例、默认值和字段。
GoFlow 的内置节点 catalog 来自
`internal/agent/templates/workflow_nodes/*.yaml`，发布时会内嵌进二进制；运行目录下
的 metadata 文件用于新增或覆盖这些可执行节点类型的编辑器展示信息。

这部分直接影响 Web Studio 的节点面板和右侧说明。
Studio 会把内置 metadata 标记为“内置”，不会在普通节点配置里展示内嵌源码路径。
只有运行目录下的覆盖文件会以短路径显示，方便操作者找到可编辑文件，同时避免把它误解成节点参数。

### Expression Helper Metadata

路径：

- `templates/expression_helpers/*.yaml`
- `templates/workflow_expressions/*.yaml`
- `metadata/expression_helpers/*.yaml`
- `metadata/workflow_expressions/*.yaml`

用于扩展表达式提示、函数签名、示例和帮助文本。
内置 helper metadata 从 `internal/agent/templates/expression_helpers/*.yaml`
内嵌进二进制；运行时 metadata 文件可以覆盖编辑器展示的 label、signature、
args、examples、hints 和 warnings。
Studio 会把随包内置的 helper 显示为“内置”，只有用户可编辑的运行时覆盖文件才显示短路径。

### Workflow Schema 资源

路径：`schemas/workflows/*.json`

用于持久化保存工作流运行中观察到的输出结构。

这个目录是可选的，默认通常为空。这里保存的是通过 Studio 或 HTTP API
写入的可复用 Workflow Schema 资源，不是内置 Workflow Template。

运行时仍会根据保留的工作流运行记录和导入内容维护活动 session schema
catalog。保存下来的 schema 资源可以帮助后续阶段和编辑器更好地理解上游
输出，也可以在需要时重新激活回当前 session catalog。

### Kit

路径：`kits/<kit-name>/kit.yaml`

用于把一整套资源打包成可版本化的组合包。

Kit 可以引用：

- agents
- providers
- skills
- tools
- workflows
- workflow templates
- team templates
- policy rules
- 示例资源

如果你想做一个领域级开箱即用方案，Kit 是最合适的入口。

Web Studio 的 Kit 列表会同时显示数量和具体引用名称，例如
`provider_refs`、`agent_refs`、`skill_refs`、`tool_refs`、
`workflow_template_refs`、`team_template_refs` 和 `policy_rule_refs`。这样用户
在编辑或导出 Kit 之前，就能知道这个 Kit 实际会联动哪些资源。

## 资源之间的关系

可以把整套框架理解成：

1. Provider 负责连模型。
2. Agent 负责角色和工具策略。
3. Skill 负责可复用流程。
4. Tool 负责具体动作。
5. Workflow 负责把阶段串起来。
6. Team Template 负责多角色协作。
7. Kit 负责把整套东西打包复用。

## CLI 和 Web 的关系

CLI 和 Web Studio 调用的是同一套后端资源 API。

- 想用文本方式就用 CLI。
- 想看可视化编辑、校验、资源浏览和拖拽编排，就用 Web Studio。
- 两边保存出来的都是同一份 runtime home 里的文件资源。

## 新手建议

如果你刚开始用，建议按这个顺序看：

1. `configs/goflow.yaml`
2. `configs/agents/*.yaml`
3. `skills/<skill>/SKILL.md`
4. `configs/mcp_servers/*.yaml`
5. `templates/workflows/*.yaml`
6. `templates/teams/*.yaml`
7. `kits/<kit>/kit.yaml`

如果你想做一个生产级的领域启动包，先从 Kit 开始，再逐步微调它引用到的 Agent、Skill、Tool 和 Workflow。
