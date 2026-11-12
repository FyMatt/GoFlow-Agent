# GoFlow V3 基线、模块取舍与迁移边界

本文档冻结 P0 的可复测基线和迁移边界。它描述产品代码，不包含外部材料。后续删除旧实现时以本文档和 `PROJECT_IMPLEMENTATION_PLAN.zh-CN.md` 为准。

## 1. 可复测基线

基线命令：

```powershell
python scripts/smoke_http_browser.py --required --baseline-only --baseline-output .artifacts/v3-baseline/browser-baseline.json --skip-build --binary <goflow.exe>
```

2026-08-09 在 Windows x64、Microsoft Edge、1440 x 1000 视口的结果：

| 指标 | 当前值 | 后续门槛 |
|---|---:|---:|
| 服务进程启动到 `/api/session` 就绪 | 2527 ms | 发布环境热启动低于 700 ms |
| 任务页首屏资源请求 | 24 | 不高于当前基线，且每次优化后记录差异 |
| 任务页首屏 API 请求 | 13 | 只保留首屏必要摘要，详情按需加载 |
| JavaScript 请求 | 7 | 路由模块继续按需加载 |
| JavaScript 解码体积 | 1090938 字节 | 首路由不超过 1.1 MB，并继续下降 |
| 样式请求 | 2 | 不增加重复主题样式 |
| 样式解码体积 | 645882 字节 | 删除被 V3 覆盖的旧规则后下降 |
| DOM 元素 | 640 | 不增加常驻板块 |
| 横向溢出 | 无 | 必须始终为无 |

基线不是完成标准。当前启动时间、13 个首屏 API 请求和样式体积仍高于产品目标，属于 P5/P6 优化输入。

真实任务质量基线采用 `.artifacts/real-provider-acceptance/20260805-144058/report.json`：

| 场景 | 状态 | 质量分 | 耗时 | 工具调用 | 用户产物 |
|---|---|---:|---:|---:|---:|
| 简单开发 Agent | completed | 100 | 65.734 秒 | 6 | 8 |
| 复杂开发 Workflow | completed | 100 | 388.953 秒 | 15 | 21 |

该报告使用真实 Provider、真实 Diff、测试和静态检查，但跳过视觉探针，不能作为 P1 视觉硬门证据。质量分只在确定性检查全部通过后记录，不能代替五领域连续三次验收。

当前内置资源文件基线：15 个智能体配置、9 个连接配置、15 个 Skill、4 个公开工作流、18 个内置工作流图模板。普通资源页只展示智能体、子智能体、工作流、技能和连接；内部模板和底层工具不按普通资源逐项展示。

## 2. 模块去向

### 2.1 Go 核心模块

| 模块 | 去向 | V3 责任 |
|---|---|---|
| `internal/agent` | 保留并收口 | 需求编译、简单/复杂路由、执行循环、工作流、质量门和统一交付 |
| `internal/api` | 保留并删除兼容面 | 任务工作台、附件、内联审批、产物、工作流、资源和设置 API |
| `internal/app` | 保留 | 组装配置、Provider、会话、MCP、调度和 HTTP 服务 |
| `internal/commandpolicy` | 保留 | 确定性命令分类、未知命令拒绝和审批策略 |
| `internal/config` | 保留并简化 | 普通设置与内部配置分层，拒绝容器配置 |
| `internal/dependencycache` | 保留 | 受控下载、SHA-256 内容寻址、只读任务依赖 |
| `internal/executiontarget` | 保留但按需显示 | SSH/远程目标存储、校验和主机密钥约束 |
| `internal/interfaces` | 保留 | LLM、MCP、Skill 等核心端口 |
| `internal/llm` | 保留 | OpenAI 兼容/Anthropic 协议、多模态、工具调用和 Provider 能力 |
| `internal/mcp` | 保留并强化 | 标准 MCP、按需启动、任务作用域和原生强隔离接入 |
| `internal/memory` | 合并到任务后台 | 代码地图、索引和已验证经验，不保留独立普通页面 |
| `internal/nativesandbox` | 保留 | Windows/Linux 原生边界与真实能力探针 |
| `internal/persistence` | 保留 | 原子写、锁和持久化基础设施 |
| `internal/policy` | 保留 | 工具风险和能力判定 |
| `internal/promptguard` | 保留 | 不可信内容包装和提示注入边界 |
| `internal/runtime` | 保留 | 审计和运行时证据 |
| `internal/scaffold` | 高级管理保留 | 资源导入/脚手架；不进入普通用户首屏 |
| `internal/scheduler` | 后台保留 | 计划任务执行；普通任务页不显示独立板块 |
| `internal/session` | 保留并版本化 | Agent/Workflow 状态机、审批、检查点、恢复和产物索引 |
| `internal/skill` | 保留 | `SKILL.md` 解析、匹配和兼容导入 |
| `internal/sshhostkey` | 保留 | SSH 主机密钥固定和变更拒绝 |
| `internal/taskstaging` | 保留 | 只读输入、任务副本、Diff、应用和恢复 |
| `internal/version` | 保留 | 版本和发布信息 |
| `internal/workspace` | 保留 | 工作区指令、边界和项目上下文 |

### 2.2 页面模块

| 页面模块 | 去向 |
|---|---|
| `views/chat.js` | 保留为任务工作台；历史、详情、审批和产物按需加载 |
| `views/workflows.js` | 保留为画布；节点库和设置使用抽屉 |
| `views/catalog.js` | 保留为五类资源页；隐藏内部工具和兼容资源 |
| `views/settings.js` | 保留模型 Key、工作区、外观和数据维护 |
| `views/schedules.js` | 不作为普通导航页面；后台兼容消费者迁移后删除独立视图 |
| 已删除的 `approvals.js`、`memory.js`、`overview.js`、`status.js`、`workspace.js` | 删除，不恢复；能力已合并到任务或设置 |

