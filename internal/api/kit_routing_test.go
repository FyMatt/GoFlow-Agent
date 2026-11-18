package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/llm"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/scaffold"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
	"gopkg.in/yaml.v3"
)

func newKitRoutingHTTPRuntime(t *testing.T, dataRoot ...string) (*Server, *atomic.Int32, *apiTestMCP) {
	t.Helper()
	calls := &atomic.Int32{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "unexpected provider path", 404)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		const answer = "Lichen specimen observations reviewed. Collection notes are ready for curator review."
		if streaming, _ := request["stream"].(bool); streaming {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", answer)
			return
		}
		writeJSON(w, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(model.Close)
	provider := config.LLMConfig{Provider: "openai-compatible", BaseURL: model.URL, APIKey: "local-test", Model: "stub", Timeout: 5 * time.Second}
	profile := config.AgentProfile{Name: "Lichen curator", Provider: "local", Mode: "chat", Model: "stub", MaxIterations: 2, AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyConfirm}
	cfg := &config.Config{RuntimeHome: t.TempDir(), WorkspaceRoot: t.TempDir(), DefaultAgent: "chat", Session: config.SessionConfig{MaxHistory: 8}, Providers: map[string]config.LLMConfig{"local": provider}, Agents: map[string]config.AgentProfile{"chat": profile, "lichen-curator": profile}, MCP: []config.MCPServerRef{{Name: "field", Enabled: true, Isolation: "process_group"}}}
	if len(dataRoot) > 0 {
		cfg.DataRoot = dataRoot[0]
	}
	mcp := &apiTestMCP{tools: []schema.Tool{{Name: "read", Server: "field", Kind: "read"}, {Name: "write", Server: "field", Kind: "write"}}, health: map[string]string{"field": "ready"}}
	rt, err := agent.NewRuntime(cfg, map[string]interfaces.LLMClient{"local": llm.NewClient(provider)}, apiTestSkillManager{}, mcp, session.New(8), runtime.NewAuditLogger(false, false))
	if err != nil {
		t.Fatal(err)
	}
	rt.SetWorkspaceConfirmed(true)
	return NewServer(rt), calls, mcp
}

func routingTestKit() kitResourceDocument {
	return kitResourceDocument{Name: "lichen-field-pack", Agents: []string{"lichen-curator"}, Tools: []string{"field"}, Routing: &scaffold.KitRouting{Version: 1, MatchTerms: []string{"lichen specimen", "地衣标本"}, EntryAgent: "lichen-curator", RequiredTools: []string{"field/read"}}}
}

func kitRoutingRequest(t *testing.T, server *Server, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	return response
}

func installRoutingTestKit(t *testing.T, server *Server, doc kitResourceDocument) {
	t.Helper()
	response := kitRoutingRequest(t, server, http.MethodPut, "/api/resources/kits/"+doc.Name, doc)
	if response.Code != http.StatusOK && response.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", response.Code, response.Body.String())
	}
}

