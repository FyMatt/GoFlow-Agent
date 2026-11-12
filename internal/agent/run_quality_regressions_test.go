package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestSummaryKeepsUTF8(t *testing.T) {
	text := truncateSummary(strings.Repeat("中文任务输出", 40))
	if !utf8.ValidString(text) || strings.ContainsRune(text, '\ufffd') || !strings.HasSuffix(text, "...") {
		t.Fatalf("broken summary: %q", text)
	}
}

func TestOperationalPreviewDoesNotClaimFileChanges(t *testing.T) {
	output := "预览服务已启动，未修改任何项目文件。访问 http://127.0.0.1:5003/，正常引用 `/static/style.css`。"
	tools := []schema.ToolResult{{ToolName: "start_process", Content: `{"running":true}`}, {ToolName: "browser_snapshot", Content: `{"url":"http://127.0.0.1:5003/","dom_bytes":3000}`}}
	_, changes, _ := buildStructuredArtifacts(output, tools, "fix")
	if len(changes) != 0 {
		t.Fatalf("preview was misreported as file modification: %#v", changes)
	}
	if ResultClaimsFileChanges(schema.AgentResult{Output: output, Changes: []schema.Change{{Files: []string{"//127.0.0.1", "`/static/style.css`"}}}, ToolResults: tools}) {
		t.Fatal("legacy preview claims must be corrected in derived delivery facts")
	}
	for _, text := range []string{"No files were changed; but modified src/app.py", "未修改任何项目文件；实际修改 src/app.py"} {
		_, changes, _ := buildStructuredArtifacts(text, tools, "fix")
		if len(changes) == 0 {
			t.Fatalf("contradictory positive claims must still require diff evidence: %s", text)
		}
	}
	files := extractMentionedFiles("http://127.0.0.1:5003/ https://example.test/app.py `src/app.py`")
	if len(files) != 1 || files[0] != "src/app.py" {
		t.Fatalf("URLs are not changed files: %#v", files)
	}
}

func TestVerifierStatusUsesExplicitVerdict(t *testing.T) {
	for _, test := range []struct{ text, want string }{
		{"Status: pass\nChecked: preview\nRisks: none\nNext: none", "passed"},
		{"Status: warn\nRisks: missing test", "warning"},
		{"Status: pass\nStatus: fail", "failed"},
		{"Status: pass\nStatus: warn", "warning"},
		{"tests failed", "failed"},
	} {
		if got := VerifierStatus(test.text); got != test.want {
			t.Fatalf("verdict %q: got=%s want=%s", test.text, got, test.want)
		}
	}
}

func TestNoFileChangeStatementsIncludeOperationLists(t *testing.T) {
	for _, output := range []string{
		"没有修改、创建或删除任何项目文件。",
		"未执行任何文件修改操作。",
		"没有进行文件改动操作。",
		"未创建或删除任何文件。",
		"没有修改和更新任何项目文件。",
	} {
		t.Run(output, func(t *testing.T) {
			_, changes, _ := buildStructuredArtifacts(output, nil, "fix")
			if len(changes) != 0 {
				t.Fatalf("no-change statement became a file modification: %#v", changes)
			}
			legacy := schema.AgentResult{Output: output, Changes: []schema.Change{{Summary: output}}}
			if ResultClaimsFileChanges(legacy) {
				t.Fatal("legacy no-change statement must not require an invented diff")
			}
			withAudit := legacy
			auditDetail := "Status: warn\nChecked: 未能独立确认文件未被修改，仅发现 HTML 引用 /static/style.css。"
			withAudit.Output += "\n\n" + formatVerifierBlock(auditDetail)
			withAudit.Verification = []schema.Verification{{Kind: "verifier", Status: "warning", Detail: auditDetail}}
			if ResultClaimsFileChanges(withAudit) {
				t.Fatal("verifier discussion of files must not become an implementation change claim")
			}
			withAudit.Verification = nil
			if !ResultClaimsFileChanges(withAudit) {
				t.Fatal("a model-authored verifier heading must not hide unsupported modification claims")
			}
			legacy.Output += "但实际修改了 src/app.py。"
			if !ResultClaimsFileChanges(legacy) {
				t.Fatal("contradictory file modification must still require diff evidence")
			}
		})
	}
}

func TestPlanOnlyWritesRequestedNewTest(t *testing.T) {
	text := "## Files Or Modules\n- app.py：被测 Flask 应用\n- templates/base.html：CSS 引用及目标结构\n- static/style.css：静态资源响应验证目标\n- tests/test_ui_smoke.py：唯一新增文件\n## Verification Commands\npython -m py_compile app.py"
	paths := plannedWritePathsFromText(text)
	if len(paths) != 1 || paths[0] != "tests/test_ui_smoke.py" {
		t.Fatalf("read dependencies became write targets: %#v", paths)
	}
	mixed := plannedWritePathsFromText("## Target Files\n- app.py：修复路由\n- tests/test_ui_smoke.py：新增测试")
	if len(mixed) != 2 {
		t.Fatalf("new tests must not hide intended implementation changes: %#v", mixed)
	}
}

func TestContinuationPreservesFileBodyBeyondSummary(t *testing.T) {
	content := `{"relative_path":"app.py","sha256":"abc","content":"` + strings.Repeat("x", 200) + `important_saved_function"}`
	got := workflowGraphPreservedToolObservations([]schema.ToolResult{{ToolName: "read_file", Content: content}})
	if !strings.Contains(got, "important_saved_function") || !strings.Contains(got, "app.py") {
		t.Fatalf("continuation lost actual file observations: %s", got)
	}
}
