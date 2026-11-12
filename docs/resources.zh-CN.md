# 资源

GoFlow V3 向用户提供六类资源：智能体、子智能体、工作流、Skill、单个 MCP 工具和 MCP 连接。单个工具可直接插入工作流；默认模型提供方由设置页单独管理。

## 原则

- 普通用户看到的名称和说明使用中文。
- 表单只展示用途、权限和启用状态。
- 内置资源开箱可用，并共享确定性工具。
- 简单任务直接执行，复杂任务选择受限工作流。
- 文件、Diff、测试、检查、证据和文档校验共同证明完成。

## 智能体与子智能体

默认任务智能体会把工作路由到开发编码、网络安全、网络运维、互联网资料核验和 Office/WPS 文档专家。子智能体只处理独立、边界明确的工作，不进行无产物讨论或无意义计划。

## 工作流

工作流是带有明确输入、阶段、输出、审批、检查点、恢复和质量门的内部执行图。画布仍可编辑，但普通任务不要求用户先设计工作流。

## Skill

Skill 兼容常见 `SKILL.md`，也能与 `AGENTS.md`、`CLAUDE.md` 共存。Skill 提供说明、声明脚本、参考资料和验收规则；脚本仍经过 MCP 权限和原生执行策略。

主机相关 Skill 包括 `local-host-diagnostics`、`managed-local-config` 和 `remote-host-diagnostics`，对应连接使用 `target_scope: host`。

## MCP 连接

MCP 使用标准 stdio JSON-RPC 和固定命令白名单。第三方连接使用 `auto` 或 `native_strong`，只操作任务副本，不能直接写真实工作区。

## 内置能力包

Kit 是内置资源组合，不增加新的用户资源类型。需要自定义时可实体化为互相关联的资源：

- `agent-framework-kit`：可实体化的工程化框架扩展能力。
- `security-research-kit`：仅在明确授权范围内执行，拒绝越界利用。
- `binary-analysis-kit`：默认只做静态证据，不执行不安全动态分析。
- `customer-support-kit`：包含隐私标记和复核人数要求。
- `multi-domain-agent-kit`：包含无效分支裁剪和合流冲突处理。
- `internet-research-kit`：包含来源核验智能体、结论来源映射，并交付 Markdown、JSON、CSV。

## 文档

文档专家能够读取现有 Office/WPS 文件、新建文件、基于模板修改、按用户意思准确修改、保留格式精确编辑、合并、校验并导出 PDF。开发、网安、运维和资料核验任务可复用它生成需求文档、报告、运行手册和证据包。

## 生命周期

资源页支持创建、修改、校验、启用、使用和删除。新路径通过验收后，旧资源、旧接口和无调用兼容代码会被删除，不保留“可能有用”的无效实现。

参见 [MCP](./mcp.zh-CN.md)、[脚手架](./scaffolds.zh-CN.md) 和 [配置](./configuration.zh-CN.md)。
