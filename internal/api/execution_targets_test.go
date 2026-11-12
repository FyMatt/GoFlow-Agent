package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/executiontarget"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/sshhostkey"
)

const testSSHHostFingerprint = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestExecutionTargetCRUD(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	server := NewServer(runtimeRef)

	create := httptest.NewRequest(http.MethodPost, "/api/targets", strings.NewReader(`{
		"id":"edge-a",
		"kind":"ssh",
		"display_name":"边缘主机 A",
		"host":"192.0.2.10",
		"port":22,
		"user":"operator",
		"credential_ref":"secret://targets/edge-a/private_key",
		"host_key_sha256":"`+testSSHHostFingerprint+`",
		"allowed_commands":["uptime","df -h"],
		"enabled":true
	}`))
	created := httptest.NewRecorder()
	server.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create target: status=%d body=%s", created.Code, created.Body.String())
	}
	var doc executionTargetDocument
	if err := json.NewDecoder(created.Body).Decode(&doc); err != nil || doc.ID != "edge-a" || doc.CredentialRef == "" {
		t.Fatalf("decode created target: doc=%#v err=%v", doc, err)
	}

	list := httptest.NewRecorder()
	server.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/targets", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list target: status=%d body=%s", list.Code, list.Body.String())
	}
	var items []executionTargetDocument
	if err := json.NewDecoder(list.Body).Decode(&items); err != nil || len(items) != 2 || items[0].ID != executiontarget.LocalID || items[1].ID != "edge-a" {
		t.Fatalf("decode target list: items=%#v err=%v", items, err)
	}

	update := httptest.NewRequest(http.MethodPut, "/api/targets/edge-a", strings.NewReader(`{
		"kind":"ssh",
		"display_name":"边缘主机 A（只读）",
		"host":"192.0.2.10",
		"port":22,
		"user":"operator",
		"credential_ref":"secret://targets/edge-a/private_key",
		"host_key_sha256":"`+testSSHHostFingerprint+`",
		"allowed_commands":["uptime"],
		"enabled":false
	}`))
	updated := httptest.NewRecorder()
	server.ServeHTTP(updated, update)
	if updated.Code != http.StatusOK {
		t.Fatalf("update target: status=%d body=%s", updated.Code, updated.Body.String())
	}

	deleted := httptest.NewRecorder()
	server.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/api/targets/edge-a", nil))
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete target: status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestExecutionTargetCreateGeneratesIDAndDoesNotOverwriteExistingTarget(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	body := `{
		"kind":"ssh","display_name":"边缘主机","host":"192.0.2.15","port":2222,"user":"operator",
		"credential_ref":"secret://targets/edge/private_key","host_key_sha256":"` + testSSHHostFingerprint + `",
		"allowed_commands":["uptime"],"enabled":true
	}`
	first := httptest.NewRecorder()
	server.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/targets", strings.NewReader(body)))
	if first.Code != http.StatusCreated {
		t.Fatalf("create generated target: status=%d body=%s", first.Code, first.Body.String())
	}
	var created executionTargetDocument
	if err := json.NewDecoder(first.Body).Decode(&created); err != nil || created.ID != "ssh-192-0-2-15-2222-operator" {
		t.Fatalf("unexpected generated target %#v err=%v", created, err)
	}
	second := httptest.NewRecorder()
	server.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/targets", strings.NewReader(body)))
	if second.Code != http.StatusCreated {
		t.Fatalf("create second generated target: status=%d body=%s", second.Code, second.Body.String())
	}
	var duplicate executionTargetDocument
	if err := json.NewDecoder(second.Body).Decode(&duplicate); err != nil || duplicate.ID != "ssh-192-0-2-15-2222-operator-2" {
		t.Fatalf("unexpected duplicate target %#v err=%v", duplicate, err)
	}

	explicit := strings.Replace(body, `"kind":"ssh"`, `"id":"`+created.ID+`","kind":"ssh"`, 1)
	conflict := httptest.NewRecorder()
	server.ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/api/targets", strings.NewReader(explicit)))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("expected explicit id conflict, got %d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestExecutionTargetAPIRejectsLocalMutationAndPlainCredential(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	local := httptest.NewRecorder()
	server.ServeHTTP(local, httptest.NewRequest(http.MethodDelete, "/api/targets/local", nil))
	if local.Code != http.StatusBadRequest {
		t.Fatalf("expected local mutation rejection, got %d body=%s", local.Code, local.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/targets", strings.NewReader(`{
		"id":"edge-b","kind":"ssh","display_name":"Edge B","host":"192.0.2.11","port":22,
		"user":"operator","credential_ref":"PRIVATE KEY CONTENT","host_key_sha256":"`+testSSHHostFingerprint+`",
		"allowed_commands":["uptime"],"enabled":true
	}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "credential_ref") {
		t.Fatalf("expected plaintext credential rejection, got %d body=%s", response.Code, response.Body.String())
	}
}

func TestExecutionTargetHostKeyScanRequiresAuthorizationAndReturnsPublicKeys(t *testing.T) {
	server := NewServer(newAPITestRuntime(t))
	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/targets/scan-host-key", strings.NewReader(`{"host":"192.0.2.10","port":22}`)))
	if unauthorized.Code != http.StatusBadRequest || !strings.Contains(unauthorized.Body.String(), "authorized_scope") {
		t.Fatalf("expected authorization rejection, got %d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	server.sshHostKeyScanner = func(_ context.Context, host string, port int) ([]sshhostkey.Key, sshhostkey.CommandOutput, error) {
		if host != "192.0.2.10" || port != 2222 {
			t.Fatalf("unexpected scan address %s:%d", host, port)
		}
		return []sshhostkey.Key{{Host: "[192.0.2.10]:2222", KeyType: "ssh-ed25519", Fingerprint: testSSHHostFingerprint, KeyData: "must-not-leak"}}, sshhostkey.CommandOutput{Stderr: "scan notice"}, nil
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/targets/scan-host-key", strings.NewReader(`{
		"host":"192.0.2.10","port":2222,"authorized_scope":true,"timeout_seconds":5
	}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("scan target: status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "must-not-leak") {
		t.Fatalf("host key material leaked in response: %s", response.Body.String())
	}
	var result executionTargetHostKeyScanResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.Count != 1 || result.Keys[0].Fingerprint != testSSHHostFingerprint {
		t.Fatalf("decode scan result: result=%#v err=%v", result, err)
	}
}

func TestTaskPreflightAcceptsEnabledSSHExecutionTargetForOperationsAgent(t *testing.T) {
	operationsAgent := config.AgentProfile{
		Name:             "运维专家",
		TargetScope:      "host",
		Provider:         "primary",
		Mode:             "audit",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
		ToolPolicy:       config.ToolPolicyAllow,
	}
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesMCPCostRiskPolicyAndExtraAgents(t, session.New(8), "", nil, nil, config.CostControlConfig{}, config.ToolRiskPolicyConfig{}, map[string]config.AgentProfile{"operations-specialist": operationsAgent})
	if _, err := executiontarget.NewStore(runtimeRef.DataRoot()).Put(executiontarget.Target{
		ID: "edge-a", Kind: executiontarget.KindSSH, DisplayName: "边缘主机 A", Host: "192.0.2.10", Port: 22,
		User: "operator", CredentialRef: "secret://targets/edge-a/private_key", HostKeySHA256: testSSHHostFingerprint,
		AllowedCommands: []string{"uptime"}, Enabled: true,
	}); err != nil {
		t.Fatalf("store target: %v", err)
	}
	result := NewServer(runtimeRef).taskPreflight(t.Context(), taskPreflightRequest{
		Type: "agent", Agent: "operations-specialist", Input: "检查远程主机运行时间", TargetID: "edge-a",
	})
	if !result.Valid || result.ExecutionTarget.ID != "edge-a" || result.ExecutionTarget.Kind != executiontarget.KindSSH {
		t.Fatalf("expected valid SSH operations target, got %#v", result)
	}
}

func TestTaskPreflightRejectsDisabledSSHExecutionTarget(t *testing.T) {
	operationsAgent := config.AgentProfile{
		Name:             "运维专家",
		TargetScope:      "host",
		Provider:         "primary",
		Mode:             "audit",
		Model:            "test-model",
		MaxIterations:    2,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead},
		ToolPolicy:       config.ToolPolicyAllow,
	}
	runtimeRef := newAPITestRuntimeWithStateHomeResponsesMCPCostRiskPolicyAndExtraAgents(t, session.New(8), "", nil, nil, config.CostControlConfig{}, config.ToolRiskPolicyConfig{}, map[string]config.AgentProfile{"operations-specialist": operationsAgent})
	if _, err := executiontarget.NewStore(runtimeRef.DataRoot()).Put(executiontarget.Target{
		ID: "edge-disabled", Kind: executiontarget.KindSSH, DisplayName: "停用的边缘主机", Host: "192.0.2.20", Port: 22,
		User: "operator", CredentialRef: "secret://targets/edge-disabled/private_key", HostKeySHA256: testSSHHostFingerprint,
		AllowedCommands: []string{"uptime"}, Enabled: false,
	}); err != nil {
		t.Fatalf("store target: %v", err)
	}

	result := NewServer(runtimeRef).taskPreflight(t.Context(), taskPreflightRequest{
		Type: "agent", Agent: "operations-specialist", Input: "检查远程主机运行时间", TargetID: "edge-disabled",
	})
	if result.Valid || !preflightIssueCodeExists(result.Issues, "execution_target_disabled") {
		t.Fatalf("expected disabled target rejection, got %#v", result)
	}
	assertPreflightIssueUsesChinese(t, result.Issues, "execution_target_disabled")
}

func TestTaskPreflightRejectsSSHExecutionTargetForWorkspaceAgent(t *testing.T) {
	runtimeRef := newAPITestRuntime(t)
	if _, err := executiontarget.NewStore(runtimeRef.DataRoot()).Put(executiontarget.Target{
		ID: "edge-a", Kind: executiontarget.KindSSH, DisplayName: "边缘主机 A", Host: "192.0.2.10", Port: 22,
		User: "operator", CredentialRef: "secret://targets/edge-a/private_key", HostKeySHA256: testSSHHostFingerprint,
		AllowedCommands: []string{"uptime"}, Enabled: true,
	}); err != nil {
		t.Fatalf("store target: %v", err)
	}

	result := NewServer(runtimeRef).taskPreflight(t.Context(), taskPreflightRequest{
		Type: "agent", Agent: "planner", Input: "检查当前项目结构", TargetID: "edge-a",
	})
	if result.Valid || !preflightIssueCodeExists(result.Issues, "execution_target_not_applicable") {
		t.Fatalf("expected inapplicable target rejection, got %#v", result)
	}
	assertPreflightIssueUsesChinese(t, result.Issues, "execution_target_not_applicable")
}

func assertPreflightIssueUsesChinese(t *testing.T, issues []TaskPreflightIssue, code string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Code != code {
			continue
		}
		if !strings.Contains(issue.Message, "目标") || issue.Recommendation == "" {
			t.Fatalf("expected Chinese user-facing issue for %s, got %#v", code, issue)
		}
		return
	}
	t.Fatalf("missing preflight issue %s in %#v", code, issues)
}
