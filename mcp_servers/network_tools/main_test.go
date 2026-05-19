package main

import (
	"encoding/json"
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
	for _, name := range []string{"device_discovery_plan", "device_command_plan", "device_config_dry_run"} {
		tool, ok := found[name]
		if !ok {
			t.Fatalf("expected %s tool", name)
		}
		if tool.Kind != "network" || !strings.Contains(tool.Description, "device") {
			t.Fatalf("unexpected tool metadata for %s: %#v", name, tool)
		}
	}
}
