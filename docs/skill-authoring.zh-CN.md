# Skill 编写

Skill 用于打包特定领域的说明、参考资料、模板、确定性脚本和验收规则。GoFlow 兼容常见 `SKILL.md`，也会读取仓库中的 `AGENTS.md` 与 `CLAUDE.md`。

## 最小结构

```markdown
---
name: source-verification
description: 对互联网资料中的重要结论进行来源核验。
---

# 来源核验

在任务需要观点到来源证据时使用。

## 步骤

1. 记录待核验结论与范围。
2. 收集权威来源和独立来源。
3. 报告一致、冲突、日期和限制。

## 验收

- 每个重要结论都能对应来源。
- 冲突与不确定性必须明确保留。
```

触发条件要具体。较长资料放入 `references/`，可复用文档输入放入 `templates/`，确定性辅助代码放入 `scripts/`。

## 声明脚本

脚本必须按名称声明，并且路径保持在 Skill 目录内：

```yaml
scripts:
  - name: compile-evidence
    path: scripts/compile_evidence.py
    runtime: python
    output: json
    timeout: 30s
    approval: required
    args_schema:
      type: object
      additionalProperties: false
      properties:
        input:
          type: string
      required: [input]
```

脚本通过 `skill_runner/run_script` 执行，并继续受 Schema、智能体权限、内联审批、审计、任务副本和 MCP 原生隔离策略约束。脚本元数据只能描述意图，不能自行授予权限。第三方脚本所在连接使用 `auto` 或 `native_strong`。

## 质量要求

- 明确输入、输出和验收证据。
- 解析、文件转换、校验和导出优先使用确定性代码。
- 不在说明文字中隐藏网络、写入、执行或主机变更。
- 不写入 API Key、SSH 私钥、密码或个人数据。
- 交付可直接使用的产物与校验结果，而不是只有描述。

参见 [Skills](./skills.zh-CN.md)、[MCP](./mcp.zh-CN.md) 和 [资源](./resources.zh-CN.md)。
