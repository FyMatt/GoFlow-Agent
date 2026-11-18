# 示例

[English](./README.md) | [简体中文](./README.zh-CN.md)

这个目录保存 GoFlow 二开时可以复制参考的示例。

大多数用户应先从 `kits/` 里的内置 Kit 或 Web Studio 的 starter 卡片开始。这里的示例更适合需要完整文件级参考、复制到其他 runtime home，或在 CI 中做验证的开发者。

## 什么时候看哪个示例

| 示例 | 适合场景 |
| --- | --- |
| `extension-workflow` | 最小端到端扩展链路：自定义 MCP 工具、自定义 Agent、自定义 Skill 和图工作流 |
| `custom-resource-pack` | 不修改核心增加领域：Kit 自动分流、工作流、真实文件读写和来源校验 |

`custom-resource-pack` 使用现有 MCP，不增加主程序领域枚举。可运行 `python scripts/resource_pack_acceptance.py --binary <本次构建的 goflow.exe>` 验证；本地确定性模型只证明扩展机制，不冒充真实模型质量验收。

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

可在仓库根目录验证部署资源和示例：

```bash
python scripts/validate_deployment_assets.py
python scripts/validate_extension_workflow.py
python scripts/validate_resource_links.py
```

`kits/` 里的内置领域包覆盖多领域路由、软件研发、Web 安全、通用安全研究、二进制分析、文档、运维、客服和框架二开。Web Studio 用户建议优先从这些 Kit 开始；本目录只保留会在 CI 中独立运行的最小扩展示例。
