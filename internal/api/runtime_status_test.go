package api

import (
	"fmt"
	"testing"

	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func TestBuildCostRecommendationsUsesRecentWindow(t *testing.T) {
	history := make([]schema.PromptBudget, 0, 12)
	for i := 0; i < 4; i++ {
		history = append(history, schema.PromptBudget{
			EstimatedPromptTokens: 9000,
			CacheablePrefixTokens: 5200,
			AgentID:               "planner",
			PromptPrefixHash:      fmt.Sprintf("old-%d", i),
		})
	}
	for i := 0; i < 8; i++ {
		history = append(history, schema.PromptBudget{
			EstimatedPromptTokens: 1200,
			CacheablePrefixTokens: 900,
			AgentID:               "planner",
			PromptPrefixHash:      "stable-recent",
		})
	}
	latest := history[len(history)-1]
	recommendations := buildCostRecommendations(costDiagnostics{
		Latest:  &latest,
		History: history,
	})
	if hasCostRecommendation(recommendations, "prompt_prefix_churn") {
		t.Fatalf("expected recent stable samples to suppress stale prefix-churn warning, got %#v", recommendations)
	}
	if hasCostRecommendation(recommendations, "agent_prompt_high") {
		t.Fatalf("expected recent lighter samples to suppress stale agent-prompt warning, got %#v", recommendations)
	}
}

func TestProviderSetupStatusUsesSavedProviderConfig(t *testing.T) {
	status := buildSetupStatus([]providerSummary{{
		ID:        "primary",
		BaseURL:   "https://api.example.test/v1",
		Model:     "test-model",
		APIKeySet: true,
	}})
	if !status.ModelReady {
		t.Fatalf("expected model setup to be ready, got %#v", status)
	}
	if len(status.Providers) != 1 || !status.Providers[0].Ready {
		t.Fatalf("expected provider setup to be ready, got %#v", status.Providers)
	}
	items := status.Env
	if len(items) != 3 {
		t.Fatalf("expected three provider setup checks, got %#v", items)
	}
	for _, item := range items {
		if !item.Required || !item.Set {
			t.Fatalf("expected configured provider field to be marked ready, got %#v", item)
		}
		if item.Name == "GOFLOW_API_KEY" || item.Name == "GOFLOW_BASE_URL" {
			t.Fatalf("expected setup checks to describe provider fields, got %#v", item)
		}
	}
}

func TestProviderSetupStatusMarksIncompleteProviderFields(t *testing.T) {
	status := buildSetupStatus([]providerSummary{{ID: "primary"}})
	if status.ModelReady {
		t.Fatalf("expected model setup to be incomplete, got %#v", status)
	}
	if len(status.MissingProviderFields) != 3 {
		t.Fatalf("expected missing provider fields to be reported, got %#v", status)
	}
	if len(status.Providers) != 1 || status.Providers[0].Ready || len(status.Providers[0].Missing) != 3 {
		t.Fatalf("expected provider setup missing fields, got %#v", status.Providers)
	}
	items := status.Env
	missing := 0
	for _, item := range items {
		if item.Required && !item.Set {
			missing++
		}
	}
	if missing != 3 {
		t.Fatalf("expected base_url, api_key, and model to be missing, got %#v", items)
	}
}

func hasCostRecommendation(items []costRecommendation, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}
