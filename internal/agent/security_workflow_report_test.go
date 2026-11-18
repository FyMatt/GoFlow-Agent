package agent

import (
	"os"
	"strings"
	"testing"
)

func TestSecurityWorkflowSynchronizesDeliverableAfterVerification(t *testing.T) {
	data, err := os.ReadFile("templates/workflows/security-audit-evidence-gate.yaml")
	if err != nil {
		t.Fatalf("read security workflow: %v", err)
	}
	text := string(data)
	for _, required := range []string{
		"写入报告前必须实际执行批准计划中适用于目标项目的格式、测试和静态检查命令",
		"报告写入后必须 read_file 读回一次",
		"读取、更新、再读回该明确路径的报告",
		"不得保留“待执行测试”等过期结论",
		"不能在完整目标 os.IsNotExist 时直接放行",
		"逐级 Lstat/EvalSymlinks 已存在组件",
		"verification 已明确通过且 review 只缺报告或证据同步，禁止再次修改实现源码",
		"禁止保留未定义 helper、占位函数、伪代码",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("security workflow is missing report synchronization rule %q", required)
		}
	}
	for _, fixture := range []string{"internal/auth/auth.go", "internal/filesafe/path.go", "internal/request/header.go", "internal/audit/redact.go", "cmd/secureapp/main.go", "deliverables/security-report.md"} {
		if strings.Contains(text, fixture) {
			t.Fatalf("security workflow hardcodes fixture target %q", fixture)
		}
	}
}
