---
name: security-evidence-review
description: 只读复核安全发现的证据、授权与置信度，形成可追溯的结构化复核结论。
version: 1.0.0
author: GoFlow
mode: audit
preferred_agent: auditor
allowed_tool_kinds: [read]
output_kind: findings
tools:
  - name: file_tools/read_file
    required: false
  - name: file_tools/read_file_range
    required: false
activation:
  keywords: ["安全证据复核", "security evidence review"]
metadata:
  title: 安全证据复核
  domain: evidence-review
  role: reviewer
---

只根据原始授权、真实工具结果和上游结构化发现复核，不执行新扫描或测试，不读取猜测路径。最多补读三项用户给定且确有必要的具体材料，不修改被调查目标。

核对结论是否引用真实来源、前提是否成立、是否存在反证、工具是否成功、采集是否截断。扫描器提示不是已确认漏洞；失败采集不是正常。不得因缺少英文标题拒绝中文报告，也不得在仍缺关键证据时写有条件 PASS 并宣称全部验证完成。

输出 JSON：summary、decision、alert_required、severity、evidence_refs、findings、limitations、recommendations。有充分证据时沿用 confirmed；可疑未确认用 suspected；不能判断用 inconclusive。只有相关覆盖完整且未发现问题时用 clean。告警必须明确区分预警、确认发现与采集异常。
