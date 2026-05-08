package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// VerifierRoute describes the effective model route used by verifier passes.
type VerifierRoute struct {
	Enabled          bool     `json:"enabled"`
	Agent            string   `json:"agent,omitempty"`
	Provider         string   `json:"provider,omitempty"`
	Model            string   `json:"model,omitempty"`
	Modes            []string `json:"modes,omitempty"`
	MaxTokens        int      `json:"max_tokens,omitempty"`
	ProviderOverride bool     `json:"provider_override"`
	ModelOverride    bool     `json:"model_override"`
}

type verifierTarget struct {
	route   VerifierRoute
	profile config.AgentProfile
	llm     interfaces.LLMClient
	mode    string
}

func (r *Runtime) maybeRunVerifierPass(ctx context.Context, request string, result *schema.AgentResult, handler func(event schema.StreamEvent) error) error {
	if r == nil || r.cfg == nil || result == nil || !r.cfg.Verifier.Enabled {
		return nil
	}
	if strings.TrimSpace(result.Output) == "" || hasSuspendedToolResult(result.ToolResults) {
		return nil
	}
	if snapshot := r.SessionSnapshot(); len(snapshot.PendingApprovals) > 0 || strings.TrimSpace(snapshot.PendingHandoff.TargetAgent) != "" || r.workflowInProgress() {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(result.Mode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(r.Mode()))
	}
	if !verifierModeEnabled(mode, r.cfg.Verifier.Modes) {
		return nil
	}
	target, ok := r.effectiveVerifierTarget()
	if !ok {
		return nil
	}
	verifierID := target.route.Agent
	emitTaskStage(handler, r.session, verifierID, "audit", "verify", "checking final result")
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: fmt.Sprintf("running verifier pass with %s (provider=%s model=%s)...", verifierID, target.route.Provider, target.route.Model), AgentID: verifierID, Mode: "audit", NeedsAction: true})
	}
	started := time.Now()
	chatRequest := schema.ChatRequest{
		Model:       target.route.Model,
		System:      verifierSystemPrompt(),
		Messages:    []schema.Message{{Role: "user", Content: buildVerifierPrompt(request, *result)}},
		Tools:       nil,
		Temperature: target.profile.Temperature,
		MaxTokens:   r.cfg.Verifier.MaxTokens,
	}
	emitPromptBudget(handler, r.session, verifierID, target.mode, chatRequest, nil, 0)
	resp, err := target.llm.Chat(ctx, chatRequest)
	if err != nil {
		if r.audit != nil {
			r.audit.Record(schema.AuditEntry{Type: "verifier_pass", AgentID: verifierID, Outcome: "failed", Detail: err.Error(), DurationMs: time.Since(started).Milliseconds()})
		}
		if handler != nil {
			_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "verifier pass failed: " + err.Error(), AgentID: verifierID, Mode: "audit", NeedsAction: true})
		}
		return nil
	}
	emitTokenUsage(handler, r.session, verifierID, "audit", resp.Usage)
	verificationText := strings.TrimSpace(resp.Message.Content)
	if verificationText == "" {
		return nil
	}
	block := formatVerifierBlock(verificationText)
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventText, Content: "\n\n" + block, AgentID: verifierID, Mode: "audit"})
	}
	result.Output = strings.TrimSpace(result.Output) + "\n\n" + block
	result.Verification = append(result.Verification, schema.Verification{Kind: "verifier", Status: inferVerifierStatus(verificationText), Detail: verificationText})
	result.Structured = append(result.Structured, schema.StructuredSection{Title: "Verifier", Kind: "verification", Summary: verificationText})
	if r.audit != nil {
		r.audit.Record(schema.AuditEntry{Type: "verifier_pass", AgentID: verifierID, Outcome: "completed", Detail: verificationText, DurationMs: time.Since(started).Milliseconds(), PromptTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.OutputTokens, CachedTokens: resp.Usage.CachedTokens})
	}
	return nil
}

// VerifierRoute returns the effective verifier route for diagnostics.
func (r *Runtime) VerifierRoute() VerifierRoute {
	if target, ok := r.effectiveVerifierTarget(); ok {
		return target.route
	}
	if r == nil || r.cfg == nil {
		return VerifierRoute{}
	}
	return VerifierRoute{
		Enabled:   r.cfg.Verifier.Enabled,
		Agent:     strings.TrimSpace(r.cfg.Verifier.Agent),
		Provider:  strings.TrimSpace(r.cfg.Verifier.Provider),
		Model:     strings.TrimSpace(r.cfg.Verifier.Model),
		Modes:     append([]string(nil), r.cfg.Verifier.Modes...),
		MaxTokens: r.cfg.Verifier.MaxTokens,
	}
}

