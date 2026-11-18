package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuiltinToolsAreReadOnlyAndComplete(t *testing.T) {
	found := make(map[string]tool)
	for _, item := range builtinTools() {
		found[item.Name] = item
		if strings.Contains(string(item.InputSchema), "program") || strings.Contains(string(item.InputSchema), "command") {
			t.Fatalf("host tool %s must not accept arbitrary commands: %s", item.Name, item.InputSchema)
		}
	}
	for _, name := range []string{"host_facts", "disk_usage", "list_ports", "list_processes", "service_status", "read_log_tail", "plan_windows_json_change"} {
		item, ok := found[name]
		if !ok {
			t.Fatalf("missing built-in host diagnostic %s", name)
		}
		if item.Kind != "read" {
			t.Fatalf("host diagnostic %s must be read-only, got %#v", name, item)
		}
	}
}

func TestHostFactsReturnsLocalReadOnlyEnvelope(t *testing.T) {
	t.Setenv("GOFLOW_TARGET_ID", "local")
	params, _ := json.Marshal(map[string]any{"name": "host_facts", "arguments": map[string]any{}})
	result := callTool(context.Background(), params)
	if result["is_error"].(bool) {
		t.Fatalf("host_facts failed: %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatalf("decode host facts: %v", err)
	}
	if payload["target_id"] != "local" || payload["read_only"] != true || payload["hostname"] == "" {
		t.Fatalf("unexpected host facts: %#v", payload)
	}
}

func TestHostToolsRejectUnsupportedTarget(t *testing.T) {
	t.Setenv("GOFLOW_TARGET_ID", "server-a")
	params, _ := json.Marshal(map[string]any{"name": "host_facts", "arguments": map[string]any{}})
	result := callTool(context.Background(), params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), `only support Target "local"`) {
		t.Fatalf("expected non-local Target rejection, got %#v", result)
	}
}

func TestManagedJSONToolRejectsMissingValueField(t *testing.T) {
	t.Setenv("GOFLOW_TARGET_ID", "local")
	params, _ := json.Marshal(map[string]any{
		"name": "set_managed_json_value",
		"arguments": map[string]any{
			"path": "/managed/app.json", "json_pointer": "/enabled",
			"expected_sha256": strings.Repeat("0", 64), "change_reason": "test",
			"backup_required": true, "post_check_required": true,
		},
	})
	result := callTool(context.Background(), params)
	if result["is_error"] != true || !strings.Contains(result["content"].(string), "value is required") {
		t.Fatalf("expected missing value rejection, got %#v", result)
	}
}

func TestParseDFReturnsByteCounts(t *testing.T) {
	items := parseDF("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 1000 250 750 25% /\n", 10)
	if len(items) != 1 || items[0]["size_bytes"] != int64(1024000) || items[0]["available_bytes"] != int64(768000) {
		t.Fatalf("unexpected df parse: %#v", items)
	}
}

