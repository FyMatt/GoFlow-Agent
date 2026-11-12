package agent

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

var numberedRequirementPattern = regexp.MustCompile(`(?m)^\s*(?:\d+[\.、\)]\s+|[-*]\s+)`)

func parseDeliveryScope(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(text, "完整产品") || strings.Contains(text, "产品级") || strings.Contains(lower, "full product"):
		return "product"
	case strings.Contains(lower, "mvp") || strings.Contains(text, "最小可行"):
		return "mvp"
	default:
		return ""
	}
}

func normalizeDeliveryScope(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "product", "完整产品", "产品级":
		return "product"
	case "mvp", "最小可行":
		return "mvp"
	default:
		return parseDeliveryScope(value)
	}
}

func displayDeliveryScope(scope string) string {
	if normalizeDeliveryScope(scope) == "product" {
		return "完整产品"
	}
	return "MVP"
}

func workflowRequestIsSparse(request string) bool {
	text := strings.TrimSpace(request)
	if text == "" {
		return true
	}
	n := utf8.RuneCountInString(text)
	if n < 60 {
		return true
	}
	if len(numberedRequirementPattern.FindAllString(text, 8)) >= 3 {
		return false
	}
	return n < 180
}

func (w *WorkflowRunner) workflowDeliveryScopeText() string {
	if w == nil || w.runtime == nil || w.runtime.session == nil || strings.TrimSpace(w.runID) == "" {
		return ""
	}
	run, ok := w.runtime.session.WorkflowRun(w.runID)
	if !ok {
		return w.workflowRequestText()
	}
	for _, stage := range run.CompletedStages {
		if !strings.EqualFold(strings.TrimSpace(stage.Stage), "approve-plan") {
			continue
		}
		if v := strings.TrimSpace(stage.Outputs["delivery_scope"]); v != "" {
			return v
		}
	}
	if parsed := parseDeliveryScope(run.Request); parsed != "" {
		return parsed
	}
	return "mvp"
}

func workflowGraphShouldSeedPlanIntake(graph workflowGraph, stage workflowGraphStage) bool {
	return stage.Params["seed_plan_intake"] == "true"
}

func (w *WorkflowRunner) seedPlanIntakeAndDesign(result *schema.AgentResult) {
	if w == nil || result == nil {
		return
	}
	req := w.workflowRequestText()
	var extra strings.Builder
	if workflowRequestIsSparse(req) {
		if !strings.Contains(result.Output, "设计方案") {
			extra.WriteString("\n\n### 设计方案\n")
			extra.WriteString("使用者提示偏短。先给出可执行设计（页面、交互、视觉、验收），请确认是否可行；批准后再实现，不要直接写代码。\n")
		}
	} else if !strings.Contains(result.Output, "需求确认") {
		extra.WriteString("\n\n### 需求确认\n")
		extra.WriteString("使用者描述已经具体。实现前请确认范围、页面、验收命令与不做的事项；批准即视为需求确认。\n")
	}
	if !strings.Contains(result.Output, "交付档位") {
		extra.WriteString("\n\n### 交付档位\n")
		extra.WriteString("- MVP：可运行闭环（核心流程、测试通过、页面引用样式表）。\n")
		extra.WriteString("- 完整产品：在 MVP 之上补齐顶栏/导航/主区结构和可日常使用的界面。\n")
		extra.WriteString("请在确认关选择交付档位；未选择时按 MVP 实现。\n")
	}
	if workflowRequestRequiresWebStyling(req) && !strings.Contains(result.Output, "界面设计") {
		extra.WriteString("\n\n### 界面设计\n")
		extra.WriteString("- 页面结构：顶栏、导航、主区版心。\n")
		extra.WriteString("- 必须新增并在 HTML 中引用样式表（例如 `internal/portal/static/app.css` 经 `/static/app.css` 提供）。\n")
		extra.WriteString("- 表单、表格、状态标签、空状态要有样式；禁止只交付无样式的 `table border=1`。\n")
		extra.WriteString("- 视觉按业务场景（校园网运行中心用深色顶栏与可读中文排版），不要紫渐变营销落地页。\n")
	}
	if extra.Len() == 0 {
		return
	}
	result.Output = strings.TrimSpace(result.Output) + extra.String()
}