func (r *Runtime) effectiveVerifierTarget() (verifierTarget, bool) {
	if r == nil || r.cfg == nil || !r.cfg.Verifier.Enabled {
		return verifierTarget{}, false
	}
	agentID := strings.TrimSpace(r.cfg.Verifier.Agent)
	runner, ok := r.runners[agentID]
	if !ok {
		return verifierTarget{}, false
	}
	provider := strings.TrimSpace(r.cfg.Verifier.Provider)
	providerOverride := provider != ""
	if provider == "" {
		provider = strings.TrimSpace(runner.profile.Provider)
	}
	model := strings.TrimSpace(r.cfg.Verifier.Model)
	modelOverride := model != ""
	if model == "" {
		if provider != strings.TrimSpace(runner.profile.Provider) {
			if providerCfg, ok := r.cfg.Providers[provider]; ok {
				model = strings.TrimSpace(providerCfg.Model)
			}
		}
	}
	if model == "" {
		model = strings.TrimSpace(runner.profile.Model)
	}
	client := runner.llm
	if providerOverride {
		overrideClient, ok := r.clients[provider]
		if !ok {
			return verifierTarget{}, false
		}
		client = overrideClient
	}
	profile := runner.profile
	if providerOverride {
		profile.Provider = provider
	}
	if modelOverride || profile.Model == "" {
		profile.Model = model
	}
	mode := strings.TrimSpace(profile.Mode)
	if mode == "" {
		mode = "audit"
	}
	return verifierTarget{
		route: VerifierRoute{
			Enabled:          true,
			Agent:            agentID,
			Provider:         provider,
			Model:            model,
			Modes:            append([]string(nil), r.cfg.Verifier.Modes...),
			MaxTokens:        r.cfg.Verifier.MaxTokens,
			ProviderOverride: providerOverride,
			ModelOverride:    modelOverride,
		},
		profile: profile,
		llm:     client,
		mode:    mode,
	}, true
}

func verifierModeEnabled(mode string, modes []string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	for _, candidate := range modes {
		if mode == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func verifierSystemPrompt() string {
	return "You are GoFlow's final verifier. Do not call tools. Check the supplied agent result for unsupported claims, missing verification, regressions, security risks, and concrete next steps. Be concise and evidence-oriented."
}

func buildVerifierPrompt(request string, result schema.AgentResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Original request:\n%s\n\n", strings.TrimSpace(request))
	fmt.Fprintf(&b, "Agent mode: %s\n", result.Mode)
	if result.MatchedSkill != nil {
		fmt.Fprintf(&b, "Matched skill: %s\n", result.MatchedSkill.Name)
	}
	b.WriteString("\nAgent final answer:\n")
	b.WriteString(strings.TrimSpace(result.Output))
	if len(result.ToolResults) > 0 {
		b.WriteString("\n\nTool observations:\n")
		for _, toolResult := range result.ToolResults {
			status := "ok"
			if toolResult.Denied {
				status = "denied"
			} else if toolResult.IsError {
				status = "error"
			}
			fmt.Fprintf(&b, "- %s (%s): %s\n", toolResult.ToolName, status, truncateSummary(toolResult.Content))
		}
	}
	b.WriteString("\nReturn exactly this compact structure:\n")
	b.WriteString("Status: pass | warn | fail\n")
	b.WriteString("Checked: <what you checked>\n")
	b.WriteString("Risks: <remaining risk or none>\n")
	b.WriteString("Next: <one concrete next step or none>\n")
	return b.String()
}

func firstUserMessage(messages []schema.Message) string {
	for _, message := range messages {
		if message.Role == "user" && strings.TrimSpace(message.Content) != "" {
			return message.Content
		}
	}
	return ""
}

func formatVerifierBlock(content string) string {
	return "Verification pass:\n" + strings.TrimSpace(content)
}

func inferVerifierStatus(content string) string {
	lower := strings.ToLower(content)
	switch {
	case strings.Contains(lower, "status: fail"), strings.Contains(lower, "failed"), strings.Contains(lower, "blocker"), strings.Contains(lower, "critical"):
		return "failed"
	case strings.Contains(lower, "status: warn"), strings.Contains(lower, "warning"), strings.Contains(lower, "risk"):
		return "warning"
	case strings.Contains(lower, "status: pass"), strings.Contains(lower, "passed"):
		return "passed"
	default:
		return "checked"
	}
}
