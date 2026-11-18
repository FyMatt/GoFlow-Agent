package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadNormalizesAgentKindsAndPrefersMainDefault(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`agent:
  name: GoFlow
  max_iterations: 4
providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  alpha-worker:
    kind: subagent
    provider: primary
  main:
    provider: primary
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.DefaultAgent != "main" {
		t.Fatalf("expected main default agent, got %q", cfg.DefaultAgent)
	}
	if !cfg.Agents["alpha-worker"].IsSubagent() {
		t.Fatal("expected alpha-worker to be a subagent")
	}
	if cfg.Agents["main"].Kind != AgentKindAgent {
		t.Fatalf("expected omitted kind to normalize to agent, got %q", cfg.Agents["main"].Kind)
	}
}

func TestLoadRejectsSubagentAsDefault(t *testing.T) {
	tmp := t.TempDir()
	configPath := filepath.Join(tmp, "goflow.yaml")
	content := []byte(`agent:
  name: GoFlow
  max_iterations: 4
default_agent: worker
providers:
  primary:
    base_url: http://localhost:9999/v1
    model: test-model
agents:
  worker:
    kind: subagent
    provider: primary
skill:
  directory: ./skills
`)
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil || !strings.Contains(err.Error(), "cannot reference a subagent") {
		t.Fatalf("expected subagent default rejection, got %v", err)
	}
}
