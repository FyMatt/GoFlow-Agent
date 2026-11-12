package agent

import "strings"

// Only named report sections have display-language aliases. Everything else
// (business values, machine fields, quoted literals) retains exact Contains.
var workflowReportSectionAliases = map[string][]string{
	"Objective":              {"目标"},
	"Acceptance Criteria":    {"验收标准"},
	"Implementation Plan":    {"实施计划", "实现计划"},
	"Change Steps":           {"变更步骤"},
	"Verification Commands":  {"验证命令"},
	"Done Criteria":          {"完成标准", "完成条件"},
	"Implementation Summary": {"实现摘要", "实施摘要", "实现总结", "实施总结"},
	"Change Summary":         {"变更摘要"},
	"Files Changed":          {"变更文件", "已修改文件", "已变更文件"},
	"Verification Attempted": {"验证尝试", "验证记录", "已尝试验证"},
	"Verification Result":    {"验证结果", "验证结论"},
	"Commands Run":           {"已执行命令", "执行命令", "运行命令"},
	"Audit Decision":         {"审计决定", "审查决定", "审计结论"},
	"Findings":               {"发现", "安全发现", "审计发现", "调查结果"},
	"Recommendation":         {"建议", "建议结论"},
	"Release Readiness":      {"发布就绪状态", "发布准备情况", "发布就绪度"},
	"Final Status":           {"最终状态", "交付状态"},
	"Delivery Status":        {"交付状态"},
	"Verification Evidence":  {"验证证据"},
	"Revision Required":      {"需要返修", "返修要求"},
	"Next Steps":             {"下一步", "后续步骤"},
}

func workflowGraphAcceptanceContains(actual, expected, ref string) bool {
	if strings.Contains(actual, expected) {
		return true
	}
	if ref != "" && ref != "result.output" {
		return false
	}
	aliases := workflowReportSectionAliases[expected]
	if len(aliases) == 0 {
		return false
	}
	// Aliases must be complete headings or labels, never incidental prose or
	// fenced examples. Preserve the original exact substring behavior above.
	fence := ""
	for _, line := range strings.Split(actual, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			marker := line[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if index := strings.IndexAny(line, ":："); index >= 0 {
			line = line[:index]
		}
		line = strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "*"))
		for _, alias := range aliases {
			if line == alias {
				return true
			}
		}
	}
	return false
}
