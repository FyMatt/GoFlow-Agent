package api

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// TaskComplexityProfile is a deterministic task contract summary used before
// any model call. It is intentionally compact so the UI can explain why a
// request was routed to a direct Agent or a durable Workflow.
type TaskComplexityProfile struct {
	Score               int            `json:"score"`
	Level               string         `json:"level"`
	PrimaryDomain       string         `json:"primary_domain"`
	Domains             []string       `json:"domains,omitempty"`
	Signals             []string       `json:"signals,omitempty"`
	Deliverables        []string       `json:"deliverables,omitempty"`
	VerificationGates   []string       `json:"verification_gates,omitempty"`
	RiskControls        []string       `json:"risk_controls,omitempty"`
	EstimatedStageCount int            `json:"estimated_stage_count"`
	DomainScores        map[string]int `json:"domain_scores,omitempty"`
}

type taskSignalGroup struct {
	Label    string
	Weight   int
	Keywords []string
}

var taskComplexitySignals = []taskSignalGroup{
	{Label: "跨模块或跨层改动", Weight: 3, Keywords: []string{"跨模块", "多个模块", "多模块", "跨层", "前后端", "端到端", "cross-module", "multi-module", "end-to-end"}},
	{Label: "仓库级或架构级范围", Weight: 3, Keywords: []string{"全项目", "完整项目", "整个仓库", "架构重构", "架构升级", "系统重构", "repository-wide", "architecture migration"}},
	{Label: "迁移与兼容", Weight: 2, Keywords: []string{"迁移", "兼容", "升级", "数据转换", "schema", "migration", "backward compatibility"}},
	{Label: "并发或状态一致性", Weight: 2, Keywords: []string{"并发", "竞态", "幂等", "事务", "一致性", "状态机", "缓存失效", "concurrency", "race", "idempotent", "transaction"}},
	{Label: "多目标或批量执行", Weight: 3, Keywords: []string{"多主机", "多台", "批量", "多个目标", "多环境", "parallel hosts", "multi-host", "batch rollout"}},
	{Label: "性能或容量约束", Weight: 2, Keywords: []string{"性能优化", "延迟", "吞吐", "容量", "基准测试", "压测", "performance", "latency", "throughput", "benchmark"}},
	{Label: "多阶段工程交付", Weight: 2, Keywords: []string{"设计并实现", "实现并验证", "开发并部署", "审计并修复", "分析并整改", "build and deploy", "audit and fix"}},
	{Label: "受控主动安全验证", Weight: 5, Keywords: []string{"主动探测", "渗透测试", "红队", "利用验证", "active probe", "penetration test", "red team"}},
	{Label: "多来源证据综合", Weight: 3, Keywords: []string{"多个官方来源", "多个来源", "多来源", "交叉核验", "冲突结论", "冲突来源", "multiple sources", "cross-check sources"}},
	{Label: "模板保真与多格式转换", Weight: 3, Keywords: []string{"保留页眉页脚", "保留模板", "保持格式", "逐页视觉复核", "模板保真", "layout fidelity"}},
}

var taskDeliverableSignals = []taskSignalGroup{
	{Label: "源码或补丁", Keywords: []string{"源码", "代码修改", "补丁", "diff", "patch", "implementation"}},
	{Label: "自动化测试", Keywords: []string{"测试用例", "单元测试", "集成测试", "端到端测试", "隐藏测试", "regression test"}},
	{Label: "可安装或可运行产物", Keywords: []string{"安装包", "发布包", "可执行文件", "部署产物", "运行说明", "release artifact", "binary"}},
	{Label: "结构化报告", Keywords: []string{"审计报告", "研究报告", "巡检报告", "验收报告", "整改报告", "需求文档", "设计文档"}},
	{Label: "办公文档", Keywords: []string{"docx", "xlsx", "pptx", "pdf", "word", "excel", "ppt", "wps"}},
	{Label: "证据数据", Keywords: []string{"markdown", "json", "csv", "证据清单", "来源清单", "sbom"}},
}

var taskVerificationSignals = []taskSignalGroup{
	{Label: "测试", Keywords: []string{"测试", "test", "复测", "回归"}},
	{Label: "构建或运行", Keywords: []string{"构建", "编译", "运行验证", "build", "compile"}},
	{Label: "静态检查", Keywords: []string{"静态检查", "lint", "vet", "格式检查", "typecheck"}},
	{Label: "浏览器或视觉复核", Keywords: []string{"浏览器", "截图", "页面复核", "移动端", "深色", "浅色", "playwright"}},
	{Label: "独立质量复核", Keywords: []string{"独立验证", "独立复测", "质量门", "验收条件", "acceptance", "quality gate"}},
}

var taskRiskSignals = []taskSignalGroup{
	{Label: "审批", Keywords: []string{"审批", "批准后", "确认后", "approval"}},
	{Label: "备份与回滚", Keywords: []string{"备份", "回滚", "恢复", "rollback", "recovery"}},
	{Label: "远程或生产边界", Keywords: []string{"远程主机", "生产环境", "线上", "ssh", "production"}},
	{Label: "授权安全范围", Keywords: []string{"授权范围", "授权目标", "时间窗口", "停止条件", "authorized scope"}},
	{Label: "凭据或敏感数据", Keywords: []string{"凭据", "密钥", "私钥", "敏感数据", "credential", "secret"}},
}