func TestKitRoutingUnknownDomainThroughPreflightAndRun(t *testing.T) {
	for _, mode := range []string{"sync", "stream", "background"} {
		t.Run(mode, func(t *testing.T) {
			server, calls, mcp := newKitRoutingHTTPRuntime(t)
			doc := routingTestKit()
			installRoutingTestKit(t, server, doc)
			req := map[string]any{"type": "auto", "input": "Review this LICHEN SPECIMEN collection", "permission_mode": "confirm"}
			response := kitRoutingRequest(t, server, http.MethodPost, "/api/runs/preflight", req)
			var preflight TaskPreflightResult
			if err := json.Unmarshal(response.Body.Bytes(), &preflight); err != nil {
				t.Fatal(err)
			}
			if !preflight.Valid || preflight.Target != "lichen-curator" || preflight.Domain != doc.Name {
				t.Fatalf("preflight: %s", response.Body.String())
			}
			if calls.Load() != 0 || mcp.calls != 0 {
				t.Fatal("preflight executed a model or tool")
			}
			path := "/api/runs"
			if mode == "stream" {
				path = "/api/runs/stream"
			}
			if mode == "background" {
				req["background"] = true
			}
			response = kitRoutingRequest(t, server, http.MethodPost, path, req)
			if response.Code != http.StatusOK && response.Code != http.StatusAccepted {
				t.Fatalf("run: %d %s", response.Code, response.Body.String())
			}
			deadline := time.Now().Add(5 * time.Second)
			var run session.AgentRunSnapshot
			for {
				runs := server.runtime.SessionSnapshot().AgentRuns
				if len(runs) > 0 {
					run = runs[0]
					if run.Status != "running" && run.Status != "queued" {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("run did not finish: %#v", run)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if calls.Load() == 0 || run.AgentID != "lichen-curator" || !reflect.DeepEqual(run.ResourcePins, preflight.ResourcePins) {
				t.Fatalf("actual selection/pins: %#v vs %#v calls=%d", run, preflight.ResourcePins, calls.Load())
			}
			if mcp.calls != 0 {
				t.Fatal("a capability declaration executed a tool")
			}
			// Editing the package changes future pins without rewriting the historical run.
			doc.Routing.Priority = 1
			installRoutingTestKit(t, server, doc)
			newResult := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
			if reflect.DeepEqual(newResult.ResourcePins, run.ResourcePins) {
				t.Fatal("routing priority did not affect pins")
			}
			stored, _ := server.runtime.AgentRun(run.ID)
			if !reflect.DeepEqual(stored.ResourcePins, run.ResourcePins) {
				t.Fatal("historical pins changed")
			}
		})
	}
}

func TestKitRoutingRejectsUnavailableAndForbiddenDependencies(t *testing.T) {
	for _, test := range []struct {
		name   string
		modify func(*kitResourceDocument)
		code   string
	}{
		{"unavailable tool", func(doc *kitResourceDocument) { doc.Routing.RequiredTools = []string{"field/absent"} }, "tool_missing"},
		{"permission boundary", func(doc *kitResourceDocument) { doc.Routing.RequiredTools = []string{"field/write"} }, "tool_not_allowed"},
		{"environment", func(doc *kitResourceDocument) { doc.RequiredEnv = []string{"GOFLOW_KIT_ROUTING_TEST_MISSING_ENV"} }, "kit_env_missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GOFLOW_KIT_ROUTING_TEST_MISSING_ENV", "")
			server, calls, mcp := newKitRoutingHTTPRuntime(t)
			doc := routingTestKit()
			test.modify(&doc)
			installRoutingTestKit(t, server, doc)
			validation := kitRoutingRequest(t, server, http.MethodPost, "/api/resources/kits/"+doc.Name+"/validate", doc)
			var contract kitValidationResult
			if err := json.Unmarshal(validation.Body.Bytes(), &contract); err != nil {
				t.Fatal(err)
			}
			if contract.Valid {
				t.Fatalf("capability validation accepted invalid dependency: %s", validation.Body.String())
			}
			for _, path := range []string{"/api/runs/preflight", "/api/runs", "/api/runs/stream"} {
				response := kitRoutingRequest(t, server, http.MethodPost, path, map[string]any{"type": "auto", "input": "lichen specimen"})
				var result TaskPreflightResult
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatalf("%s: %s", path, response.Body.String())
				}
				if result.Valid || !preflightIssueCodeExists(result.Issues, test.code) {
					t.Fatalf("%s: %s", path, response.Body.String())
				}
				if path != "/api/runs/preflight" && response.Code != http.StatusUnprocessableEntity {
					t.Fatalf("run status %d", response.Code)
				}
			}
			if calls.Load() != 0 || mcp.calls != 0 || len(server.runtime.SessionSnapshot().AgentRuns) != 0 {
				t.Fatal("rejected contract caused execution")
			}
		})
	}
}