### 2.3 工具和资源

| 组 | 去向 |
|---|---|
| `file_tools` | 保留，负责受限文件读写、精确补丁、变更日志和恢复 |
| `command_tools` | 保留，负责结构化命令、构建、测试和静态检查 |
| `host_tools` | 保留，负责本机只读事实和受批系统操作 |
| `network_tools` | 保留，负责网络诊断和受限探测 |
| `ssh_tools` | 保留，负责已登记目标的远程诊断与变更 |
| `web_tools` | 保留，负责资料检索、抓取、缓存和来源证据 |
| `office_tools` | 保留，负责 DOCX/XLSX/PPTX/PDF 创建、读取、模板修改、精确修改和校验 |
| `skill_runner` | 运行时兼容保留，普通资源页隐藏，替代消费者完成后再评估删除 |
| `python_notes.py` | 兼容保留，普通资源页隐藏；不得扩展为新的产品能力 |
| 重复智能体、工作流、Kit 和领域工具副本 | 删除或合并，不以“可能有用”保留 |

## 3. V3 API 边界

### 3.1 正式页面 API

- 任务：`/api/runs`、`/api/runs/preflight`、`/api/agent-runs`、`/api/workflow-runs`。
- 输入和交付：`/api/attachments`、`/api/artifacts`、`/api/workspace-files`、`/api/workspace-file`。
- 工作流：`/api/workflow-graphs`、`/api/workflow-options`。
- 资源：`/api/resources`、`/api/resources/agents`、`/api/resources/subagents`、`/api/resources/skills`、`/api/resources/mcp`。
- 设置：`/api/workspace`、`/api/data/backups`、`/api/capabilities`。

集合接口默认返回摘要和分页窗口。运行事件、完整产物、阶段细节和大参数必须由详情接口按需加载。审批是运行项的动作，不建立独立普通页面。

### 3.2 内部或高级接口

`/api/runtime`、`/api/config/diagnostics`、`/api/update-policy`、`/api/targets`、`/api/schedules`、`/api/memory` 和脚手架接口只供高级诊断、后台执行或管理抽屉使用，不进入普通首屏。

### 3.3 待删除兼容接口

`/api/run`、`/api/run/stream`、独立 `/api/approvals/*`、`/api/collaboration/*`、`/api/team-state`、公开 Provider/底层 Tool/Kit/Team/Policy 资源接口和旧 Workflow Schema/Template 双入口属于兼容面。删除前必须通过调用者搜索、浏览器烟测、导入导出夹具和旧会话恢复测试证明已无消费者。P6 完成前不得把这些接口视为正式 V3 API。

## 4. 会话迁移边界

- `internal/session.Snapshot` 使用顶层 `schema_version`；当前版本为 1。
- 旧快照缺少版本时按版本 0 迁移，保留已有字段并写回版本 1 的压缩索引和完整归档。
- 高于当前版本的快照必须拒绝，不能静默丢字段或覆盖用户数据。
- Agent 运行、Workflow 运行、审批范围、附件引用、资源内容哈希、检查点、Token 记录和产物引用必须跨重启保留。
- 大内容保存在内容寻址对象仓库，`session.json` 只保留有界摘要与引用。
- 数据升级前创建备份；迁移失败时保持原数据和备份，不允许半迁移继续启动。
- 旧页面状态不属于持久化契约。页面只从正式运行、工作流、附件和产物接口重建状态。

## 5. 执行和质量 Schema 责任

| 契约 | 权威类型/实现 | 不变量 |
|---|---|---|
| 用户消息与附件 | `schema.Message`、`MessageContentPart`、`AttachmentRef` | 附件使用对象引用；视觉不支持时明确失败 |
| 工具调用 | `schema.Tool`、`ToolCall`、`ToolResult`、`ToolRiskProfile` | 参数结构化、作用域明确、风险与审批不可旁路 |
| 任务作用域 | `schema.TaskWorkspaceScope` | 输入和依赖只读、任务副本可写、路径互不重叠 |
| Agent 状态机 | `session.AgentRunSnapshot` | 每个状态转移持久化，审批和恢复幂等 |
| Workflow 状态机 | `session.WorkflowRunSnapshot`、工作流图文档 | 阶段依赖确定，失败只重试允许阶段，最终统一交付 |
| 执行上下文 | `agent.ExecutionContext` | 工作区、目标、运行 ID、工具作用域贯穿全链路 |
| 质量结果 | `schema.Verification`、`Finding`、`Change`、`AgentResult` | 成功必须来自真实文件、Diff、命令、测试或文档校验 |
| 工作流结果 | `schema.WorkflowResult`、`WorkflowStageResult` | 记录完成阶段、阻塞、产物、验证和剩余风险 |
| 资源兼容 | Skill/MCP/仓库指令解析器与内容哈希固定 | 未知字段不静默丢失，外部格式不能绕过权限 |

执行循环固定为：需求编译 -> 路由 -> 上下文检索 -> 确定性动作或模型决策 -> 工具代理 -> 持久化观察 -> 质量门 -> 统一交付。没有新决策、工具动作、用户问题或产物的模型调用视为无效调用，必须去重或终止。

## 6. 删除准入

删除旧模块、API 或资源前必须同时满足：

1. 新能力通过单元、集成、真实 Provider 和浏览器验收。
2. `git grep`、路由资源测试和运行夹具证明无正式消费者。
3. 旧数据可迁移、可导出或明确不属于持久化契约。
4. 删除后全仓测试、文档、资源链接、中文化和双目标构建通过。
5. 不保留旧实现的空目录、重复样式、翻译、测试夹具或兼容包装。

