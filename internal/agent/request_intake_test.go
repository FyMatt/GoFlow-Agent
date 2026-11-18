package agent

import (
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestParseDeliveryScopeChoosesMVPOrProduct(t *testing.T) {
	if parseDeliveryScope("做个校园网 MVP") != "mvp" {
		t.Fatal("MVP wording must select mvp")
	}
	if parseDeliveryScope("按完整产品交付校园网门户") != "product" {
		t.Fatal("完整产品 wording must select product")
	}
	if normalizeDeliveryScope("完整产品") != "product" || normalizeDeliveryScope("MVP") != "mvp" {
		t.Fatal("select-field values must normalize")
	}
}

func TestWorkflowRequestIsSparseForShortPrompts(t *testing.T) {
	if !workflowRequestIsSparse("做个校园网") {
		t.Fatal("short prompt must be treated as sparse design-first intake")
	}
	detailed := "在空工作区实现校园网 Web 门户，使用 Go 标准库。\n1. 学生登录\n2. 报修工单隔离\n3. 管理员改状态\n4. 中文 SSR 与样式表\n5. go test 覆盖错误密码"
	if workflowRequestIsSparse(detailed) {
		t.Fatal("numbered detailed prompt must go through requirements confirmation")
	}
}

func TestSeedPlanIntakeAndDesignAsksBeforeImplement(t *testing.T) {
	state := session.New(4)
	shortID := state.StartWorkflowRun("complex-project-delivery", "做个校园网网页")
	short := &WorkflowRunner{runtime: &Runtime{session: state, cfg: &config.Config{}}, runID: shortID}
	shortResult := schema.AgentResult{Output: "### Delivery Plan\n先看仓库。"}
	short.seedPlanIntakeAndDesign(&shortResult)
	if !strings.Contains(shortResult.Output, "设计方案") || !strings.Contains(shortResult.Output, "是否可行") {
		t.Fatalf("sparse intake must ask the user to confirm a design: %s", shortResult.Output)
	}

	detailID := state.StartWorkflowRun("complex-project-delivery", "实现校园网 Web 门户 HTML：\n1. 登录\n2. 报修\n3. 管理员改状态\n页面为服务端渲染并引用 app.css")
	detail := &WorkflowRunner{runtime: &Runtime{session: state, cfg: &config.Config{}}, runID: detailID}
	detailResult := schema.AgentResult{Output: "### Delivery Plan\n按需求实施。"}
	detail.seedPlanIntakeAndDesign(&detailResult)
	if !strings.Contains(detailResult.Output, "需求确认") {
		t.Fatalf("detailed intake must confirm requirements: %s", detailResult.Output)
	}
	if !strings.Contains(detailResult.Output, "界面设计") || !strings.Contains(detailResult.Output, "app.css") {
		t.Fatalf("web requests must include visual design in the plan: %s", detailResult.Output)
	}
	if !strings.Contains(detailResult.Output, "交付档位") || !strings.Contains(detailResult.Output, "完整产品") {
		t.Fatalf("plan must let the user choose MVP vs product: %s", detailResult.Output)
	}
}