func TestKitRoutingWorkflowEntryThroughActualRun(t *testing.T) {
	for _, mode := range []string{"sync", "stream", "background"} {
		t.Run(mode, func(t *testing.T) {
			server, calls, mcp := newKitRoutingHTTPRuntime(t)
			graph := agent.WorkflowGraphDocument{Name: "lichen-survey", Stages: []agent.WorkflowGraphStageDocument{{Name: "observe", Agent: "lichen-curator", Skill: "execution-plan", Params: map[string]string{"allowed_tools": "field/read"}}}}
			if err := server.runtime.WorkflowRunner().SaveWorkflowGraphDocument(graph.Name, graph); err != nil {
				t.Fatal(err)
			}
			doc := routingTestKit()
			doc.Workflows = []string{graph.Name}
			doc.Routing.EntryAgent, doc.Routing.EntryWorkflow = "", graph.Name
			installRoutingTestKit(t, server, doc)
			preflight := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
			if !preflight.Valid || preflight.Kind != "workflow" || preflight.Target != graph.Name {
				t.Fatalf("workflow preflight: %#v", preflight)
			}
			request := map[string]any{"type": "auto", "input": "lichen specimen", "approve": false, "permission_mode": "confirm", "reasoning_effort": "high"}
			path := "/api/runs"
			if mode == "stream" {
				path += "/stream"
			}
			if mode == "background" {
				request["background"] = true
			}
			response := kitRoutingRequest(t, server, http.MethodPost, path, request)
			if response.Code != http.StatusOK && response.Code != http.StatusAccepted {
				t.Fatalf("workflow run: %d %s", response.Code, response.Body.String())
			}
			deadline := time.Now().Add(5 * time.Second)
			var run session.WorkflowRunSnapshot
			for {
				runs := server.runtime.SessionSnapshot().WorkflowRuns
				if len(runs) > 0 {
					run = runs[0]
					if run.Status != "running" && run.Status != "queued" {
						break
					}
				}
				if time.Now().After(deadline) {
					t.Fatalf("workflow did not finish: %#v", run)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if calls.Load() == 0 || run.Name != graph.Name || !reflect.DeepEqual(run.ResourcePins, preflight.ResourcePins) {
				t.Fatalf("workflow selection/pins: %#v vs %#v", run, preflight.ResourcePins)
			}
			if run.PermissionMode != "confirm" || run.ReasoningEffort != "high" {
				t.Fatalf("request controls lost: %#v", run)
			}
			if mcp.calls != 0 {
				t.Fatal("capability requirements invoked a tool")
			}
		})
	}
}

func TestKitRoutingAcceptsTemplateEntryWithoutCatalogRunnableTag(t *testing.T) {
	server, _, _ := newKitRoutingHTTPRuntime(t)
	graph := agent.WorkflowGraphDocument{Name: "lichen-template-entry", Stages: []agent.WorkflowGraphStageDocument{{Name: "review", Agent: "lichen-curator", Skill: "execution-plan", Params: map[string]string{"allowed_tools": "field/read"}}}}
	if _, err := server.runtime.SaveWorkflowTemplateResource(graph.Name, agent.WorkflowTemplate{WorkflowTemplateSummary: agent.WorkflowTemplateSummary{Name: graph.Name, Title: "地衣复核模板"}, Graph: graph}); err != nil {
		t.Fatal(err)
	}
	doc := routingTestKit()
	doc.Workflows = []string{graph.Name}
	doc.Routing.EntryAgent, doc.Routing.EntryWorkflow = "", graph.Name
	installRoutingTestKit(t, server, doc)
	result := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
	if !result.Valid || result.Kind != "workflow" || result.Target != graph.Name {
		t.Fatalf("loadable companion template rejected: %#v", result)
	}
}

func TestKitRoutingLibraryToolsDoNotExpandEntryRequirements(t *testing.T) {
	for _, workflow := range []bool{false, true} {
		t.Run(fmt.Sprintf("workflow=%t", workflow), func(t *testing.T) {
			server, calls, mcp := newKitRoutingHTTPRuntime(t)
			doc := routingTestKit()
			// Other entries in a pack may use write tools; this entry is read-only.
			doc.Tools = []string{"field/read", "field/write"}
			if workflow {
				graph := agent.WorkflowGraphDocument{Name: "readonly-survey", Stages: []agent.WorkflowGraphStageDocument{{Name: "observe", Agent: "lichen-curator", Skill: "execution-plan", Params: map[string]string{"allowed_tools": "field/read"}}}}
				if err := server.runtime.WorkflowRunner().SaveWorkflowGraphDocument(graph.Name, graph); err != nil {
					t.Fatal(err)
				}
				doc.Workflows = []string{graph.Name}
				doc.Routing.EntryAgent, doc.Routing.EntryWorkflow = "", graph.Name
			}
			installRoutingTestKit(t, server, doc)
			result := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
			if !result.Valid {
				t.Fatalf("pack library incorrectly required at entry: %#v", result.Issues)
			}
			doc.Tools = append(doc.Tools, "field/optional-missing")
			installRoutingTestKit(t, server, doc)
			result = server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
			if !result.Valid || !preflightIssueCodeExists(result.Issues, "tool_missing") {
				t.Fatalf("missing library tool should warn without blocking: %#v", result.Issues)
			}
			// Promoting that tool to an entry requirement must still fail policy checks.
			doc.Routing.RequiredTools = append(doc.Routing.RequiredTools, "field/write")
			installRoutingTestKit(t, server, doc)
			result = server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
			if result.Valid {
				t.Fatal("required write tool bypassed read-only entry policy")
			}
			if calls.Load() != 0 || mcp.calls != 0 {
				t.Fatal("preflight invoked a model or tool")
			}
		})
	}
}

func TestKitRoutingWorkflowCapabilityRespectsStageBoundaries(t *testing.T) {
	for _, params := range []map[string]string{{"disable_tools": "true"}, {"allowed_tools": "field/write"}} {
		server, calls, _ := newKitRoutingHTTPRuntime(t)
		graph := agent.WorkflowGraphDocument{Name: "restricted-survey", Stages: []agent.WorkflowGraphStageDocument{{Name: "observe", Agent: "lichen-curator", Skill: "execution-plan", Params: params}}}
		if err := server.runtime.WorkflowRunner().SaveWorkflowGraphDocument(graph.Name, graph); err != nil {
			t.Fatal(err)
		}
		doc := routingTestKit()
		doc.Workflows = []string{graph.Name}
		doc.Routing.EntryAgent = ""
		doc.Routing.EntryWorkflow = graph.Name
		installRoutingTestKit(t, server, doc)
		response := kitRoutingRequest(t, server, http.MethodPost, "/api/runs", map[string]any{"type": "auto", "input": "lichen specimen", "approve": true})
		var result TaskPreflightResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusUnprocessableEntity || !preflightIssueCodeExists(result.Issues, "kit_capability_unavailable") {
			t.Fatalf("stage restriction ignored: %d %s", response.Code, response.Body.String())
		}
		if calls.Load() != 0 {
			t.Fatal("restricted workflow called LLM")
		}
	}
}

func TestKitRoutingWorkflowStartupApprovalStillRequired(t *testing.T) {
	server, calls, _ := newKitRoutingHTTPRuntime(t)
	graph := agent.WorkflowGraphDocument{Name: "approved-survey", Stages: []agent.WorkflowGraphStageDocument{{Name: "observe", Agent: "lichen-curator", Skill: "execution-plan", Execution: agent.WorkflowGraphExecutionContractDocument{Mode: "live", RiskLevel: "high", RequiresRollback: true}}}}
	if err := server.runtime.WorkflowRunner().SaveWorkflowGraphDocument(graph.Name, graph); err != nil {
		t.Fatal(err)
	}
	doc := routingTestKit()
	doc.Workflows = []string{graph.Name}
	doc.Routing.EntryAgent = ""
	doc.Routing.EntryWorkflow = graph.Name
	installRoutingTestKit(t, server, doc)
	response := kitRoutingRequest(t, server, http.MethodPost, "/api/runs", map[string]any{"type": "auto", "input": "lichen specimen", "approve": false})
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "workflow_startup_contract") {
		t.Fatalf("startup contract bypassed: %d %s", response.Code, response.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatal("unapproved workflow called LLM")
	}
}

func TestKitRoutingStrictMatchingAndExplicitChoices(t *testing.T) {
	server, _, _ := newKitRoutingHTTPRuntime(t)
	doc := routingTestKit()
	installRoutingTestKit(t, server, doc)
	// A complex task with no kit match must stop and ask the user to choose.
	noMatch := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "跨模块迁移缓存状态机；修改 API 与持久层；补充单元测试和集成测试；运行静态检查；生成发布包；失败时回滚"})
	if noMatch.Valid || !preflightIssueCodeExists(noMatch.Issues, "kit_routing_no_match") {
		t.Fatalf("strict no match: %#v", noMatch)
	}
	// A focused task with no kit match stays on the active agent instead of failing.
	simpleFallback := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "explain the last build log"})
	if !simpleFallback.Valid || simpleFallback.Kind != "agent" || simpleFallback.Target != "chat" {
		t.Fatalf("simple fallback: %#v", simpleFallback)
	}
	doc.Name = "second-pack"
	installRoutingTestKit(t, server, doc)
	ambiguous := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
	if ambiguous.Valid || !preflightIssueCodeExists(ambiguous.Issues, "kit_routing_ambiguous") {
		t.Fatalf("ambiguity: %#v", ambiguous)
	}
	explicit := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Agent: "chat", Input: "lichen specimen"})
	if !explicit.Valid || explicit.Target != "chat" {
		t.Fatalf("explicit selection lost: %#v", explicit)
	}
	response := kitRoutingRequest(t, server, http.MethodPost, "/api/runs", map[string]any{"type": "auto", "agent": "chat", "input": "lichen specimen"})
	if response.Code != http.StatusOK {
		t.Fatalf("explicit run: %d %s", response.Code, response.Body.String())
	}
	runs := server.runtime.SessionSnapshot().AgentRuns
	if len(runs) != 1 || runs[0].AgentID != "chat" {
		t.Fatalf("explicit run changed Agent: %#v", runs)
	}
	disabled := false
	doc.Routing.Enabled = &disabled
	installRoutingTestKit(t, server, doc)
	result := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "地衣标本"})
	if !result.Valid || result.Target != "lichen-curator" {
		t.Fatalf("disabled pack was selected: %#v", result)
	}
}

