package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/skill"
)

func TestFirstPartySkillSummariesRetainOperationalConstraints(t *testing.T) {
	for name, required := range map[string][]string{
		"code-writing":                    {"expected_sha256", `args=["test","./..."]`, "独立测试", "echo"},
		"code-audit":                      {"不猜默认输入文件", "已有证据", "流程契约优先"},
		"execution-plan":                  {"goflow-change-slices", "read 或 write", "已有确认不重复询问"},
		"controlled-operations":           {"不猜默认文件", "默认只读取证或干跑", "公开文档查询不授权扫描"},
		"office-document-production":      {"不能用文字说明替代交付", "不猜默认路径", "保留未授权范围"},
		"web-vulnerability-research":      {"不猜默认文件", "破坏性", "实际证据"},
		"vulnerability-research":          {"不猜默认文件", "不扩大操作范围", "不伪造验证"},
		"security-assessment":             {"Markdown", "未触发通知", "不自动修补"},
		"security-incident-investigation": {"Markdown", "未触发通知", "固定指纹", "read_file_range"},
	} {
		t.Run(name, func(t *testing.T) {
			parsed, err := skill.ParseFile(filepath.Join("..", "..", "skills", name, "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			injected, _ := skillPromptInjectedBudgetText(parsed)
			for _, text := range required {
				if !strings.Contains(injected, text) {
					t.Fatalf("constraint %q is in the source but missing from the actual model prompt", text)
				}
			}
		})
	}
}
