package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/notification"
)

func TestServeLocalCollectorAndApprovalMetadata(t *testing.T) {
	root := t.TempDir()
	publisher := notification.Publisher{DataRoot: root}
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"publish_event","arguments":{"severity":"info","title":"Completed","summary":"See evidence","evidence_refs":["artifact://report"]}}}
`)
	var output bytes.Buffer
	if err := serve(t.Context(), input, &output, publisher); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var listed struct {
		Result struct {
			Tools []tool `json:"tools"`
		} `json:"result"`
	}
	if err := decoder.Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Result.Tools) != 1 || listed.Result.Tools[0].Kind != "network" || listed.Result.Tools[0].Name != "publish_event" {
		t.Fatalf("approval metadata: %+v", listed)
	}
	var published struct {
		Result struct {
			Content string `json:"content"`
			IsError bool   `json:"is_error"`
		} `json:"result"`
	}
	if err := decoder.Decode(&published); err != nil {
		t.Fatal(err)
	}
	var event notification.Event
	if err := json.Unmarshal([]byte(published.Result.Content), &event); err != nil || published.Result.IsError || event.EventID == "" || event.ChannelID != "local" || event.DeliveryStatus != "stored" {
		t.Fatalf("local result: %+v, %v", published, err)
	}
	if _, err := os.Stat(filepath.Join(root, "notifications", "outbox", event.EventID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestCallToolRejectsDestinationAndCredentialArguments(t *testing.T) {
	for _, extra := range []string{`"url":"http://127.0.0.1"`, `"credential_ref":"secret://test/token"`, `"allow_private":true`, `"token":"SECRET"`, `"data_root":"../outside"`, `"event_id":"../outside"`, `"outbox_path":"../outside"`} {
		params := json.RawMessage(`{"name":"publish_event","arguments":{"severity":"info","title":"x","summary":"y",` + extra + `}}`)
		result := callTool(t.Context(), params, notification.Publisher{DataRoot: t.TempDir()})
		if result["is_error"] != true || strings.Contains(result["content"].(string), "SECRET") {
			t.Fatalf("invalid arguments: %+v", result)
		}
	}
}

func TestMissingChannelIsToolErrorWithPersistentID(t *testing.T) {
	root := t.TempDir()
	result := callTool(t.Context(), json.RawMessage(`{"name":"publish_event","arguments":{"channel_id":"missing","severity":"error","title":"x","summary":"y"}}`), notification.Publisher{DataRoot: root})
	var event notification.Event
	if err := json.Unmarshal([]byte(result["content"].(string)), &event); err != nil || result["is_error"] != true || event.DeliveryStatus != "failed" || event.EventID == "" {
		t.Fatalf("missing channel result: %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "notifications", "outbox", event.EventID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredDataRoot(t *testing.T) {
	runtimeHome := t.TempDir()
	t.Setenv("GOFLOW_DATA_ROOT", "")
	if got := config.ResolveDataRoot(runtimeHome); got != runtimeHome {
		t.Fatalf("default data root: %s", got)
	}
	t.Setenv("GOFLOW_DATA_ROOT", "saved-data")
	if got := config.ResolveDataRoot(runtimeHome); got != filepath.Join(runtimeHome, "saved-data") {
		t.Fatalf("relative data root: %s", got)
	}
	absolute := t.TempDir()
	t.Setenv("GOFLOW_DATA_ROOT", absolute)
	if got := config.ResolveDataRoot(runtimeHome); got != absolute {
		t.Fatalf("absolute data root: %s", got)
	}
}

func TestServeBounds(t *testing.T) {
	var output bytes.Buffer
	if err := serve(t.Context(), strings.NewReader(strings.Repeat("x", 65537)+"\n"), &output, notification.Publisher{DataRoot: t.TempDir()}); err == nil || output.Len() != 0 {
		t.Fatal("oversized request accepted")
	}
}