func TestKitRoutingHighestPrioritySelection(t *testing.T) {
	for _, test := range []struct {
		name                               string
		primaryPriority, referencePriority int
		input, target, pack                string
		ambiguous                          bool
	}{
		{"primary with references", 0, -10, "lichen specimen with reference research", "lichen-curator", "primary-pack", false},
		{"references alone", 0, -10, "reference research", "chat", "reference-pack", false},
		{"all negative", -20, -50, "lichen specimen with reference research", "lichen-curator", "primary-pack", false},
		{"higher reference priority", -100, 100, "lichen specimen with reference research", "chat", "reference-pack", false},
		{"equal maximum priorities", 0, 0, "lichen specimen with reference research", "", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, calls, _ := newKitRoutingHTTPRuntime(t)
			primary := routingTestKit()
			primary.Name, primary.Routing.Priority = "primary-pack", test.primaryPriority
			installRoutingTestKit(t, server, primary)
			reference := routingTestKit()
			reference.Name, reference.Agents = "reference-pack", []string{"chat"}
			reference.Routing.EntryAgent, reference.Routing.MatchTerms = "chat", []string{"reference research"}
			reference.Routing.Priority = test.referencePriority
			installRoutingTestKit(t, server, reference)
			// A lower-ranked match must not affect selection or maximum-priority ambiguity.
			lower := routingTestKit()
			lower.Name, lower.Routing.Priority = "lower-pack", -100
			installRoutingTestKit(t, server, lower)
			request := map[string]any{"type": "auto", "input": test.input}
			response := kitRoutingRequest(t, server, http.MethodPost, "/api/runs/preflight", request)
			var result TaskPreflightResult
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if test.ambiguous {
				if result.Valid || !preflightIssueCodeExists(result.Issues, "kit_routing_ambiguous") || strings.Contains(response.Body.String(), "lower-pack") {
					t.Fatalf("priority tie: %s", response.Body.String())
				}
			} else {
				if !result.Valid || result.Target != test.target || result.Domain != test.pack {
					t.Fatalf("priority selection: %s", response.Body.String())
				}
				for _, pin := range result.ResourcePins {
					if pin.Kind == "kit" && pin.Name != test.pack {
						t.Fatalf("unselected package pinned: %#v", pin)
					}
				}
			}
			response = kitRoutingRequest(t, server, http.MethodPost, "/api/runs", request)
			if test.ambiguous {
				if response.Code != http.StatusUnprocessableEntity || calls.Load() != 0 {
					t.Fatalf("ambiguous run: %d %s", response.Code, response.Body.String())
				}
				return
			}
			if response.Code != http.StatusOK {
				t.Fatalf("priority run: %d %s", response.Code, response.Body.String())
			}
			runs := server.runtime.SessionSnapshot().AgentRuns
			if len(runs) != 1 || runs[0].AgentID != test.target || !reflect.DeepEqual(runs[0].ResourcePins, result.ResourcePins) {
				t.Fatalf("actual selection/pins: %#v", runs)
			}
			// Priority cannot override an explicit user choice.
			explicit := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Agent: "chat", Input: test.input})
			if !explicit.Valid || explicit.Target != "chat" {
				t.Fatalf("explicit choice lost: %#v", explicit)
			}
		})
	}
}

