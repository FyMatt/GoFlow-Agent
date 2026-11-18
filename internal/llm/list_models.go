package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// ModelRef is one model advertised by an OpenAI-compatible provider.
type ModelRef struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// ListModels fetches the provider's model catalog from GET /models.
func (c *Client) ListModels(ctx context.Context) ([]ModelRef, error) {
	if c == nil {
		return nil, fmt.Errorf("model provider setup required: provider client is not configured")
	}
	if strings.TrimSpace(c.baseURL) == "" {
		return nil, fmt.Errorf("model provider setup required: configure base_url in Web Studio Settings/Resources or configs/providers/*.yaml")
	}
	if strings.TrimSpace(c.apiKey) == "" {
		return nil, fmt.Errorf("model provider setup required: configure api_key in Web Studio Settings/Resources or configs/providers/*.yaml")
	}
	var lastErr error
	for _, endpoint := range modelListURLs(c.baseURL) {
		models, err := c.listModelsFrom(ctx, endpoint)
		if err == nil {
			return models, nil
		}
		lastErr = err
		if !isMissingModelsEndpoint(err) {
			return nil, err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("provider did not return a model list")
	}
	return nil, lastErr
}

func (c *Client) listModelsFrom(ctx context.Context, endpoint string) ([]ModelRef, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list models: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	models, err := decodeModelList(body)
	if err != nil {
		return nil, err
	}
	return models, nil
}

func modelListURLs(baseURL string) []string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil
	}
	out := []string{base + "/models"}
	if !strings.HasSuffix(strings.ToLower(base), "/v1") {
		out = append(out, base+"/v1/models")
	}
	return out
}

func decodeModelList(raw []byte) ([]ModelRef, error) {
	var payload struct {
		Data   []json.RawMessage `json:"data"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}
	rows := payload.Data
	if len(rows) == 0 {
		rows = payload.Models
	}
	seen := map[string]struct{}{}
	models := make([]ModelRef, 0, len(rows))
	for _, row := range rows {
		id, ownedBy := modelListEntry(row)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, ModelRef{ID: id, OwnedBy: ownedBy})
	}
	sort.Slice(models, func(i, j int) bool {
		return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
	})
	return models, nil
}

func modelListEntry(raw json.RawMessage) (string, string) {
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return strings.TrimSpace(id), ""
	}
	var obj struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		OwnedBy string `json:"owned_by"`
	}
	if json.Unmarshal(raw, &obj) != nil {
		return "", ""
	}
	return strings.TrimSpace(firstNonEmpty(obj.ID, obj.Name)), strings.TrimSpace(obj.OwnedBy)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func isMissingModelsEndpoint(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "404") || strings.Contains(text, "not found") || strings.Contains(text, "405")
}
