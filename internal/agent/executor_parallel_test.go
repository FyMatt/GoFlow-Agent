package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type parallelExecutorMCP struct {
	entered chan string
	release chan struct{}
	wait    bool
	mu      sync.Mutex
	active  int
	maximum int
}

func (m *parallelExecutorMCP) ListTools(context.Context) ([]schema.Tool, error) { return nil, nil }

func (m *parallelExecutorMCP) CallTool(_ context.Context, name string, arguments []byte) (schema.ToolResult, error) {
	var input struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(arguments, &input)
	m.mu.Lock()
	m.active++
	if m.active > m.maximum {
		m.maximum = m.active
	}
	m.mu.Unlock()
	m.entered <- input.Path
	if m.wait {
		<-m.release
	}
	m.mu.Lock()
	m.active--
	m.mu.Unlock()
	return schema.ToolResult{ToolName: name, Content: input.Path}, nil
}

func TestExecutorParallelizesReadBatchAndPreservesOrder(t *testing.T) {
	mcp := &parallelExecutorMCP{entered: make(chan string, 2), release: make(chan struct{}), wait: true}
	readTool := schema.Tool{Name: "file_tools/read_file", Server: "file_tools", Kind: "read", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)}
	execCtx := ExecutionContext{
		Profile: config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindRead}, ToolPolicy: config.ToolPolicyAllow},
		Tools:   []schema.Tool{readTool},
		Session: session.New(4),
	}
	calls := []schema.ToolCall{
		{ID: "a", Name: readTool.Name, Arguments: []byte(`{"path":"a.go"}`)},
		{ID: "b", Name: readTool.Name, Arguments: []byte(`{"path":"b.go"}`)},
	}
	resultCh := make(chan struct {
		results []schema.ToolResult
		err     error
	}, 1)
	go func() {
		results, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, calls, nil, nil)
		resultCh <- struct {
			results []schema.ToolResult
			err     error
		}{results, err}
	}()
	select {
	case <-mcp.entered:
	case <-time.After(time.Second):
		t.Fatal("first read call did not start")
	}
	select {
	case <-mcp.entered:
	case <-time.After(time.Second):
		t.Fatal("second read call did not start concurrently")
	}
	close(mcp.release)
	outcome := <-resultCh
	if outcome.err != nil {
		t.Fatalf("parallel read batch failed: %v", outcome.err)
	}
	if len(outcome.results) != 2 || outcome.results[0].Content != "a.go" || outcome.results[1].Content != "b.go" {
		t.Fatalf("results must retain call order: %#v", outcome.results)
	}
	mcp.mu.Lock()
	maximum := mcp.maximum
	mcp.mu.Unlock()
	if maximum < 2 {
		t.Fatalf("expected overlapping read calls, maximum concurrency=%d", maximum)
	}
}

func TestExecutorKeepsWriteBatchSequential(t *testing.T) {
	mcp := &parallelExecutorMCP{entered: make(chan string, 2), release: make(chan struct{}, 2)}
	writeTool := schema.Tool{Name: "file_tools/write_file", Server: "file_tools", Kind: "write", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)}
	execCtx := ExecutionContext{
		Profile: config.AgentProfile{AllowedToolKinds: []config.ToolKind{config.ToolKindWrite}, ToolPolicy: config.ToolPolicyAllow},
		Tools:   []schema.Tool{writeTool},
		Session: session.New(4),
	}
	results, err := NewExecutor(mcp).RunToolCalls(context.Background(), execCtx, []schema.ToolCall{
		{ID: "a", Name: writeTool.Name, Arguments: []byte(`{"path":"a.go","content":"a"}`)},
		{ID: "b", Name: writeTool.Name, Arguments: []byte(`{"path":"b.go","content":"b"}`)},
	}, nil, nil)
	if err != nil || len(results) != 2 {
		t.Fatalf("sequential write batch failed: results=%#v err=%v", results, err)
	}
	mcp.mu.Lock()
	maximum := mcp.maximum
	mcp.mu.Unlock()
	if maximum != 1 {
		t.Fatalf("write calls must remain sequential, maximum concurrency=%d", maximum)
	}
}