func TestKitRoutingPriorityDoesNotBypassDependencyErrors(t *testing.T) {
	server, calls, _ := newKitRoutingHTTPRuntime(t)
	primary := routingTestKit()
	primary.Routing.RequiredTools = []string{"field/missing"}
	installRoutingTestKit(t, server, primary)
	fallback := routingTestKit()
	fallback.Name, fallback.Routing.Priority = "lower-priority-pack", -10
	installRoutingTestKit(t, server, fallback)
	response := kitRoutingRequest(t, server, http.MethodPost, "/api/runs", map[string]any{"type": "auto", "input": "lichen specimen"})
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "tool_missing") || calls.Load() != 0 {
		t.Fatalf("priority hid a dependency failure: %d %s", response.Code, response.Body.String())
	}
}

func TestKitRoutingMissingAndDisabledMCPDependencies(t *testing.T) {
	for _, missing := range []bool{true, false} {
		server, calls, mcp := newKitRoutingHTTPRuntime(t)
		doc := routingTestKit()
		code := "mcp_degraded"
		if missing {
			doc.Tools = []string{"absent"}
			code = "kit_mcp_missing"
		} else {
			mcp.health = map[string]string{"field": "degraded: missing dependency"}
		}
		installRoutingTestKit(t, server, doc)
		result := server.taskPreflight(t.Context(), taskPreflightRequest{Type: "auto", Input: "lichen specimen"})
		if result.Valid || !preflightIssueCodeExists(result.Issues, code) {
			t.Fatalf("missing MCP dependency was accepted: %#v", result)
		}
		if calls.Load() != 0 || mcp.calls != 0 {
			t.Fatal("dependency validation executed model/tool")
		}
	}
}

