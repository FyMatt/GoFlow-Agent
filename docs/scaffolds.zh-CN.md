# 脚手架

脚手架只负责创建一组小而完整的起点，不增加第二套复杂产品界面，也不暴露底层原生沙箱参数。

## 工具脚手架

内置 `python-local` 预设生成：

- `mcp_servers/<name>.py`
- `configs/mcp_servers/<name>.yaml`

连接使用 `process_group`、固定命令白名单、`GOFLOW_WORKSPACE_ROOT` 路径限制、路径穿越拒绝、读取上限和结构化 MCP 错误。第三方工具审查后应改用 `auto`，由运行时决定是否必须进入 `native_strong`。

```text
GET /api/resources/tools/scaffolds
POST /api/resources/tools/scaffolds/python-local
```

普通用户只填写中文名称、中文说明和是否覆盖现有资源。V3 不提供运行镜像、挂载、namespace 等底层配置表单。

## 智能体与工作流

智能体脚手架生成中文说明和最小工具权限。工作流脚手架生成清晰的输入、输出、策略门和交付校验。简单任务直接执行，复杂任务只保留确实需要的阶段。

`complex-project-delivery` 展示 unified verification 和受限 `context.max_tokens`。`agent-framework-extension` 支持 materialization；生成包包含 `activation_steps`，并保存在所选 runtime home 下。

## 实体化能力包

一次实体化会生成互相关联的 Kit、智能体或子智能体、Skill、本地 MCP 工具、工作流、工作流模板，以及工作流内部使用的团队和策略资源。启用前必须审查。辅助工具默认使用原生进程边界和任务副本保护，只有任务确实需要时才增加写权限。

## 质量规则

- 普通用户可见名称和说明使用中文。
- 生成命令使用精确白名单。
- 工作流声明产物和质量门。
- 不写入 API Key、SSH 私钥或用户秘密。
- 只有真实运行证明产物可用，脚手架才算有效。

参见 [资源](./resources.zh-CN.md)、[配置](./configuration.zh-CN.md) 和 [MCP](./mcp.zh-CN.md)。
