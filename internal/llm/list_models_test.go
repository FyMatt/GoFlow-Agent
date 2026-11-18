package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
)

func TestClientListModelsReadsOpenAICatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("expected bearer auth, got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "gpt-4.1-mini", "owned_by": "openai"},
				{"id": "gpt-5.4-mini"},
			},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClient(config.LLMConfig{BaseURL: server.URL, APIKey: "test-key", Timeout: 2 * time.Second})
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "gpt-4.1-mini" || models[1].ID != "gpt-5.4-mini" {
		t.Fatalf("unexpected models %#v", models)
	}
}

func TestClientListModelsFallsBackToV1Path(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []any{"alpha", map[string]any{"name": "beta"}},
		})
	}))
	t.Cleanup(server.Close)

	client := NewClient(config.LLMConfig{BaseURL: server.URL, APIKey: "k", Timeout: 2 * time.Second})
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "alpha" || models[1].ID != "beta" {
		t.Fatalf("unexpected models %#v", models)
	}
}
