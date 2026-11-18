package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubagentResourceEndpointsAreSeparatedFromAgents(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	body := `{
		"id":"review-worker",
		"name":"Review Worker",
		"description":"Reviews a focused change and returns findings.",
		"system_prompt":"Review only the delegated scope.",
		"provider":"primary",
		"mode":"audit",
		"tool_policy":"allow",
		"allowed_tool_kinds":["read"],
		"max_iterations":4
	}`

	saveRequest := httptest.NewRequest(http.MethodPut, "/api/resources/subagents/review-worker", strings.NewReader(body))
	saveRequest.Header.Set("Content-Type", "application/json")
	saveResponse := httptest.NewRecorder()
	server.ServeHTTP(saveResponse, saveRequest)
	if saveResponse.Code != http.StatusOK || !strings.Contains(saveResponse.Body.String(), `"kind":"subagent"`) {
		t.Fatalf("expected saved subagent, got %d body=%s", saveResponse.Code, saveResponse.Body.String())
	}

	subagentsRequest := httptest.NewRequest(http.MethodGet, "/api/resources/subagents", nil)
	subagentsResponse := httptest.NewRecorder()
	server.ServeHTTP(subagentsResponse, subagentsRequest)
	if subagentsResponse.Code != http.StatusOK || !strings.Contains(subagentsResponse.Body.String(), `"id":"review-worker"`) {
		t.Fatalf("expected subagent collection entry, got %d body=%s", subagentsResponse.Code, subagentsResponse.Body.String())
	}

	agentItemRequest := httptest.NewRequest(http.MethodGet, "/api/resources/agents/review-worker", nil)
	agentItemResponse := httptest.NewRecorder()
	server.ServeHTTP(agentItemResponse, agentItemRequest)
	if agentItemResponse.Code != http.StatusNotFound {
		t.Fatalf("expected subagent to be hidden from agent endpoint, got %d body=%s", agentItemResponse.Code, agentItemResponse.Body.String())
	}

	subagentMainRequest := httptest.NewRequest(http.MethodGet, "/api/resources/subagents/chat", nil)
	subagentMainResponse := httptest.NewRecorder()
	server.ServeHTTP(subagentMainResponse, subagentMainRequest)
	if subagentMainResponse.Code != http.StatusNotFound {
		t.Fatalf("expected main agent to be hidden from subagent endpoint, got %d body=%s", subagentMainResponse.Code, subagentMainResponse.Body.String())
	}

	wrongDeleteRequest := httptest.NewRequest(http.MethodDelete, "/api/resources/agents/review-worker", nil)
	wrongDeleteResponse := httptest.NewRecorder()
	server.ServeHTTP(wrongDeleteResponse, wrongDeleteRequest)
	if wrongDeleteResponse.Code != http.StatusNotFound {
		t.Fatalf("expected cross-kind delete rejection, got %d body=%s", wrongDeleteResponse.Code, wrongDeleteResponse.Body.String())
	}
}

func TestSubagentCodexImportAndClaudeExport(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	source := `name = "reviewer"
description = "Reviews code for correctness and missing tests."
developer_instructions = "Review only the delegated scope and return evidence."
model = "gpt-5"
sandbox_mode = "read-only"
model_reasoning_effort = "high"
`
	importRequest := httptest.NewRequest(http.MethodPost, "/api/resources/subagents/import?format=codex", strings.NewReader(source))
	importResponse := httptest.NewRecorder()
	server.ServeHTTP(importResponse, importRequest)
	if importResponse.Code != http.StatusOK ||
		!strings.Contains(importResponse.Body.String(), `"kind":"subagent"`) ||
		!strings.Contains(importResponse.Body.String(), `"id":"reviewer"`) ||
		!strings.Contains(importResponse.Body.String(), "model_reasoning_effort") {
		t.Fatalf("expected Codex import with compatibility warning, got %d body=%s", importResponse.Code, importResponse.Body.String())
	}

	exportRequest := httptest.NewRequest(http.MethodGet, "/api/resources/subagents/reviewer/export?format=claude", nil)
	exportResponse := httptest.NewRecorder()
	server.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK ||
		!strings.Contains(exportResponse.Header().Get("Content-Disposition"), "reviewer.md") ||
		!strings.Contains(exportResponse.Body.String(), "name: reviewer") ||
		!strings.Contains(exportResponse.Body.String(), "Review only the delegated scope") {
		t.Fatalf("expected Claude export, got %d headers=%v body=%s", exportResponse.Code, exportResponse.Header(), exportResponse.Body.String())
	}
}

func TestSubagentClaudeImportAndCodexExport(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	source := `---
name: browser-reviewer
description: Reproduces browser defects and reports evidence
tools: Read, Glob, Grep, Bash
model: inherit
maxTurns: 8
skills:
  - browser-testing
isolation: worktree
---

Inspect the delegated browser flow. Do not edit unrelated files.
`
	importRequest := httptest.NewRequest(http.MethodPost, "/api/resources/subagents/import?format=claude", strings.NewReader(source))
	importResponse := httptest.NewRecorder()
	server.ServeHTTP(importResponse, importRequest)
	if importResponse.Code != http.StatusOK ||
		!strings.Contains(importResponse.Body.String(), `"id":"browser-reviewer"`) ||
		!strings.Contains(importResponse.Body.String(), `"max_iterations":8`) ||
		!strings.Contains(importResponse.Body.String(), "manual selection") ||
		!strings.Contains(importResponse.Body.String(), "isolation") {
		t.Fatalf("expected Claude import with compatibility warnings, got %d body=%s", importResponse.Code, importResponse.Body.String())
	}

	exportRequest := httptest.NewRequest(http.MethodGet, "/api/resources/subagents/browser-reviewer/export?format=codex", nil)
	exportResponse := httptest.NewRecorder()
	server.ServeHTTP(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK ||
		!strings.Contains(exportResponse.Header().Get("Content-Disposition"), "browser-reviewer.toml") ||
		!strings.Contains(exportResponse.Body.String(), `name = 'browser-reviewer'`) ||
		!strings.Contains(exportResponse.Body.String(), `sandbox_mode = 'workspace-write'`) {
		t.Fatalf("expected Codex export, got %d headers=%v body=%s", exportResponse.Code, exportResponse.Header(), exportResponse.Body.String())
	}
}
