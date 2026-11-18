package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

func TestResourceCatalogHidesInternalMCPHelpers(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{
		{Name: "python_notes", Command: "python", Enabled: true},
		{Name: "skill_runner", Command: "go", Enabled: true},
		{Name: "web_tools", Command: "go", Enabled: true},
	})
	server := NewServer(runtimeRef)

	catalog := httptest.NewRecorder()
	server.ServeHTTP(catalog, httptest.NewRequest(http.MethodGet, "/api/resources?kind=mcp", nil))
	if catalog.Code != http.StatusOK {
		t.Fatalf("MCP catalog: status=%d body=%s", catalog.Code, catalog.Body.String())
	}
	body := catalog.Body.String()
	if strings.Contains(body, `"name":"python_notes"`) || strings.Contains(body, `"name":"skill_runner"`) {
		t.Fatalf("ordinary resource catalog exposed internal MCP helpers: %s", body)
	}
	if !strings.Contains(body, `"name":"web_tools"`) {
		t.Fatalf("ordinary resource catalog hid a user-facing MCP connection: %s", body)
	}
	var visible resourceCatalogResponse
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&visible); err != nil {
		t.Fatalf("decode visible MCP catalog: %v", err)
	}
	if len(visible.Families) != 1 || visible.Families[0].Count != 1 || visible.Counts.TotalResources != 1 || len(visible.Items) != 1 {
		t.Fatalf("visible MCP counts must exclude hidden helpers, got families=%#v counts=%#v items=%d", visible.Families, visible.Counts, len(visible.Items))
	}

	direct := httptest.NewRecorder()
	server.ServeHTTP(direct, httptest.NewRequest(http.MethodGet, "/api/resources/mcp", nil))
	if direct.Code != http.StatusOK || !strings.Contains(direct.Body.String(), `"name":"python_notes"`) {
		t.Fatalf("direct MCP compatibility endpoint should retain internal helpers: status=%d body=%s", direct.Code, direct.Body.String())
	}
}

func TestToolDependencyStatusDefersEnabledIdleMCP(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesAndMCP(t, session.New(8), "", nil, []config.MCPServerRef{
		{Name: "lazy_tools", Command: "go", Enabled: true},
		{Name: "disabled_tools", Command: "go", Enabled: false},
	})
	server := NewServer(runtimeRef)

	tests := []struct {
		name string
		want string
	}{
		{name: "web_tools/fetch_page_assets", want: "ready"},
		{name: "lazy_tools/read", want: "deferred"},
		{name: "disabled_tools/read", want: "disabled"},
		{name: "missing_tools/read", want: "missing"},
	}
	for _, test := range tests {
		if got := server.toolDependencyStatus(test.name); got != test.want {
			t.Fatalf("toolDependencyStatus(%q) = %q, want %q", test.name, got, test.want)
		}
	}

	dependencies := []resourceCatalogDependency{{Kind: "tool", Name: "lazy_tools/read", Required: true, Status: "deferred"}}
	if health, detail := catalogHealthFromDependencies(true, dependencies); health != "healthy" || detail != "" {
		t.Fatalf("deferred dependency health = %q, %q, want healthy", health, detail)
	}
	if issues := resourceCatalogDependencyIssues(dependencies); len(issues) != 0 {
		t.Fatalf("deferred dependency produced issues: %#v", issues)
	}
}

func TestDefaultResourceCatalogUsesCompleteChineseMetadata(t *testing.T) {
	wantFamilies := map[string]string{
		"agent": "智能体", "subagent": "子智能体", "mcp": "MCP 连接", "tool": "MCP 工具", "skill": "技能", "workflow": "工作流",
	}
	for _, capability := range helpResourceCapabilities() {
		want, ok := wantFamilies[capability.Kind]
		if !ok {
			continue
		}
		if capability.DisplayName != want || strings.TrimSpace(capability.Description) == "" || strings.TrimSpace(capability.ApplyMessage) == "" {
			t.Fatalf("resource family %s has incomplete metadata: %#v", capability.Kind, capability)
		}
	}
	if got := resourceCatalogFriendlyTitle("skill", "code-writing", "code-writing"); got != "代码实现" {
		t.Fatalf("code-writing title = %q", got)
	}
	if got := resourceCatalogFriendlyTitle("workflow", "skill-chain", "skill-chain"); got != "专业技能路由" {
		t.Fatalf("skill-chain title = %q", got)
	}
	if description := resourceCatalogMCPDescription("command_tools", "go", []string{"run", "./mcp_servers/command_tools"}); !strings.Contains(description, "结构化本地命令") {
		t.Fatalf("command MCP description is not user-facing: %q", description)
	}
}

func TestResourceCatalogExposesIndividualMCPToolsForWorkflowUse(t *testing.T) {
	runtimeRef := newAPITestRuntimeWithStateAndResponses(t, session.New(8), nil)
	server := NewServer(runtimeRef)

	catalog := httptest.NewRecorder()
	server.ServeHTTP(catalog, httptest.NewRequest(http.MethodGet, "/api/resources?kind=tool", nil))
	if catalog.Code != http.StatusOK {
		t.Fatalf("tool catalog: status=%d body=%s", catalog.Code, catalog.Body.String())
	}
	body := catalog.Body.String()
	for _, expected := range []string{`"kind":"tool"`, `"name":"stub/write_file"`, `"route":"#workflows"`, `"editable":false`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("individual MCP tool catalog is missing %s: %s", expected, body)
		}
	}

	options := httptest.NewRecorder()
	server.ServeHTTP(options, httptest.NewRequest(http.MethodGet, "/api/workflow-options", nil))
	if options.Code != http.StatusOK || !strings.Contains(options.Body.String(), `"tool_catalog":[{"name":"stub/write_file"`) || !strings.Contains(options.Body.String(), `"input_schema"`) {
		t.Fatalf("workflow options must expose tool descriptions and input schemas: status=%d body=%s", options.Code, options.Body.String())
	}
}