func analyzeTaskComplexity(input string) TaskComplexityProfile {
	normalized := strings.ToLower(strings.TrimSpace(input))
	profile := TaskComplexityProfile{Level: "simple"}
	for _, group := range taskComplexitySignals {
		if containsAnyTerm(normalized, group.Keywords) {
			profile.Score += group.Weight
			profile.Signals = append(profile.Signals, group.Label)
		}
	}
	profile.Deliverables = matchedTaskSignalLabels(normalized, taskDeliverableSignals)
	profile.VerificationGates = matchedTaskSignalLabels(normalized, taskVerificationSignals)
	profile.RiskControls = matchedTaskSignalLabels(normalized, taskRiskSignals)
	if len(profile.Deliverables) > 1 {
		profile.Score += len(profile.Deliverables) - 1
		profile.Signals = append(profile.Signals, "多产物交付")
	}
	if len(profile.VerificationGates) > 1 {
		profile.Score += len(profile.VerificationGates) - 1
		profile.Signals = append(profile.Signals, "多重独立验证")
	}
	if len(profile.RiskControls) > 0 {
		profile.Score += len(profile.RiskControls)
	}
	// Format breadth is a generic complexity signal: no domain names live in the
	// kernel, so a new domain only adds resources, not branches here.
	if formatCount := countMatchedGroups(normalized, [][]string{{"word", "docx"}, {"excel", "xlsx"}, {"ppt", "pptx"}, {"pdf"}}); formatCount >= 2 {
		profile.Score += formatCount
		profile.Signals = append(profile.Signals, "多格式办公交付")
	}
	if formatCount := countMatchedGroups(normalized, [][]string{{"markdown"}, {"json"}, {"csv"}}); formatCount >= 2 {
		profile.Score += formatCount
		profile.Signals = append(profile.Signals, "多格式研究交付")
	}
	if countTaskClauses(input) >= 5 {
		profile.Score += 2
		profile.Signals = append(profile.Signals, "多项约束或验收条件")
	}
	if utf8.RuneCountInString(input) >= 320 {
		profile.Score++
		profile.Signals = append(profile.Signals, "长任务契约")
	}
	if profile.Score >= 6 || hasHardComplexitySignal(profile.Signals) {
		profile.Level = "complex"
	}
	profile.EstimatedStageCount = estimateTaskStages(profile)
	profile.Signals = uniqueSortedTaskStrings(profile.Signals)
	profile.Deliverables = uniqueSortedTaskStrings(profile.Deliverables)
	profile.VerificationGates = uniqueSortedTaskStrings(profile.VerificationGates)
	profile.RiskControls = uniqueSortedTaskStrings(profile.RiskControls)
	return profile
}

func countMatchedTerms(input string, keywords []string) int {
	count := 0
	for _, keyword := range keywords {
		if strings.Contains(input, strings.ToLower(strings.TrimSpace(keyword))) {
			count++
		}
	}
	return count
}

func containsAnyTerm(input string, keywords []string) bool {
	return countMatchedTerms(input, keywords) > 0
}

func matchedTaskSignalLabels(input string, groups []taskSignalGroup) []string {
	matched := make([]string, 0, len(groups))
	for _, group := range groups {
		if containsAnyTerm(input, group.Keywords) {
			matched = append(matched, group.Label)
		}
	}
	return matched
}

func countMatchedGroups(input string, groups [][]string) int {
	count := 0
	for _, group := range groups {
		if containsAnyTerm(input, group) {
			count++
		}
	}
	return count
}

func countTaskClauses(input string) int {
	parts := strings.FieldsFunc(input, func(r rune) bool {
		switch r {
		case '\n', ';', '；', '。':
			return true
		default:
			return false
		}
	})
	count := 0
	for _, part := range parts {
		if strings.TrimSpace(strings.TrimLeft(part, "-0123456789.、 ")) != "" {
			count++
		}
	}
	return count
}

func hasHardComplexitySignal(signals []string) bool {
	for _, signal := range signals {
		switch signal {
		case "仓库级或架构级范围", "跨模块或跨层改动", "多目标或批量执行", "多领域协作":
			return true
		}
	}
	return false
}

func estimateTaskStages(profile TaskComplexityProfile) int {
	stages := 1
	if profile.Level == "complex" {
		stages = 4
	}
	if len(profile.Deliverables) > 1 {
		stages++
	}
	if len(profile.VerificationGates) > 1 {
		stages++
	}
	if len(profile.RiskControls) > 0 {
		stages++
	}
	if len(profile.Domains) > 1 {
		stages += len(profile.Domains) - 1
	}
	if stages > 12 {
		return 12
	}
	return stages
}

func uniqueSortedTaskStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
