package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDomainClosedLoopUsesComplexProjectDelivery(t *testing.T) {
	template, ok := (&WorkflowRunner{}).WorkflowTemplate("complex-project-delivery")
	if !ok {
		t.Fatal("missing complex-project-delivery")
	}
	hasApprove := false
	hasImplement := false
	hasVerify := false
	hasAudit := false
	for _, stage := range template.Graph.Stages {
		switch strings.TrimSpace(stage.Name) {
		case "approve-plan":
			hasApprove = true
			if !strings.Contains(stage.Params["fields_json"], "delivery_scope") {
				t.Fatalf("approve-plan must expose delivery_scope, got %#v", stage.Params)
			}
		case "implement":
			hasImplement = true
		case "verify":
			hasVerify = true
		case "audit":
			hasAudit = true
		}
	}
	if !hasApprove || !hasImplement || !hasVerify || !hasAudit {
		t.Fatalf("complex-project-delivery missing closed-loop stages approve=%v implement=%v verify=%v audit=%v", hasApprove, hasImplement, hasVerify, hasAudit)
	}
}

func TestDomainRequestsTriggerEngineeringGates(t *testing.T) {
	crypto := "请开发一个密码学学习网站，覆盖 AES、RSA、Base64 等常见加密解密学习流程。需要登录后的实验页、算法说明、输入输出区和结果展示，页面必须引用真实样式表，并补单元测试，可用 go test ./... 验证登录隔离与编解码正确性。"
	if !workflowRequestRequiresWebStyling(crypto) {
		t.Fatal("expected crypto learning site to require web styling")
	}
	if !workflowRequestRequiresTestCoverage(crypto) {
		t.Fatal("expected crypto learning site to require tests")
	}

	ops := "整理一份本地运维学习资料，包含巡检清单、回滚步骤和可运行的检查脚本，并补单元测试。"
	if !workflowRequestRequiresTestCoverage(ops) {
		t.Fatal("expected ops study request to require tests")
	}

	pentest := "做一个授权范围内的渗透小工具，只处理用户明确授权的本地样本，不要碰 DVWA，并补单元测试。"
	if !workflowRequestRequiresTestCoverage(pentest) {
		t.Fatal("expected authorized pentest helper to require tests")
	}

	for _, name := range []string{"crypto-learning", "ops-study", "authorized-pentest-helper"} {
		path := filepath.Join("..", "..", "skills", name, "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read skill %s: %v", name, err)
		}
		text := string(data)
		if !strings.Contains(text, "complex-project-delivery") && !strings.Contains(text, name) {
			t.Fatalf("skill %s should recommend a closed-loop workflow", name)
		}
		if name == "authorized-pentest-helper" && !strings.Contains(strings.ToLower(text), "dvwa") {
			t.Fatal("authorized pentest helper must keep DVWA out of default scope")
		}
	}
}
