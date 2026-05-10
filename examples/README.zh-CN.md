# 示例

[English](./README.md) | [简体中文](./README.zh-CN.md)

这个目录保存 GoFlow 二开时可以复制参考的示例。

大多数用户应先从 `kits/` 里的内置 Kit 或 Web Studio 的 starter 卡片开始。这里的示例更适合需要完整文件级参考、复制到其他 runtime home，或在 CI 中做验证的开发者。

## 什么时候看哪个示例

| 示例 | 适合场景 |
| --- | --- |
| `extension-workflow` | 最小端到端扩展链路：自定义 MCP 工具、自定义 Agent、自定义 Skill 和图工作流 |
| `binary-analysis-kit` | 生产级领域包参考：串联 Agent、Skill、Tool、Team、Workflow Template、Policy Rule 和 Kit |

## `extension-workflow`

一个紧凑的扩展 smoke test，包含：

- Python MCP server
- 模块化 MCP server 配置
- 窄权限只读 Agent
- 自定义 Skill
- 图工作流

当你想理解最小端到端扩展链路时，从这个示例开始。
即使你平时主要使用更完整的内置 Kit，也建议保留这个示例，作为最小文件级
smoke test 参考。

复制前可验证：

```bash
python scripts/validate_extension_workflow.py
```

## `binary-analysis-kit`

一个更完整的 materialized kit 示例，包含：

- 领域 Agent
- GoFlow 原生 Skill
- Docker/Podman 隔离的 Python MCP helper
- Workflow
- Workflow Template
- Team Template
- Policy Rule
- Kit manifest

当你想理解 Agent、Skill、Tool、Team、Workflow Template、Policy Rule 和 Kit
如何在生产级领域包里互相关联时，看这个示例。

可在仓库根目录验证部署资源和示例：

```bash
python scripts/validate_deployment_assets.py
python scripts/validate_extension_workflow.py
python scripts/validate_binary_analysis_kit.py
python scripts/validate_resource_links.py
```

`kits/` 里的内置领域包覆盖多领域路由、软件研发、Web 安全、通用安全研究、二进制分析、文档、运维、客服和框架二开。Web Studio 用户建议优先从这些 Kit 开始；本目录示例保留为可审查、可复制的参考包。
