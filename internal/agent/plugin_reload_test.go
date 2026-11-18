package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type reloadBarrierMCP struct {
	*stubRuntimeMCP
	entered chan struct{}
	release chan struct{}
	err     error
}

func (m *reloadBarrierMCP) ListTools(ctx context.Context) ([]schema.Tool, error) {
	close(m.entered)
	select {
	case <-m.release:
		return nil, m.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRuntimeHotReloadPreservesRunningStageRunner(t *testing.T) {
	oldClient, newClient := &delayedUsageLLMClient{}, &delayedUsageLLMClient{}
	oldProfile := config.AgentProfile{Name: "Original", Provider: "old", Model: "old-model", Mode: "chat", MaxIterations: 2, AllowedTools: []string{"file_tools/read_file"}}
	stageErr := errors.New("stop before provider execution")
	mcp := &reloadBarrierMCP{stubRuntimeMCP: &stubRuntimeMCP{}, entered: make(chan struct{}), release: make(chan struct{}), err: stageErr}
	runtimeRef, err := NewRuntime(&config.Config{
		WorkspaceRoot: t.TempDir(), DefaultAgent: "worker",
		Agents: map[string]config.AgentProfile{"worker": oldProfile},
	}, map[string]interfaces.LLMClient{"old": oldClient, "new": newClient}, stubSkillManager{}, mcp, session.New(4), nil)
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := runtimeRef.agentRunner("worker")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runSkillStage(ctx, runtimeRef, "worker", "inspect", schema.Skill{Name: "inspect"}, nil)
		done <- err
	}()
	select {
	case <-mcp.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("stage did not reach the barrier")
	}
	newProfile := config.AgentProfile{Name: "Replacement", Provider: "new", Model: "new-model", Mode: "plan", MaxIterations: 4, AllowedTools: []string{"file_tools/list_tree"}}
	updated := make(chan error, 1)
	go func() { updated <- runtimeRef.UpsertAgentProfile("worker", newProfile) }()
	select {
	case err := <-updated:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reload blocked on the running stage")
	}
	current, ok := runtimeRef.agentRunner("worker")
	if !ok || current == previous || !reflect.DeepEqual(current.profile, newProfile) || current.llm != newClient {
		t.Fatalf("replacement runner was not published: previous=%p current=%#v", previous, current)
	}
	if !reflect.DeepEqual(previous.profile, oldProfile) || previous.llm != oldClient {
		t.Fatalf("running stage was mutated: profile=%#v client=%p", previous.profile, previous.llm)
	}
	if current.executor != previous.executor {
		t.Fatal("reload replaced the executor")
	}
	close(mcp.release)
	select {
	case err := <-done:
		if !errors.Is(err, stageErr) {
			t.Fatalf("stage error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stage did not leave the barrier")
	}
	if oldClient.calls != 0 || newClient.calls != 0 {
		t.Fatal("barrier test must not call a provider")
	}
}

func TestRuntimeHotReloadConcurrentRegistrySelection(t *testing.T) {
	profile := config.AgentProfile{Name: "Chat", Provider: "primary", Model: "model", Mode: "chat"}
	runtimeRef, err := NewRuntime(&config.Config{
		WorkspaceRoot: t.TempDir(),
		Agents:        map[string]config.AgentProfile{"chat": profile},
		Verifier:      config.VerifierConfig{Enabled: true, Agent: "chat"},
	}, map[string]interfaces.LLMClient{"primary": &delayedUsageLLMClient{}}, stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), nil)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			<-start
			for range 1000 {
				for _, name := range runtimeRef.AgentNames() {
					if profile, ok := runtimeRef.Profile(name); ok && profile.Provider != "primary" {
						t.Errorf("inconsistent profile: %#v", profile)
					}
				}
				if id := runtimeRef.defaultAgentID(); id != "chat" {
					t.Errorf("default agent=%q", id)
				}
				if _, ok := runtimeRef.agentRunner("chat"); !ok {
					t.Error("stable agent disappeared")
				}
				if target, ok := runtimeRef.effectiveVerifierTarget(); !ok || target.profile.Provider != "primary" {
					t.Error("verifier selection lost the stable agent")
				}
				_ = runtimeRef.activeModel()
			}
		})
	}
	wg.Go(func() {
		<-start
		for i := range 1000 {
			updated := profile
			updated.Model = fmt.Sprintf("model-%d", i)
			for _, id := range []string{"chat", fmt.Sprintf("temporary-%d", i%16)} {
				if err := runtimeRef.UpsertAgentProfile(id, updated); err != nil {
					t.Error(err)
					return
				}
			}
			if err := runtimeRef.RemoveAgentProfile(fmt.Sprintf("temporary-%d", i%16)); err != nil {
				t.Error(err)
				return
			}
		}
	})
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("concurrent registry selection did not finish")
	}
}

func TestRuntimeHotReloadRemovingActiveAgentDoesNotDeadlock(t *testing.T) {
	runtimeRef := &Runtime{
		cfg:     &config.Config{Agents: map[string]config.AgentProfile{"a": {}, "b": {}}},
		runners: map[string]*AgentRunner{"a": {id: "a"}, "b": {id: "b"}}, active: "a",
	}
	done := make(chan error, 1)
	go func() { done <- runtimeRef.RemoveAgentProfile("a") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("removing active agent deadlocked during default selection")
	}
	if runtimeRef.active != "b" || runtimeRef.defaultAgentID() != "b" {
		t.Fatal("default selection changed")
	}
}

func TestRuntimeHotPlugsAgentProfile(t *testing.T) {
	runtimeRef, err := NewRuntime(&config.Config{
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat": {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 2},
		},
	}, map[string]interfaces.LLMClient{
		"primary": &delayedUsageLLMClient{response: schema.ChatResponse{Message: schema.Message{Content: "ok"}}},
	}, stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), nil)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := runtimeRef.UpsertAgentProfile("planner", config.AgentProfile{
		Name:     "Planner",
		Provider: "primary",
		Mode:     "plan",
		Model:    "test-model",
	}); err != nil {
		t.Fatalf("UpsertAgentProfile: %v", err)
	}
	profile, ok := runtimeRef.Profile("planner")
	if !ok || profile.Mode != "plan" {
		t.Fatalf("expected hot-plugged planner, got ok=%v profile=%#v", ok, profile)
	}
	if err := runtimeRef.RemoveAgentProfile("planner"); err != nil {
		t.Fatalf("RemoveAgentProfile: %v", err)
	}
	if _, ok := runtimeRef.Profile("planner"); ok {
		t.Fatal("expected planner to be removed")
	}
}

func TestRuntimeHotPlugsMCPServerConfig(t *testing.T) {
	runtimeRef, err := NewRuntime(&config.Config{
		DefaultAgent: "chat",
		Agents: map[string]config.AgentProfile{
			"chat": {Name: "Chat", Provider: "primary", Mode: "chat", Model: "test-model", MaxIterations: 2},
		},
	}, map[string]interfaces.LLMClient{
		"primary": &delayedUsageLLMClient{response: schema.ChatResponse{Message: schema.Message{Content: "ok"}}},
	}, stubSkillManager{}, &stubRuntimeMCP{}, session.New(4), nil)
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if err := runtimeRef.UpsertMCPServer(config.MCPServerRef{Name: "notes-helper", Command: "python", Enabled: true}); err != nil {
		t.Fatalf("UpsertMCPServer: %v", err)
	}
	found := false
	for _, ref := range runtimeRef.MCPServerRefs() {
		if ref.Name == "notes-helper" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected notes-helper in runtime MCP catalog, got %#v", runtimeRef.MCPServerRefs())
	}
}
