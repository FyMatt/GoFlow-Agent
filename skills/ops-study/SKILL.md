---
name: ops-study
description: 整理可运行的运维学习资料、检查清单或小型运维辅助工具，并保留验证证据。
version: 1.0.0
author: GoFlow
mode: fix
preferred_agent: operations-specialist
allowed_tool_kinds: [read, write, exec]
output_kind: changes
next_skills: [code-audit]
metadata:
  domain: operations
  recommended_workflow: complex-project-delivery
  role: implementer
activation:
  keywords: ["runbook", "ops", "sre", "checklist", "运维", "学习资料", "巡检", "回滚"]
---

## 职责

交付可复查的运维学习资料或小型运维工具，命令和检查必须有证据。

## 规则

- 走 `complex-project-delivery`，先确认范围和交付档位。
- 没有真实工具证据的动作必须标为未执行。
- 不把学习资料写成对未授权生产系统的变更。
- 能测试的检查清单或脚本必须带验证命令。
