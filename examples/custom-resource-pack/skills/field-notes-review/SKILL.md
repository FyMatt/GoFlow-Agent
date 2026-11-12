---
name: field-notes-review
description: 从用户给定的田野笔记整理可追溯记录并读回报告，不要求核心认识该领域。
version: 1.0.0
author: Example
mode: fix
preferred_agent: field-notes-reporter
allowed_tool_kinds: [read, write]
tools:
  - name: file_tools/read_file
    required: true
  - name: file_tools/write_file
    required: true
activation:
  keywords: [田野笔记整理, field notes review]
metadata:
  title: 田野笔记整理
  domain: field-notes
---

只读取用户明确给出的源文件。提取原文已有的记录、观察值和不确定信息；缺失日期、单位或地点时标为未知。将 summary、source、observations、unknowns、evidence_refs 写到用户指定的 JSON 报告文件，再用 read_file 读回验证。报告文件必须位于工作区，不能修改输入文件或写入系统路径。最后返回真实的报告路径与检查结果，不只给下一步建议。
