package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type auxiliaryModelKind string

const (
	auxiliaryModelRouter     auxiliaryModelKind = "router"
	auxiliaryModelSummarizer auxiliaryModelKind = "summarizer"
)

// AuxiliaryModelRoute describes an effective cheap-model helper route.
type AuxiliaryModelRoute struct {
	Enabled          bool    `json:"enabled"`
	Kind             string  `json:"kind"`
	Provider         string  `json:"provider,omitempty"`
	Model            string  `json:"model,omitempty"`
	MaxTokens        int     `json:"max_tokens,omitempty"`
	Temperature      float64 `json:"temperature,omitempty"`
	ProviderOverride bool    `json:"provider_override"`
	ModelOverride    bool    `json:"model_override"`
}

type auxiliaryModelTarget struct {
	route AuxiliaryModelRoute
	llm   interfaces.LLMClient
}

// AuxiliaryModelRoutes returns effective auxiliary routes for diagnostics.
func (r *Runtime) AuxiliaryModelRoutes() []AuxiliaryModelRoute {
	kinds := []auxiliaryModelKind{auxiliaryModelRouter, auxiliaryModelSummarizer}
	out := make([]AuxiliaryModelRoute, 0, len(kinds))
	for _, kind := range kinds {
		route := r.configuredAuxiliaryModelRoute(kind)
		out = append(out, AuxiliaryModelRoute{
			Enabled:          route.Enabled,
			Kind:             string(kind),
			Provider:         strings.TrimSpace(route.Provider),
			Model:            strings.TrimSpace(route.Model),
			MaxTokens:        route.MaxTokens,
			Temperature:      route.Temperature,
			ProviderOverride: strings.TrimSpace(route.Provider) != "",
			ModelOverride:    strings.TrimSpace(route.Model) != "",
		})
	}
	return out
}

func (r *Runtime) effectiveAuxiliaryModelTarget(kind auxiliaryModelKind, fallbackProfile config.AgentProfile, fallbackLLM interfaces.LLMClient) (auxiliaryModelTarget, bool) {
	if r == nil || r.cfg == nil {
		return auxiliaryModelTarget{}, false
	}
	routeCfg := r.configuredAuxiliaryModelRoute(kind)
	if !routeCfg.Enabled {
		return auxiliaryModelTarget{}, false
	}
	provider := strings.TrimSpace(routeCfg.Provider)
	providerOverride := provider != ""
	if provider == "" {
		provider = strings.TrimSpace(fallbackProfile.Provider)
	}
	model := strings.TrimSpace(routeCfg.Model)
	modelOverride := model != ""
	if model == "" && provider != "" && provider != strings.TrimSpace(fallbackProfile.Provider) {
		if providerCfg, ok := r.cfg.Providers[provider]; ok {
			model = strings.TrimSpace(providerCfg.Model)
		}
	}
	if model == "" {
		model = strings.TrimSpace(fallbackProfile.Model)
	}
	client := fallbackLLM
	if providerOverride {
		overrideClient, ok := r.clients[provider]
		if !ok {
			return auxiliaryModelTarget{}, false
		}
		client = overrideClient
	}
	maxTokens := routeCfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = fallbackProfile.MaxTokens
	}
	return auxiliaryModelTarget{
		route: AuxiliaryModelRoute{
			Enabled:          true,
			Kind:             string(kind),
			Provider:         provider,
			Model:            model,
			MaxTokens:        maxTokens,
			Temperature:      routeCfg.Temperature,
			ProviderOverride: providerOverride,
			ModelOverride:    modelOverride,
		},
		llm: client,
	}, true
}

func (r *Runtime) configuredAuxiliaryModelRoute(kind auxiliaryModelKind) config.AuxiliaryModelConfig {
	if r == nil || r.cfg == nil {
		return config.AuxiliaryModelConfig{}
	}
	switch kind {
	case auxiliaryModelRouter:
		return r.cfg.CostControl.Router
	case auxiliaryModelSummarizer:
		return r.cfg.CostControl.Summarizer
	default:
		return config.AuxiliaryModelConfig{}
	}
}

func (r *Runtime) classifyOrdinaryChatIntentWithModel(ctx context.Context, input string, handler func(event schema.StreamEvent) error) (ordinaryChatIntent, bool) {
	if r == nil {
		return ordinaryChatIntent{}, false
	}
	active := strings.TrimSpace(r.ActiveAgent())
	runner, ok := r.runners[active]
	if !ok {
		return ordinaryChatIntent{}, false
	}
	target, ok := r.effectiveAuxiliaryModelTarget(auxiliaryModelRouter, runner.profile, runner.llm)
	if !ok {
		return ordinaryChatIntent{}, false
	}
	emitTaskStage(handler, r.session, "router", "route", "inspect", "classifying request intent")
	request := schema.ChatRequest{
		Model:       target.route.Model,
		System:      routerSystemPrompt(),
		Messages:    []schema.Message{{Role: "user", Content: strings.TrimSpace(input)}},
		Tools:       nil,
		Temperature: target.route.Temperature,
		MaxTokens:   target.route.MaxTokens,
	}
	emitPromptBudget(handler, r.session, "router", "route", request, nil, 0)
	resp, err := target.llm.Chat(ctx, request)
	if err != nil {
		if r.audit != nil {
			r.audit.Record(schema.AuditEntry{Type: "intent_route", AgentID: "router", Outcome: "failed", Detail: err.Error()})
		}
		return ordinaryChatIntent{}, false
	}
	emitTokenUsage(handler, r.session, "router", "route", resp.Usage)
	mode := parseRouterMode(resp.Message.Content)
	if mode == "" || mode == "chat" {
		return ordinaryChatIntent{}, false
	}
	intent := ordinaryChatIntent{
		Mode:   mode,
		Score:  1,
		Term:   "llm_router",
		Hits:   []string{"llm_router"},
		Reason: fmt.Sprintf("llm_router:%s", truncateSummary(resp.Message.Content)),
	}
	return intent, true
}

func routerSystemPrompt() string {
	return "Classify the user's request for GoFlow routing. Return exactly one lowercase word: chat, plan, fix, or audit. Use fix for requests that ask to create, edit, implement, optimize, extend, debug, or run project changes. Use audit for review, risk, vulnerability, reverse engineering, or security analysis. Use plan for planning/design without direct changes. Use chat for ordinary Q&A."
}

func parseRouterMode(content string) string {
	lower := strings.ToLower(strings.TrimSpace(content))
	lower = strings.Trim(lower, "` \t\r\n\"'")
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		switch r {
		case ' ', '\t', '\r', '\n', ',', '.', ':', ';', '"', '\'', '`', '{', '}', '[', ']':
			return true
		default:
			return false
		}
	})
	for _, field := range fields {
		switch field {
		case "chat", "plan", "fix", "audit":
			return field
		}
	}
	return ""
}
