package nativesandbox

import (
	"runtime"
	"sort"
	"strings"
)

// Feature reports one independently verifiable native isolation primitive.
type Feature struct {
	Name        string `json:"name"`
	Available   bool   `json:"available"`
	Implemented bool   `json:"implemented"`
	Enforced    bool   `json:"enforced"`
	Required    bool   `json:"required"`
}

// Report is the authoritative runtime view of the native sandbox boundary.
// StrongReady is true only when every required primitive is both implemented
// and enforced by the active executor.
type Report struct {
	Platform      string    `json:"platform"`
	Architecture  string    `json:"architecture"`
	StrongReady   bool      `json:"strong_ready"`
	Level         string    `json:"level"`
	Features      []Feature `json:"features"`
	Missing       []string  `json:"missing,omitempty"`
	RefusalReason string    `json:"refusal_reason,omitempty"`
}

// Probe returns a conservative report. Availability never implies that the
// primitive is enforced; enforcement is set only by a wired executor.
func Probe() Report {
	report := platformProbe()
	report.Platform = runtime.GOOS
	report.Architecture = runtime.GOARCH
	sort.Slice(report.Features, func(i, j int) bool { return report.Features[i].Name < report.Features[j].Name })
	for _, feature := range report.Features {
		if feature.Required && (!feature.Available || !feature.Implemented || !feature.Enforced) {
			report.Missing = append(report.Missing, feature.Name)
		}
	}
	report.StrongReady = len(report.Missing) == 0 && len(report.Features) > 0
	if report.StrongReady {
		report.Level = "strong"
		return report
	}
	report.Level = "controlled"
	report.RefusalReason = "当前系统的原生强沙箱尚未完整启用（缺少 " + strings.Join(report.Missing, "、") + "），未知代码和第三方连接已拒绝在真实工作区直接运行。"
	return report
}
