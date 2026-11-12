package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/mcp"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestNotificationConnectorExecutorIntegration(t *testing.T) {
	if os.Getenv("GOFLOW_NOTIFICATION_INTEGRATION") != "1" {
		t.Skip("set GOFLOW_NOTIFICATION_INTEGRATION=1 to compile the real local connector")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dataRoot, workspace := t.TempDir(), t.TempDir()
	t.Setenv("GOFLOW_DATA_ROOT", dataRoot)
	manager := mcp.NewManager([]config.MCPServerRef{{Name: "notification_tools", Command: "go", Args: []string{"run", "./mcp_servers/notification_tools"}, WorkDir: root, RuntimeHome: root, Enabled: true, Isolation: "auto", Timeout: 30 * time.Second, EnvAllowlist: []string{"PATH", "SystemRoot", "USERPROFILE", "TEMP", "TMP", "LOCALAPPDATA", "GOCACHE", "GOMODCACHE", "GOFLOW_DATA_ROOT"}, AllowedCommands: []string{"go"}}})
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	tools, err := manager.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := session.New(4)
	id := state.StartAgentRun("emit a local fixture event")
	runtime := &Runtime{cfg: &config.Config{WorkspaceRoot: workspace, RuntimeHome: root, DataRoot: dataRoot}, session: state, mcp: manager, approvals: newApprovalStore(), taskStagingEnabled: true}
	profile := config.AgentProfile{Mode: "audit", ToolPolicy: config.ToolPolicyAllow, AllowedToolKinds: []config.ToolKind{config.ToolKindNetwork}}
	execCtx := newExecutionContext("notifier", profile, tools, nil, workspace)
	execCtx.AgentRunID, execCtx.Session = id, state
	results, err := NewExecutor(manager).RunToolCalls(ctx, execCtx, []schema.ToolCall{{ID: "local-event", Name: "notification_tools/publish_event", Arguments: json.RawMessage(`{"severity":"warning","title":"本地机制测试","summary":"没有连接真实目标或投递外部通知。"}`)}}, runtime, nil)
	if err != nil || len(results) != 1 || results[0].IsError || results[0].Suspended {
		t.Fatalf("local event failed through normal executor: %#v err=%v", results, err)
	}
	var receipt struct {
		EventID string `json:"event_id"`
		Status  string `json:"delivery_status"`
	}
	if err := json.Unmarshal([]byte(results[0].Content), &receipt); err != nil || receipt.EventID == "" || receipt.Status != "stored" {
		t.Fatalf("local receipt is not persisted/delivered accurately: %s err=%v", results[0].Content, err)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "notifications", "outbox", receipt.EventID+".json")); err != nil {
		t.Fatal(err)
	}
	if len(runtime.taskStaging) != 0 {
		t.Fatal("local notifications must not duplicate the project into a task sandbox")
	}
}
