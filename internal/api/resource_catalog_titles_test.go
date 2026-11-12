package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type catalogTitleSkills struct {
	apiTestSkillManager
	items []schema.Skill
}

func (m catalogTitleSkills) List() []schema.Skill { return m.items }

func TestResourceCatalogPrefersResourceProvidedTitles(t *testing.T) {
	skills := catalogTitleSkills{items: []schema.Skill{
		{Name: "lichen-observation", Metadata: map[string]string{"title": " 地衣观察记录 ", "display_name": "次选标题"}},
		{Name: "lichen-display-name", Metadata: map[string]string{"title": " \t", "display_name": " 标本来源核验 "}},
		{Name: "code-writing", Metadata: map[string]string{"title": " 自定义代码实施 "}},
		{Name: "execution-plan", Metadata: map[string]string{"title": " ", "display_name": "\t"}},
		{Name: "lichen-without-title"},
	}}
	cfg := &config.Config{RuntimeHome: t.TempDir(), WorkspaceRoot: t.TempDir(), DefaultAgent: "chat", Agents: map[string]config.AgentProfile{"chat": {Name: "Chat", Provider: "local", Mode: "chat"}}, Providers: map[string]config.LLMConfig{"local": {Provider: "openai-compatible", BaseURL: "http://localhost:9999/v1", Model: "stub"}}}
	rt, err := agent.NewRuntime(cfg, map[string]interfaces.LLMClient{"local": &apiTestLLM{}}, skills, &apiTestMCP{}, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(rt)
	for _, fixture := range []struct{ name, title string }{
		{"lichen-review", " 地衣记录复核流程 "},
		{"plan-fix-audit", " 本地规划审阅流程 "},
	} {
		graph := agent.WorkflowGraphDocument{Name: fixture.name, Stages: []agent.WorkflowGraphStageDocument{{Name: "review", Agent: "chat", Skill: "lichen-observation"}}}
		if err := rt.WorkflowRunner().SaveWorkflowGraphDocument(graph.Name, graph); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.SaveWorkflowTemplateResource(graph.Name, agent.WorkflowTemplate{WorkflowTemplateSummary: agent.WorkflowTemplateSummary{Name: graph.Name, Title: fixture.title}, Graph: graph}); err != nil {
			t.Fatal(err)
		}
	}
	graph := agent.WorkflowGraphDocument{Name: "lichen-no-template", Stages: []agent.WorkflowGraphStageDocument{{Name: "review", Agent: "chat", Skill: "lichen-observation"}}}
	if err := rt.WorkflowRunner().SaveWorkflowGraphDocument(graph.Name, graph); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/resources?kind=skill,workflow", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("catalog: %d %s", response.Code, response.Body.String())
	}
	var catalog resourceCatalogResponse
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	titles := make(map[string]string)
	for _, item := range catalog.Items {
		titles[item.Kind+":"+item.Name] = item.Title
	}
	for key, want := range map[string]string{
		"skill:lichen-observation":    "地衣观察记录",
		"skill:lichen-display-name":   "标本来源核验",
		"skill:code-writing":          "自定义代码实施",
		"skill:execution-plan":        "执行规划",
		"skill:lichen-without-title":  "lichen-without-title",
		"workflow:lichen-review":      "地衣记录复核流程",
		"workflow:plan-fix-audit":     "本地规划审阅流程",
		"workflow:lichen-no-template": "lichen-no-template",
	} {
		if got := titles[key]; got != want {
			t.Errorf("%s title = %q, want %q", key, got, want)
		}
	}
}

func TestResourceCatalogUnknownMCPKeepsIDTitle(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	item := server.mcpCatalogItem(mcpResourceDocument{Name: "lichen-reader", Command: "unused-local-stub", Enabled: true}, map[string]string{"lichen-reader": "ready"})
	if item.Title != "lichen-reader" {
		t.Fatalf("MCP without a title field must retain its ID: %#v", item)
	}
}
