# Skill 开发

[English](./skill-authoring.md) | [简体中文](./skill-authoring.zh-CN.md)

Skill 是可复用的任务指导，存放在 `skills/<name>/SKILL.md`。

当任务需要领域流程、工具偏好、输出结构或可复用工作方式时，应使用 Skill。

## CLI 脚手架

```text
/skill-templates
/new-skill <template> <name>
```

内置模板：

- `code-writing`
- `code-audit`
- `web-vulnerability-research`
- `binary-vulnerability-research`
- `execution-plan`

示例：

```text
/new-skill code-audit custom-audit
```

生成后会写入 `skills/custom-audit/SKILL.md`，并进行校验和 reload。

## Web Studio

```text
http://127.0.0.1:8080/console#catalog
```

Skill editor 会写入 `skills/<name>/SKILL.md`，使用与启动时相同的 parser 校验 frontmatter 和 instructions。

## 最小格式

兼容 Claude/Codex 风格的最小 Skill：

```markdown
---
name: image-workflow
description: Use when the user asks to generate, edit, or evaluate bitmap images.
metadata:
  short-description: Image workflow helper
---

# Image Workflow

Use this skill for image tasks.
```

## 原生 GoFlow 格式

原生格式可以增加：

- 激活关键词。
- 推荐 Agent。
- 工具声明。
- 参数。
- 输出类型。
- 后续 Skill。
- 资源文件。

这些字段让 Skill 更适合复杂工作流编排。

## 复杂 Skill 脚本

复杂 Skill 可以在 frontmatter 里声明确定性的辅助脚本，而不是把所有逻辑都塞进说明文字里。适合做重复的数据采集、解析、归一化等任务，但仍然要走正常的审批和隔离策略。

示例：

```yaml
scripts:
  - name: collect-assets
    description: Fetch and summarize page assets for review.
    path: scripts/collect_assets.py
    runtime: python
    output: json
    timeout: 30s
    isolation: container
    workspace_mount: ro
    network: disabled
    approval: required
    args_schema:
      type: object
      additionalProperties: false
      properties:
        url:
          type: string
      required: [url]
```

GoFlow 会校验这些声明，并通过正常运行时暴露出来：

- 脚本路径必须在 Skill 目录的 `scripts/` 内。
- runtime、isolation、network、approval、timeout、output 都会校验。
- args_schema 必须是 object，且默认关闭额外字段。
- 声明的脚本会暴露到运行时提示词和资源 API。
- 匹配到包含脚本的 Skill 后，只有当前 Agent profile 仍允许 `exec` 时，才会暴露 `skill_runner/run_script`。

脚本执行不是隐藏的后门能力，而是普通工具调用。模型需要通过
`skill_runner/run_script` 调用：

```json
{"skill":"my-skill","script":"collect-assets","args":{"url":"https://example.test"}}
```

该调用会继续经过 Agent 工具权限、审批策略、审计日志、风险元数据和 MCP
隔离配置，和其他 `exec` 工具一致。

脚本会从 stdin 和 `GOFLOW_SKILL_ARGS_JSON` 环境变量接收 JSON 参数。返回值是
JSON 对象，包含 Skill 名称、脚本名称、声明的 runtime/output/network
元数据、stdout、退出码、耗时、是否超时，以及声明 `output: json` 时的 JSON
输出校验状态。

`isolation`、`network`、`workspace_mount` 字段用于表达脚本意图。真正的执行边界
由 MCP server 配置决定。需要强隔离时，使用 MCP `isolation: container`
启用 Docker/Podman 沙箱；默认本地二进制配置使用 `process_group`，它主要是进程生命周期边界，不是强安全沙箱。

## 编写建议

- 明确触发场景。
- 明确允许使用的工具和输出格式。
- 把复杂参考材料放到 `references/`。
- 把可执行辅助脚本放到 `scripts/`。
- 把模板文件放到 `templates/`。
- 不要在 Skill 中授予 Agent 本来没有的权限；权限仍由 Agent profile 和 runtime policy 控制。