func TestParseWindowsDiskJSONSupportsCIMAndPSDrive(t *testing.T) {
	cim, err := parseWindowsDiskJSON(`{"DeviceID":"C:","VolumeName":"System","FileSystem":"NTFS","Size":1000,"FreeSpace":400}`, 10)
	if err != nil || len(cim) != 1 || cim[0]["volume"] != "C:" || cim[0]["used_bytes"] != int64(600) {
		t.Fatalf("unexpected CIM disk parse: items=%#v err=%v", cim, err)
	}
	psdrive, err := parseWindowsDiskJSON(`{"Name":"D","Root":"D:\\","Used":250,"Free":750}`, 10)
	if err != nil || len(psdrive) != 1 || psdrive[0]["mount"] != `D:\` || psdrive[0]["size_bytes"] != int64(1000) {
		t.Fatalf("unexpected PSDrive disk parse: items=%#v err=%v", psdrive, err)
	}
}

func TestParseWindowsNetstatRespectsProtocolAndListeningFilter(t *testing.T) {
	raw := "TCP  0.0.0.0:80  0.0.0.0:0  LISTENING  42\r\nTCP  127.0.0.1:9000  127.0.0.1:5000  ESTABLISHED  43\r\nUDP  0.0.0.0:53  *:*  44\r\n"
	items := parseWindowsNetstat(raw, "tcp", true, 10)
	if len(items) != 1 || items[0]["pid"] != 42 || items[0]["state"] != "listening" || items[0]["local_port"] != "80" {
		t.Fatalf("unexpected netstat parse: %#v", items)
	}
}

func TestParseSSReturnsStructuredSocketEvidence(t *testing.T) {
	raw := `tcp LISTEN 0 4096 127.0.0.1:8080 0.0.0.0:* users:(("goflow",pid=123,fd=7))` + "\n"
	items := parseSS(raw, "any", true, 10)
	if len(items) != 1 || items[0]["pid"] != 123 || items[0]["local_address"] != "127.0.0.1:8080" || items[0]["local_host"] != "127.0.0.1" || items[0]["local_port"] != "8080" {
		t.Fatalf("unexpected ss parse: %#v", items)
	}
}

func TestProcessParsersRemainBounded(t *testing.T) {
	windowsItems := parseTasklistCSV(`"a.exe","10","Console","1","1,024 K"`+"\n"+`"b.exe","20","Console","1","2,048 K"`, 1)
	if len(windowsItems) != 1 || windowsItems[0]["pid"] != 10 {
		t.Fatalf("unexpected tasklist parse: %#v", windowsItems)
	}
	unixItems := parsePS("10 1 root S 00:10 /usr/bin/a\n20 1 app R 00:02 /usr/bin/b\n", 1)
	if len(unixItems) != 1 || unixItems[0]["ppid"] != 1 {
		t.Fatalf("unexpected ps parse: %#v", unixItems)
	}
}

func TestFilterProcessItemsSupportsExactPIDLookup(t *testing.T) {
	items := []map[string]any{{"pid": 10, "name": "a"}, {"pid": 20, "name": "b"}, {"pid": 30, "name": "c"}}
	filtered := filterProcessItems(items, []int{30, 10}, 10)
	if len(filtered) != 2 || numberValue(filtered[0]["pid"]) != 10 || numberValue(filtered[1]["pid"]) != 30 {
		t.Fatalf("unexpected filtered processes: %#v", filtered)
	}
	missing := missingProcessPIDs(filtered, []int{10, 20, 30, 40})
	if len(missing) != 2 || missing[0] != 20 || missing[1] != 40 {
		t.Fatalf("unexpected missing pids: %#v", missing)
	}
}

func TestProcessPIDsFromEnvelopeDeduplicatesAndBounds(t *testing.T) {
	envelope := map[string]any{"items": []map[string]any{{"pid": 30}, {"pid": 10}, {"pid": 30}, {"pid": 20}}}
	pids := processPIDsFromEnvelope(envelope, 2)
	if len(pids) != 2 || pids[0] != 10 || pids[1] != 20 {
		t.Fatalf("unexpected port pids: %#v", pids)
	}
	if count := portPIDCount(envelope); count != 3 {
		t.Fatalf("unexpected unique pid count: %d", count)
	}
}

func TestServiceParsersFilterNames(t *testing.T) {
	windows := "SERVICE_NAME: alpha\r\nDISPLAY_NAME: Alpha Service\r\n        STATE              : 4  RUNNING\r\n\r\nSERVICE_NAME: beta\r\n        STATE              : 1  STOPPED\r\n"
	items := parseWindowsServices(windows, map[string]struct{}{"alpha": {}}, 10)
	if len(items) != 1 || items[0]["name"] != "alpha" || items[0]["state"] != "running" {
		t.Fatalf("unexpected Windows service parse: %#v", items)
	}
	linux := "alpha.service loaded active running Alpha Service\nbeta.service loaded inactive dead Beta Service\n"
	items = parseSystemctl(linux, map[string]struct{}{"beta.service": {}}, 10)
	if len(items) != 1 || items[0]["state"] != "dead" {
		t.Fatalf("unexpected systemctl parse: %#v", items)
	}
}

func TestReadLogTailRequiresAllowedRootAndReturnsLastLines(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLOW_HOST_LOG_ROOTS", root)
	params, _ := json.Marshal(map[string]any{"name": "read_log_tail", "arguments": map[string]any{"source": "file", "path": path, "lines": 2}})
	result := callTool(context.Background(), params)
	if result["is_error"].(bool) {
		t.Fatalf("read_log_tail failed: %#v", result)
	}
	var payload struct {
		Lines     []string `json:"lines"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(result["content"].(string)), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Join(payload.Lines, ",") != "two,three" || !payload.Truncated {
		t.Fatalf("unexpected log tail: %#v", payload)
	}

	outside := filepath.Join(t.TempDir(), "secret.log")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	params, _ = json.Marshal(map[string]any{"name": "read_log_tail", "arguments": map[string]any{"source": "file", "path": outside}})
	result = callTool(context.Background(), params)
	if !result["is_error"].(bool) || !strings.Contains(result["content"].(string), "outside allowed") {
		t.Fatalf("expected log root rejection, got %#v", result)
	}
}

func TestCollectorsReportUnavailableWithoutFailingTask(t *testing.T) {
	original := runDiagnosticCommand
	runDiagnosticCommand = func(context.Context, string, ...string) (commandOutput, error) {
		return commandOutput{}, os.ErrNotExist
	}
	t.Cleanup(func() { runDiagnosticCommand = original })

	if runtime.GOOS != "windows" {
		payload, err := collectPorts(context.Background(), diagnosticInput{})
		if err != nil || payload["status"] != "unavailable" {
			t.Fatalf("expected unavailable ports result, got payload=%#v err=%v", payload, err)
		}
	}
}