func TestKitRoutingInvalidReferencesRejectedBySaveAndImport(t *testing.T) {
	for _, modify := range []func(*kitResourceDocument){
		func(doc *kitResourceDocument) { doc.Routing.EntryAgent = "missing" },
		func(doc *kitResourceDocument) { doc.Agents = []string{"missing"}; doc.Routing.EntryAgent = "missing" },
		func(doc *kitResourceDocument) { doc.Routing.EntryWorkflow = "missing" },
		func(doc *kitResourceDocument) { doc.Routing.Version = 2 },
		func(doc *kitResourceDocument) { doc.Routing.Priority = -101 },
		func(doc *kitResourceDocument) { doc.Routing.Priority = 101 },
		func(doc *kitResourceDocument) { doc.Routing.EntryAgent = "../outside" },
	} {
		server, _, _ := newKitRoutingHTTPRuntime(t)
		doc := routingTestKit()
		modify(&doc)
		response := kitRoutingRequest(t, server, http.MethodPut, "/api/resources/kits/"+doc.Name, doc)
		if response.Code < 400 {
			t.Fatalf("invalid contract saved: %s", response.Body.String())
		}
		bundle := kitBundleDocument{Kind: kitBundleKind, Version: 1, Kit: doc}
		response = kitRoutingRequest(t, server, http.MethodPost, "/api/resources/kits/import", bundle)
		if response.Code < 400 && response.Code != http.StatusMultiStatus {
			t.Fatalf("invalid bundle imported: %s", response.Body.String())
		}
		if _, ok := server.kitResourceByName(doc.Name); ok {
			t.Fatal("invalid kit persisted")
		}
	}
}

