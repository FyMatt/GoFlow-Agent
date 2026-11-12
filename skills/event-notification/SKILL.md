---
name: event-notification
description: 将已形成的有来源事件提交到用户配置的本地或外部通知通道，记录实际投递状态。
version: 1.0.0
author: GoFlow
mode: audit
preferred_agent: security-researcher
allowed_tool_kinds: [network]
output_kind: report
tools:
  - name: notification_tools/publish_event
    required: true
activation:
  keywords: ["事件通知", "event notification"]
metadata:
  title: 事件通知
  domain: event-notification
  role: notifier
---

只提交上游已形成的事件。使用用户已配置的 channel_id，未指定时使用 local；不得让模型输入任意 Webhook URL，不解析或输出凭据，不把原始日志和敏感材料塞进通知。

通知包含真实来源引用、目标、确认级别和最小摘要，不夸大为已确认入侵或已成功处置。单次调用 publish_event 后报告其 event_id 与实际 delivery_status；发送失败、仅本地保存或缺少通道均不能写成外部通知成功。此技能不负责诊断、修复或改变目标状态。
