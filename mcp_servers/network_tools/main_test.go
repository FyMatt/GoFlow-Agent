package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDeviceDiscoveryPlanRequiresAuthorization(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"name": "device_discovery_plan",
		"arguments": map[string]any{
			"host":             "192.0.2.10",
			"authorized_scope": false,
			"allowed_hosts":    []string{"192.0.2.10"},
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "authorized_scope") {
		t.Fatalf("expected authorization rejection, got %#v", result)
	}
}

func TestDeviceDiscoveryPlanReturnsReadOnlyPlan(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"name": "device_discovery_plan",
		"arguments": map[string]any{
			"device_id":        "edge-1",
			"host":             "192.0.2.10",
			"platform":         "cisco_ios",
			"connection":       "ssh",
			"credential_ref":   "vault://netops/edge-1",
			"authorized_scope": true,
			"allowed_hosts":    []string{"192.0.2.0/24"},
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected discovery plan, got %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["mode"] != "read_only_discovery" || payload["audit_note"] == "" {
		t.Fatalf("unexpected discovery payload: %#v", payload)
	}
	commands := payload["allowed_commands"].([]any)
	if len(commands) == 0 {
		t.Fatalf("expected default discovery commands, got %#v", payload)
	}
}

func TestDeviceCommandPlanRejectsCommandsOutsideAllowlist(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"name": "device_command_plan",
		"arguments": map[string]any{
			"host":             "router.example.com",
			"authorized_scope": true,
			"allowed_hosts":    []string{"router.example.com"},
			"allowed_commands": []string{"show version", "show interface *"},
			"commands":         []string{"show version", "configure terminal"},
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "outside allowlist") {
		t.Fatalf("expected allowlist rejection, got %#v", result)
	}
}

func TestDeviceCommandPlanAcceptsAllowlistedReadCommands(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"name": "device_command_plan",
		"arguments": map[string]any{
			"host":             "router.example.com",
			"authorized_scope": true,
			"allowed_hosts":    []string{"*.example.com"},
			"allowed_commands": []string{"show version", "show interface *"},
			"commands":         []string{"show interface status", "show version"},
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected command plan, got %#v", result)
	}
	content := result["content"].(string)
	if !strings.Contains(content, "requires_approval") || !strings.Contains(content, "raw_output_artifact_ref") {
		t.Fatalf("expected approval and evidence contract, got %s", content)
	}
}

func TestDeviceConfigDryRunNeverAppliesChanges(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"name": "device_config_dry_run",
		"arguments": map[string]any{
			"host":             "192.0.2.10",
			"authorized_scope": true,
			"allowed_hosts":    []string{"192.0.2.10"},
			"allowed_commands": []string{"interface *", "description *", "no shutdown"},
			"change_commands":  []string{"interface Gi0/1", "description uplink", "no shutdown"},
			"rollback_plan":    []string{"interface Gi0/1", "shutdown"},
			"dry_run":          true,
			"change_approved":  true,
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "change_approved must remain false") {
		t.Fatalf("expected dry-run approval rejection, got %#v", result)
	}

	params, _ = json.Marshal(map[string]any{
		"name": "device_config_dry_run",
		"arguments": map[string]any{
			"host":             "192.0.2.10",
			"authorized_scope": true,
			"allowed_hosts":    []string{"192.0.2.10"},
			"allowed_commands": []string{"interface *", "description *", "no shutdown"},
			"change_commands":  []string{"interface Gi0/1", "description uplink", "no shutdown"},
			"rollback_plan":    []string{"interface Gi0/1", "shutdown"},
			"dry_run":          true,
			"change_approved":  false,
		},
	})
	result = callTool(params)
	if result["is_error"].(bool) || !strings.Contains(result["content"].(string), "dry_run_only") {
		t.Fatalf("expected dry-run plan, got %#v", result)
	}
}

func TestBuiltinToolsIncludesNetworkDeviceTools(t *testing.T) {
	found := map[string]tool{}
	for _, tool := range builtinTools() {
		found[tool.Name] = tool
	}
	for _, name := range []string{"device_discovery_plan", "device_command_plan", "device_config_dry_run", "device_restconf_live_apply"} {
		tool, ok := found[name]
		if !ok {
			t.Fatalf("expected %s tool", name)
		}
		if tool.Kind != "network" || (!strings.Contains(tool.Description, "device") && !strings.Contains(tool.Description, "RESTCONF/API")) {
			t.Fatalf("unexpected tool metadata for %s: %#v", name, tool)
		}
	}
}

func TestDeviceRestconfLiveApplyRequiresReadinessEvidence(t *testing.T) {
	params, _ := json.Marshal(map[string]any{
		"name": "device_restconf_live_apply",
		"arguments": map[string]any{
			"host":              "router.example.com",
			"endpoint":          "https://router.example.com/restconf/data/native/interface",
			"method":            "PATCH",
			"credential_ref":    "env:GOFLOW_TEST_TOKEN",
			"approval_ref":      "approval-123",
			"change_ticket":     "CHG-123",
			"authorized_scope":  true,
			"allowed_hosts":     []string{"router.example.com"},
			"allowed_methods":   []string{"PATCH"},
			"allowed_paths":     []string{"/restconf/data/native/*"},
			"payload":           map[string]any{"interface": map[string]any{"description": "uplink"}},
			"rollback_plan":     []string{"restore previous interface description"},
			"dry_run_confirmed": false,
			"change_approved":   true,
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "dry_run_confirmed") {
		t.Fatalf("expected dry-run confirmation rejection, got %#v", result)
	}
}

func TestDeviceRestconfLiveApplyBlocksWhenLiveDisabled(t *testing.T) {
	t.Setenv("GOFLOW_NETWORK_TOOLS_ENABLE_LIVE", "")
	t.Setenv("GOFLOW_TEST_TOKEN", "test-token")
	params, _ := json.Marshal(map[string]any{
		"name": "device_restconf_live_apply",
		"arguments": map[string]any{
			"host":              "router.example.com",
			"endpoint":          "https://router.example.com/restconf/data/native/interface",
			"method":            "PATCH",
			"credential_ref":    "env:GOFLOW_TEST_TOKEN",
			"approval_ref":      "approval-123",
			"change_ticket":     "CHG-123",
			"authorized_scope":  true,
			"allowed_hosts":     []string{"router.example.com"},
			"allowed_methods":   []string{"PATCH"},
			"allowed_paths":     []string{"/restconf/data/native/*"},
			"payload":           map[string]any{"interface": map[string]any{"description": "uplink"}},
			"rollback_plan":     []string{"restore previous interface description"},
			"dry_run_confirmed": true,
			"change_approved":   true,
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected blocked audit payload, got %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["status"] != "blocked" || payload["live_enabled"] != false || !strings.Contains(payload["audit_note"].(string), "No HTTP request") {
		t.Fatalf("unexpected blocked payload: %#v", payload)
	}
}

func TestDeviceRestconfLiveApplyRejectsEndpointOutsideAllowlist(t *testing.T) {
	t.Setenv("GOFLOW_TEST_TOKEN", "test-token")
	params, _ := json.Marshal(map[string]any{
		"name": "device_restconf_live_apply",
		"arguments": map[string]any{
			"host":              "router.example.com",
			"endpoint":          "https://router.example.com/admin/delete",
			"method":            "PATCH",
			"credential_ref":    "env:GOFLOW_TEST_TOKEN",
			"approval_ref":      "approval-123",
			"change_ticket":     "CHG-123",
			"authorized_scope":  true,
			"allowed_hosts":     []string{"router.example.com"},
			"allowed_methods":   []string{"PATCH"},
			"allowed_paths":     []string{"/restconf/data/native/*"},
			"payload":           map[string]any{"interface": map[string]any{"description": "uplink"}},
			"rollback_plan":     []string{"restore previous interface description"},
			"dry_run_confirmed": true,
			"change_approved":   true,
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "outside allowed_paths") {
		t.Fatalf("expected path allowlist rejection, got %#v", result)
	}
}

func TestDeviceRestconfLiveApplySendsApprovedHTTPSRequest(t *testing.T) {
	t.Setenv("GOFLOW_NETWORK_TOOLS_ENABLE_LIVE", "1")
	t.Setenv("GOFLOW_TEST_TOKEN", "Bearer test-token")
	previousClient := restconfHTTPClient
	defer func() { restconfHTTPClient = previousClient }()

	var receivedMethod, receivedPath, receivedAuth, receivedApproval string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.EscapedPath()
		receivedAuth = r.Header.Get("Authorization")
		receivedApproval = r.Header.Get("X-GoFlow-Approval-Ref")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	restconfHTTPClient = server.Client()
	endpoint := server.URL + "/restconf/data/native/interface"
	hostPort := strings.TrimPrefix(server.URL, "https://")
	host := hostPort
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}

	params, _ := json.Marshal(map[string]any{
		"name": "device_restconf_live_apply",
		"arguments": map[string]any{
			"host":              host,
			"endpoint":          endpoint,
			"method":            "PATCH",
			"credential_ref":    "env:GOFLOW_TEST_TOKEN",
			"approval_ref":      "approval-123",
			"change_ticket":     "CHG-123",
			"authorized_scope":  true,
			"allowed_hosts":     []string{host},
			"allowed_methods":   []string{"PATCH"},
			"allowed_paths":     []string{"/restconf/data/native/*"},
			"payload":           map[string]any{"interface": map[string]any{"description": "uplink"}},
			"rollback_plan":     []string{"restore previous interface description"},
			"dry_run_confirmed": true,
			"change_approved":   true,
		},
	})
	result := callTool(params)
	if result["is_error"].(bool) {
		t.Fatalf("expected live apply result, got %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["status"] != "applied" || payload["http_status"].(float64) != 200 || payload["live_enabled"] != true {
		t.Fatalf("unexpected live apply payload: %#v", payload)
	}
	if receivedMethod != "PATCH" || receivedPath != "/restconf/data/native/interface" || receivedAuth != "Bearer test-token" || receivedApproval != "approval-123" {
		t.Fatalf("unexpected request: method=%s path=%s auth=%s approval=%s", receivedMethod, receivedPath, receivedAuth, receivedApproval)
	}
	if strings.Contains(result["content"].(string), "test-token") {
		t.Fatalf("tool result leaked authorization material: %s", result["content"].(string))
	}
}

func TestDeviceRestconfLiveApplyRejectsInvalidCredentialRef(t *testing.T) {
	os.Unsetenv("GOFLOW_TEST_TOKEN")
	params, _ := json.Marshal(map[string]any{
		"name": "device_restconf_live_apply",
		"arguments": map[string]any{
			"host":              "router.example.com",
			"endpoint":          "https://router.example.com/restconf/data/native/interface",
			"method":            "PATCH",
			"credential_ref":    "vault://secret/router",
			"approval_ref":      "approval-123",
			"change_ticket":     "CHG-123",
			"authorized_scope":  true,
			"allowed_hosts":     []string{"router.example.com"},
			"allowed_methods":   []string{"PATCH"},
			"allowed_paths":     []string{"/restconf/data/native/*"},
			"payload":           map[string]any{"interface": map[string]any{"description": "uplink"}},
			"rollback_plan":     []string{"restore previous interface description"},
			"dry_run_confirmed": true,
			"change_approved":   true,
		},
	})
	result := callTool(params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "credential_ref must use env") {
		t.Fatalf("expected credential ref rejection, got %#v", result)
	}
}