func TestKitRoutingRoundTripBundleAndMaturitySummary(t *testing.T) {
	server, _, _ := newKitRoutingHTTPRuntime(t)
	doc := routingTestKit()
	doc.Routing.Priority = -10
	doc.VerticalPack = &scaffold.KitVerticalPack{Domain: "lichen", Maturity: "production-ready", Summary: "Curator guidance", SupportedTasks: []string{"survey"}, RequiredInputs: []string{"specimens"}, ToolBoundaries: []string{"read only"}, SafetyGates: []string{"user scope"}, QualityGates: []string{"review notes"}, EvidenceArtifacts: []string{"notes"}, PresentationGuidance: []string{"cite specimens"}, TokenStrategy: []string{"focused"}, ModelRoutes: scaffold.KitVerticalPackModelRoutes{Strong: []string{"local"}, Worker: []string{"local"}}}
	installRoutingTestKit(t, server, doc)
	for _, format := range []string{"json", "yaml"} {
		response := kitRoutingRequest(t, server, http.MethodGet, "/api/kits/"+doc.Name+"/export?format="+format, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("export: %d %s", response.Code, response.Body.String())
		}
		bundle, err := decodeKitBundleData(response.Body.Bytes(), response.Header().Get("Content-Type"))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(bundle.Kit.Routing, doc.Routing) {
			t.Fatalf("routing lost: %#v", bundle.Kit.Routing)
		}
		// Import the exported manifest into an existing, separately loaded runtime.
		other, _, _ := newKitRoutingHTTPRuntime(t)
		result := other.importKitBundle(bundle, true)
		if len(result.Errors) > 0 {
			t.Fatalf("import errors: %#v", result.Errors)
		}
		loaded, ok := other.kitResourceByName(doc.Name)
		if !ok || !reflect.DeepEqual(loaded.Routing, doc.Routing) {
			t.Fatalf("reloaded contract: %#v", loaded)
		}
	}
	summary := server.kitResourceList()[0]
	if summary.ClaimedMaturity != "production-ready" || summary.VerificationStatus != "unverified" {
		t.Fatalf("maturity claim promoted to verification: %#v", summary)
	}
	// The existing disk loader also retains contracts authored directly as YAML.
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(server.runtime.RuntimeHome(), "kits", doc.Name, "kit.yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, _ := server.kitResourceByName(doc.Name)
	if !reflect.DeepEqual(loaded.Routing, doc.Routing) {
		t.Fatal("disk roundtrip lost routing")
	}
}
