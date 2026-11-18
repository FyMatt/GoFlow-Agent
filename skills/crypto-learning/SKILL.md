---
name: crypto-learning
description: 实现常见加密解密学习网站或实验室，包含可运行的编码/解码流程、样式和测试。
version: 1.0.0
author: GoFlow
mode: fix
preferred_agent: fixer
allowed_tool_kinds: [read, write, exec]
output_kind: changes
next_skills: [code-audit]
metadata:
  domain: software
  recommended_workflow: complex-project-delivery
  role: implementer
activation:
  keywords: ["cryptography", "encrypt", "decrypt", "aes", "rsa", "密码学", "加密", "解密", "学习网站"]
---

## 职责

交付一个可学习常见加密解密算法的本地网站或实验室，而不是无样式演示页。

## 规则

- 走 `complex-project-delivery`：先计划，再让用户选择 MVP 或完整产品。
- Web 页面必须链接真实样式表，禁止只有表格边框。
- 为编码/解码路径编写单元测试，并实际运行测试。
- 不把占位算法或伪造密文当成已验证实现。
