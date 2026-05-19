package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

// Agent coordinates skill matching, LLM reasoning, and MCP tools.
type Agent struct {
	cfg      *config.Config
	llm      interfaces.LLMClient
	skills   interfaces.SkillManager
	mcp      interfaces.MCPClient
	executor *Executor
}

type agentConversationState struct {
	Profile          config.AgentProfile
	MatchedSkill     *schema.Skill
	SystemPrompt     string
	MemoryText       string
	SessionSnapshot  session.Snapshot
	PromptContext    promptBudgetContext
	Mode             string
	Input            string
	Messages         []schema.Message
	CollectedResults []schema.ToolResult
	Tools            []schema.Tool
	StartedAt        time.Time
	FallbackUsed     bool
	StreamedText     bool
}

type agentRunOptions struct {
	AllowedTools []string
}

// New constructs a new agent instance.
func New(cfg *config.Config, llm interfaces.LLMClient, skills interfaces.SkillManager, mcp interfaces.MCPClient) *Agent {
	return &Agent{
		cfg:      cfg,
		llm:      llm,
		skills:   skills,
		mcp:      mcp,
		executor: NewExecutor(mcp),
	}
}

// Run executes a single agent request.
func (a *Agent) Run(ctx context.Context, input string) (schema.AgentResult, error) {
	return a.RunStream(ctx, input, nil)
}

// RunStream executes a request while emitting incremental progress.
func (a *Agent) RunStream(ctx context.Context, input string, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	profile := config.AgentProfile{
		Name:             a.cfg.Agent.Name,
		Provider:         "default",
		Model:            a.cfg.LLM.Model,
		Temperature:      a.cfg.LLM.Temperature,
		MaxTokens:        a.cfg.LLM.MaxTokens,
		MaxIterations:    a.cfg.Agent.MaxIterations,
		AllowedToolKinds: []config.ToolKind{config.ToolKindRead, config.ToolKindWrite, config.ToolKindExec, config.ToolKindNetwork, config.ToolKindUnknown},
		ToolPolicy:       config.ToolPolicyAllow,
		Mode:             "chat",
	}
	runner := &AgentRunner{id: "default", profile: profile, llm: a.llm, executor: a.executor}
	return runner.RunStream(ctx, input, a.skills, a.mcp, session.New(a.cfg.Session.MaxHistory), runtime.NewAuditLogger(a.cfg.Audit.Enabled, a.cfg.Audit.RedactContent), nil, handler)
}

// RunStream executes a request for one configured agent runner.
func (r *AgentRunner) RunStream(ctx context.Context, input string, skills interfaces.SkillManager, mcp interfaces.MCPClient, state *session.State, audit *runtime.AuditLogger, runtimeRef *Runtime, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	return r.runStreamWithOptions(ctx, input, skills, mcp, state, audit, runtimeRef, agentRunOptions{}, handler)
}

func (r *AgentRunner) runStreamWithOptions(ctx context.Context, input string, skills interfaces.SkillManager, mcp interfaces.MCPClient, state *session.State, audit *runtime.AuditLogger, runtimeRef *Runtime, options agentRunOptions, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	if runtimeRef != nil && !runtimeRef.WorkspaceConfirmed() {
		mcp = workspaceGatedMCP{mcp: mcp}
	}
	matchedSkill, matchDiagnostic, _ := skills.MatchWithDiagnostics(input)
	if matchedSkill != nil && strings.TrimSpace(matchedSkill.PreferredAgent) != "" && matchedSkill.PreferredAgent != r.id {
		audit.Record(schema.AuditEntry{Type: "skill_match", AgentID: r.id, SkillName: matchedSkill.Name, Outcome: "preferred_agent_mismatch", Detail: matchedSkill.PreferredAgent})
	}
	tools, err := mcp.ListTools(ctx)
	if err != nil {
		return schema.AgentResult{}, fmt.Errorf("list tools: %w", err)
	}
	profile := r.profile
	profile = applySkillToProfile(profile, matchedSkill)
	profile = applyWorkflowStageAllowedTools(profile, options.AllowedTools)
	profile = ensureMinimumToolIterations(profile)
	if state != nil {
		state.SetActiveAgent(r.id)
		if state.Mode() == "" || state.Mode() == "chat" {
			state.SetMode(profile.Mode)
		}
		state.AddPrompt(input)
		state.SetLastSkillMatch(matchedSkill, matchDiagnostic)
	}
	snapshot := session.Snapshot{}
	if state != nil {
		snapshot = state.Snapshot()
	}
	mode := snapshot.Mode
	if mode == "" {
		mode = profile.Mode
	}
	memoryText := ""
	promptContext := promptBudgetContext{}
	if runtimeRef != nil && runtimeRef.MemoryStore() != nil {
		if memoryContext, err := runtimeRef.MemoryStore().PromptContextFresh(ctx, input, agentRunIDFromContext(ctx)); err == nil {
			memoryText = memoryContext.PromptText()
			promptContext.Memory = &memoryContext
		}
	}
	systemPrompt := BuildSystemPrompt(profile, matchedSkill, snapshot)
	startedAt := time.Now()
	fallbackUsed := effectiveFallbackUsed(r.llm, r.fallbackUsed)
	if audit != nil {
		audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(matchedSkill), Outcome: "started", Detail: input})
	}
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: fmt.Sprintf("agent=%s mode=%s", r.id, mode), AgentID: r.id, Mode: mode})
	}
	emitTaskStage(handler, state, r.id, mode, initialTaskStageForMode(mode), "starting request")
	return r.continueConversation(ctx, agentConversationState{
		Profile:         profile,
		MatchedSkill:    matchedSkill,
		SystemPrompt:    systemPrompt,
		MemoryText:      memoryText,
		SessionSnapshot: snapshot,
		PromptContext:   promptContext,
		Mode:            mode,
		Input:           input,
		Messages:        []schema.Message{{Role: "user", Content: input}},
		Tools:           tools,
		StartedAt:       startedAt,
		FallbackUsed:    fallbackUsed,
	}, skills, mcp, state, audit, runtimeRef, handler)
}

type workspaceGatedMCP struct {
	mcp interfaces.MCPClient
}

func (m workspaceGatedMCP) ListTools(ctx context.Context) ([]schema.Tool, error) {
	tools, err := m.mcp.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]schema.Tool, 0, len(tools))
	for _, tool := range tools {
		if toolAllowedWithoutWorkspace(tool) {
			filtered = append(filtered, tool)
		}
	}
	return filtered, nil
}

func (m workspaceGatedMCP) RefreshTools(ctx context.Context) ([]schema.Tool, error) {
	tools, err := m.mcp.RefreshTools(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]schema.Tool, 0, len(tools))
	for _, tool := range tools {
		if toolAllowedWithoutWorkspace(tool) {
			filtered = append(filtered, tool)
		}
	}
	return filtered, nil
}

func (m workspaceGatedMCP) CallTool(ctx context.Context, name string, arguments []byte) (schema.ToolResult, error) {
	tools, err := m.mcp.ListTools(ctx)
	if err != nil {
		return schema.ToolResult{}, err
	}
	for _, tool := range tools {
		if tool.Name == name || formatQualifiedToolName(tool) == name {
			if !toolAllowedWithoutWorkspace(tool) {
				return schema.ToolResult{ToolName: tool.Name, Content: "workspace is not confirmed; workspace-scoped tools are disabled", IsError: true, Denied: true}, nil
			}
			return m.mcp.CallTool(ctx, name, arguments)
		}
	}
	return schema.ToolResult{ToolName: name, Content: "workspace is not confirmed; unknown tools are disabled", IsError: true, Denied: true}, nil
}

func (m workspaceGatedMCP) HealthStatus(ctx context.Context) map[string]string {
	return m.mcp.HealthStatus(ctx)
}

func (m workspaceGatedMCP) MCPCallMetrics() map[string]interfaces.MCPServerCallMetrics {
	reporter, ok := m.mcp.(interfaces.MCPMetricsReporter)
	if !ok {
		return nil
	}
	return reporter.MCPCallMetrics()
}

func (m workspaceGatedMCP) ToolNames() []string {
	names := m.mcp.ToolNames()
	if len(names) == 0 {
		return nil
	}
	return names
}

func toolAllowedWithoutWorkspace(tool schema.Tool) bool {
	return strings.EqualFold(strings.TrimSpace(tool.Kind), string(config.ToolKindNetwork))
}

func formatQualifiedToolName(tool schema.Tool) string {
	if strings.TrimSpace(tool.Server) == "" {
		return strings.TrimSpace(tool.Name)
	}
	return strings.TrimSpace(tool.Server) + "/" + strings.TrimSpace(tool.Name)
}

func applySkillToProfile(profile config.AgentProfile, skill *schema.Skill) config.AgentProfile {
	if skill == nil {
		return profile
	}
	if len(skill.AllowedToolKinds) > 0 {
		profile.AllowedToolKinds = intersectProfileToolKinds(profile.AllowedToolKinds, skillAllowedToolKinds(skill))
	}
	if skill.MaxIterations > 0 {
		profile.MaxIterations = skill.MaxIterations
	}
	profile.AllowedTools = intersectProfileTools(profile.AllowedTools, skillDeclaredTools(skill))
	if skill.Mode != "" {
		profile.Mode = skill.Mode
	}
	return profile
}

func skillAllowedToolKinds(skill *schema.Skill) []string {
	if skill == nil {
		return nil
	}
	kinds := append([]string(nil), skill.AllowedToolKinds...)
	if len(skill.Scripts) > 0 {
		kinds = append(kinds, string(config.ToolKindExec))
	}
	return kinds
}

func skillDeclaredTools(skill *schema.Skill) []schema.SkillTool {
	if skill == nil {
		return nil
	}
	tools := append([]schema.SkillTool(nil), skill.Tools...)
	if len(skill.Scripts) > 0 {
		tools = append(tools, schema.SkillTool{Name: "skill_runner/run_script", Required: true})
	}
	return tools
}

func intersectProfileTools(existing []string, declared []schema.SkillTool) []string {
	if len(declared) == 0 {
		return existing
	}
	declaredNames := make([]string, 0, len(declared))
	for _, tool := range declared {
		name := strings.TrimSpace(tool.Name)
		if name != "" {
			declaredNames = append(declaredNames, name)
		}
	}
	if len(declaredNames) == 0 {
		return existing
	}
	if len(existing) == 0 {
		return dedupeToolNames(declaredNames)
	}
	intersected := make([]string, 0, len(declaredNames))
	for _, declaredName := range declaredNames {
		for _, existingName := range existing {
			if toolNameEquivalent(declaredName, existingName) {
				intersected = append(intersected, declaredName)
				break
			}
		}
	}
	return dedupeToolNames(intersected)
}

func dedupeToolNames(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

func toolNameEquivalent(left, right string) bool {
	left = strings.ToLower(strings.TrimSpace(left))
	right = strings.ToLower(strings.TrimSpace(right))
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	if !strings.Contains(left, "/") && strings.HasSuffix(right, "/"+left) {
		return true
	}
	if !strings.Contains(right, "/") && strings.HasSuffix(left, "/"+right) {
		return true
	}
	return false
}

func filterPromptTools(profile config.AgentProfile, tools []schema.Tool) []schema.Tool {
	filtered := make([]schema.Tool, 0, len(tools))
	for _, tool := range tools {
		if policy.EnforceToolPolicy(profile, tool) == nil {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func applyWorkflowStageAllowedTools(profile config.AgentProfile, allowed []string) config.AgentProfile {
	allowed = dedupeToolNames(allowed)
	if len(allowed) == 0 {
		return profile
	}
	if len(profile.AllowedTools) == 0 {
		profile.AllowedTools = allowed
		return profile
	}
	intersected := make([]string, 0, len(allowed))
	for _, stageTool := range allowed {
		for _, profileTool := range profile.AllowedTools {
			if toolNameEquivalent(stageTool, profileTool) {
				intersected = append(intersected, stageTool)
				break
			}
		}
	}
	if len(intersected) == 0 {
		profile.AllowedTools = []string{"__none__"}
		return profile
	}
	profile.AllowedTools = dedupeToolNames(intersected)
	return profile
}

func intersectProfileToolKinds(existing []config.ToolKind, requested []string) []config.ToolKind {
	requestedKinds := make([]config.ToolKind, 0, len(requested))
	seenRequested := map[config.ToolKind]struct{}{}
	for _, kind := range requested {
		toolKind := config.ToolKind(strings.TrimSpace(kind))
		if strings.TrimSpace(string(toolKind)) == "" {
			continue
		}
		if _, ok := seenRequested[toolKind]; ok {
			continue
		}
		seenRequested[toolKind] = struct{}{}
		requestedKinds = append(requestedKinds, toolKind)
	}
	if len(requestedKinds) == 0 {
		return existing
	}
	if len(existing) == 0 {
		return requestedKinds
	}
	allowedExisting := make(map[config.ToolKind]struct{}, len(existing))
	for _, kind := range existing {
		allowedExisting[kind] = struct{}{}
	}
	intersected := make([]config.ToolKind, 0, len(requestedKinds))
	for _, kind := range requestedKinds {
		if _, ok := allowedExisting[kind]; ok {
			intersected = append(intersected, kind)
		}
	}
	if len(intersected) == 0 {
		return []config.ToolKind{config.ToolKind("__none__")}
	}
	return intersected
}

func (r *AgentRunner) continueConversation(ctx context.Context, state agentConversationState, skills interfaces.SkillManager, mcp interfaces.MCPClient, sessionState *session.State, audit *runtime.AuditLogger, runtimeRef *Runtime, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	_ = skills
	if len(state.Tools) == 0 {
		tools, err := mcp.ListTools(ctx)
		if err != nil {
			return schema.AgentResult{}, fmt.Errorf("list tools: %w", err)
		}
		state.Tools = tools
	}
	if state.StartedAt.IsZero() {
		state.StartedAt = time.Now()
	}
	profile := state.Profile
	mode := state.Mode
	if mode == "" {
		mode = profile.Mode
	}
	messages := dynamicContextMessages(state.MemoryText, state.SessionSnapshot, state.Input, state.Messages)
	collectedResults := append([]schema.ToolResult(nil), state.CollectedResults...)
	streamedText := state.StreamedText
	fallbackUsed := effectiveFallbackUsed(r.llm, state.FallbackUsed)
	execCtx := newExecutionContext(r.id, profile, state.Tools, audit, workspaceRoot(runtimeRef))
	execCtx.AgentRunID = agentRunIDFromContext(ctx)
	if runtimeRef != nil {
		execCtx.MemoryStore = runtimeRef.MemoryStore()
		execCtx.MemoryTaskID = fallbackText(execCtx.AgentRunID, runtimeRef.currentWorkflowRunID())
		execCtx.RiskPolicy = runtimeRef.toolRiskPolicy()
		execCtx.MCPServers = runtimeRef.MCPServerRefs()
		runtimeRef.applySessionApprovedTools(&execCtx)
	}
	promptTools := filterPromptTools(profile, state.Tools)
	promptToolContext := promptBudgetContextWithTools(state.PromptContext, profile, state.Tools)
	actionNudge := newActionNudgeTracker(profile, mode, state.MatchedSkill, state.Input)

	for i := 0; i <= profile.MaxIterations; i++ {
		iteration := i + 1
		finalResponseTurn := i == profile.MaxIterations
		if i > 0 && len(collectedResults) > 0 {
			emitTaskStage(handler, sessionState, r.id, mode, "summarize", "reasoning over tool observations")
		}
		emitModelWaitStatus(handler, r.id, mode, modelWaitStatusContext{AfterToolResults: i > 0 && len(collectedResults) > 0})
		request := schema.ChatRequest{
			Model:       profile.Model,
			System:      state.SystemPrompt,
			Messages:    messages,
			Tools:       promptTools,
			Temperature: profile.Temperature,
			MaxTokens:   profile.MaxTokens,
		}
		emitPromptBudget(handler, sessionState, r.id, mode, request, state.MatchedSkill, len(state.Tools), promptToolContext)

		resp, err := runAgentChatWithRecovery(ctx, r.llm, request, r.id, mode, handler, &streamedText, &messages)
		if err != nil {
			if isRecoverableToolArgumentError(err) {
				return r.finalizeWithInvalidToolArguments(state, collectedResults, fallbackUsed, iteration, handler, audit)
			}
			execCtx.recordAudit(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "llm_error", Detail: err.Error(), DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: effectiveFallbackUsed(r.llm, fallbackUsed)})
			return schema.AgentResult{}, fmt.Errorf("llm chat: %w", err)
		}
		emitTokenUsage(handler, sessionState, r.id, mode, resp.Usage)

		if len(resp.ToolCalls) == 0 {
			emitTaskStage(handler, sessionState, r.id, mode, "summarize", "preparing final response")
			if !streamedText && handler != nil && resp.Message.Content != "" {
				if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: resp.Message.Content, AgentID: r.id, Mode: mode}); err != nil {
					return schema.AgentResult{}, err
				}
			}
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventDone, Content: resp.Message.Content, AgentID: r.id, Mode: mode})
			}
			if audit != nil {
				fallbackUsed = effectiveFallbackUsed(r.llm, fallbackUsed)
				audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "completed", Detail: resp.Message.Content, DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: fallbackUsed, PromptTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.OutputTokens, CachedTokens: resp.Usage.CachedTokens})
			}
			structured := buildStructuredSections(resp.Message.Content, state.MatchedSkill, collectedResults, mode, fallbackUsed)
			findings, changes, verification := buildStructuredArtifacts(resp.Message.Content, collectedResults, mode)
			return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: profile.Model, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification, ResponseMessage: schema.CopyMessage(resp.Message)}, nil
		}
		runFinalToolCalls := finalResponseTurn && shouldRunFinalTurnToolCalls(execCtx, resp.ToolCalls)
		if finalResponseTurn && !runFinalToolCalls {
			return r.finalizeAfterIterationBudget(ctx, state, messages, collectedResults, fallbackUsed, iteration, handler, audit, runtimeRef)
		}

		assistantMessage := assistantToolMessage(resp)
		messages = append(messages, assistantMessage)
		messages = compactMessagesForConversation(messages)
		batchProfile := classifyToolCallBatch(resp.ToolCalls, execCtx)
		resumeMessages := schema.CopyMessages(messages)
		resumeCollected := append([]schema.ToolResult(nil), collectedResults...)
		emitTaskStage(handler, sessionState, r.id, mode, taskStageForToolCalls(resp.ToolCalls, execCtx), summarizeToolCallStage(resp.ToolCalls, execCtx))
		results, err := r.executor.RunToolCalls(ctx, execCtx, resp.ToolCalls, runtimeRef, handler)
		if err != nil {
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), AgentID: r.id, Mode: mode, IsError: true})
			}
			return schema.AgentResult{}, err
		}
		collectedResults = append(collectedResults, results...)
		suspendedCalls := make([]schema.ToolCall, 0)
		for _, result := range results {
			if sessionState != nil {
				summary := summarizeToolResultForSession(result)
				sessionState.AddToolSummary(summary)
			}
			if handler != nil {
				if err := handler(schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: result.ToolName, ToolCallID: result.CallID, ArgumentsSummary: summarizeToolResultArguments(result, resp.ToolCalls), Content: result.Content, AgentID: r.id, Mode: mode, IsError: result.IsError, Suspended: result.Suspended}); err != nil {
					return schema.AgentResult{}, err
				}
			}
			if result.Suspended {
				if call, ok := toolCallByID(resp.ToolCalls, result.CallID); ok {
					suspendedCalls = append(suspendedCalls, call)
				}
				continue
			}
			artifactRef := storeCompactedToolResultArtifact(sessionState, result, r.id, mode)
			promptContent := compactToolResultForPromptWithArtifact(result, artifactRef)
			messages = append(messages, schema.Message{Role: "tool", Name: result.ToolName, ToolCallID: result.CallID, Content: promptContent})
			resumeMessages = append(resumeMessages, schema.Message{Role: "tool", Name: result.ToolName, ToolCallID: result.CallID, Content: promptContent})
			resumeCollected = append(resumeCollected, result)
		}
		if len(suspendedCalls) > 0 {
			if runtimeRef != nil && (strings.TrimSpace(execCtx.AgentRunID) != "" || !runtimeRef.workflowInProgress()) {
				runtimeRef.CaptureOrdinaryApprovalContext(execCtx.AgentRunID, r.id, profile, state.MatchedSkill, state.SystemPrompt, mode, resumeMessages, suspendedCalls, resumeCollected)
			}
			structured := buildStructuredSections(resp.Message.Content, state.MatchedSkill, collectedResults, mode, fallbackUsed)
			findings, changes, verification := buildStructuredArtifacts(resp.Message.Content, collectedResults, mode)
			return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: profile.Model, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification, ResponseMessage: assistantMessage}, nil
		}
		if runFinalToolCalls {
			return r.finalizeAfterIterationBudget(ctx, state, messages, collectedResults, fallbackUsed, iteration, handler, audit, runtimeRef)
		}
		if nudge, ok := actionNudge.Next(batchProfile, len(results)); ok {
			if summary := toolObservationDigestMessage(collectedResults); strings.TrimSpace(summary) != "" {
				messages = append(messages, schema.Message{Role: "user", Content: summary})
			}
			messages = append(messages, schema.Message{Role: "user", Content: nudge})
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "enough context gathered; nudging model to act", AgentID: r.id, Mode: mode, NeedsAction: true})
			}
		}
		streamedText = false
	}

	execCtx.recordAudit(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "max_iterations", Detail: state.Input, DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: profile.MaxIterations, FallbackUsed: effectiveFallbackUsed(r.llm, fallbackUsed)})
	return schema.AgentResult{}, fmt.Errorf("agent exceeded max iterations")
}

func (r *AgentRunner) finalizeAfterIterationBudget(ctx context.Context, state agentConversationState, messages []schema.Message, collectedResults []schema.ToolResult, fallbackUsed bool, iteration int, handler func(event schema.StreamEvent) error, audit *runtime.AuditLogger, runtimeRef *Runtime) (schema.AgentResult, error) {
	mode := state.Mode
	if mode == "" {
		mode = state.Profile.Mode
	}
	summaryLLM := r.llm
	summaryModel := state.Profile.Model
	summaryTemperature := state.Profile.Temperature
	summaryMaxTokens := state.Profile.MaxTokens
	if runtimeRef != nil {
		if target, ok := runtimeRef.effectiveAuxiliaryModelTarget(auxiliaryModelSummarizer, state.Profile, r.llm); ok {
			summaryLLM = target.llm
			summaryModel = target.route.Model
			summaryTemperature = target.route.Temperature
			summaryMaxTokens = target.route.MaxTokens
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: fmt.Sprintf("using summarizer route provider=%s model=%s", fallbackText(target.route.Provider, "-"), fallbackText(target.route.Model, "-")), AgentID: r.id, Mode: mode, NeedsAction: true})
			}
		}
	}
	var sessionState *session.State
	if runtimeRef != nil {
		sessionState = runtimeRef.session
	}
	if handler != nil {
		emitTaskStage(handler, sessionState, r.id, mode, "summarize", "iteration budget reached")
		_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "tool iteration budget reached; requesting final summary without additional tool calls", AgentID: r.id, Mode: mode, NeedsAction: true})
	}
	finalMessages := compactMessagesForSummaryPrompt(messages)
	finalMessages = append(finalMessages, schema.Message{Role: "user", Content: "Tool iteration budget has been reached. Do not call more tools. Do not repeat earlier planning text. Produce a concise final summary from the observations already in this conversation: completed work, verification already available, remaining risk, and the next concrete step if any."})
	streamedText := false
	request := schema.ChatRequest{
		Model:       summaryModel,
		System:      state.SystemPrompt,
		Messages:    finalMessages,
		Tools:       nil,
		Temperature: summaryTemperature,
		MaxTokens:   summaryMaxTokens,
	}
	emitPromptBudget(handler, sessionState, r.id, mode, request, state.MatchedSkill, len(state.Tools), promptBudgetContextWithTools(state.PromptContext, state.Profile, state.Tools))
	emitModelWaitStatus(handler, r.id, mode, modelWaitStatusContext{FinalSummary: true})
	resp, err := runAgentChatWithRecovery(ctx, summaryLLM, request, r.id, mode, handler, &streamedText, &finalMessages)
	if err != nil {
		if isRecoverableToolArgumentError(err) {
			return r.finalizeWithLocalBudgetSummary(state, collectedResults, fallbackUsed, iteration, handler, audit)
		}
		if audit != nil {
			audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "max_iterations", Detail: err.Error(), DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: effectiveFallbackUsed(summaryLLM, fallbackUsed)})
		}
		return schema.AgentResult{}, fmt.Errorf("agent exceeded max iterations")
	}
	emitTokenUsage(handler, sessionState, r.id, mode, resp.Usage)
	if len(resp.ToolCalls) > 0 && strings.TrimSpace(resp.Message.Content) == "" {
		return r.finalizeWithLocalBudgetSummary(state, collectedResults, fallbackUsed, iteration, handler, audit)
	}
	if !streamedText && handler != nil && strings.TrimSpace(resp.Message.Content) != "" {
		if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: resp.Message.Content, AgentID: r.id, Mode: mode}); err != nil {
			return schema.AgentResult{}, err
		}
	}
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventDone, Content: resp.Message.Content, AgentID: r.id, Mode: mode})
	}
	if audit != nil {
		fallbackUsed = effectiveFallbackUsed(summaryLLM, fallbackUsed)
		audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "completed_after_iteration_budget", Detail: resp.Message.Content, DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: fallbackUsed, PromptTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.OutputTokens, CachedTokens: resp.Usage.CachedTokens})
	}
	structured := buildStructuredSections(resp.Message.Content, state.MatchedSkill, collectedResults, mode, fallbackUsed)
	findings, changes, verification := buildStructuredArtifacts(resp.Message.Content, collectedResults, mode)
	return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: summaryModel, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification, ResponseMessage: schema.CopyMessage(resp.Message)}, nil
}

func (r *AgentRunner) finalizeWithLocalBudgetSummary(state agentConversationState, collectedResults []schema.ToolResult, fallbackUsed bool, iteration int, handler func(event schema.StreamEvent) error, audit *runtime.AuditLogger) (schema.AgentResult, error) {
	mode := state.Mode
	if mode == "" {
		mode = state.Profile.Mode
	}
	output := buildLocalBudgetSummary(collectedResults)
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventText, Content: output, AgentID: r.id, Mode: mode})
		_ = handler(schema.StreamEvent{Type: schema.StreamEventDone, Content: output, AgentID: r.id, Mode: mode})
	}
	if audit != nil {
		fallbackUsed = effectiveFallbackUsed(r.llm, fallbackUsed)
		audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "completed_after_iteration_budget", Detail: output, DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: fallbackUsed})
	}
	structured := buildStructuredSections(output, state.MatchedSkill, collectedResults, mode, fallbackUsed)
	findings, changes, verification := buildStructuredArtifacts(output, collectedResults, mode)
	return schema.AgentResult{Output: output, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: state.Profile.Model, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification}, nil
}

func (r *AgentRunner) finalizeWithInvalidToolArguments(state agentConversationState, collectedResults []schema.ToolResult, fallbackUsed bool, iteration int, handler func(event schema.StreamEvent) error, audit *runtime.AuditLogger) (schema.AgentResult, error) {
	mode := state.Mode
	if mode == "" {
		mode = state.Profile.Mode
	}
	output := "The model produced incomplete tool-call JSON repeatedly, so I stopped before executing the partial tool call. No additional tools were run after the last completed result."
	if len(collectedResults) > 0 {
		output = buildLocalBudgetSummary(collectedResults)
	}
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventText, Content: output, AgentID: r.id, Mode: mode})
		_ = handler(schema.StreamEvent{Type: schema.StreamEventDone, Content: output, AgentID: r.id, Mode: mode})
	}
	if audit != nil {
		fallbackUsed = effectiveFallbackUsed(r.llm, fallbackUsed)
		audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "invalid_tool_arguments_stopped", Detail: output, DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: fallbackUsed})
	}
	structured := buildStructuredSections(output, state.MatchedSkill, collectedResults, mode, fallbackUsed)
	findings, changes, verification := buildStructuredArtifacts(output, collectedResults, mode)
	return schema.AgentResult{Output: output, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: state.Profile.Model, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification}, nil
}

func dynamicContextMessages(memoryText string, snapshot session.Snapshot, currentInput string, messages []schema.Message) []schema.Message {
	blocks := make([]string, 0, 2)
	if text := strings.TrimSpace(memoryText); text != "" {
		blocks = append(blocks, text)
	}
	if text := sessionContextPromptText(snapshot, currentInput); text != "" {
		blocks = append(blocks, text)
	}
	out := make([]schema.Message, 0, len(messages)+1)
	if len(blocks) > 0 {
		out = append(out, schema.Message{
			Role:    "user",
			Content: strings.Join(blocks, "\n\n") + "\nUse this dynamic context only when it is relevant to the current request.",
		})
	}
	out = append(out, messages...)
	return out
}

func buildLocalBudgetSummary(results []schema.ToolResult) string {
	if len(results) == 0 {
		return "Tool iteration budget reached before the model produced a final no-tool summary. No tool calls completed in this turn."
	}
	counts := make(map[string]int)
	for _, result := range results {
		name := strings.TrimSpace(result.ToolName)
		if name == "" {
			name = "tool"
		}
		counts[name]++
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		if counts[name] == 1 {
			parts = append(parts, name)
			continue
		}
		parts = append(parts, fmt.Sprintf("%s x%d", name, counts[name]))
	}
	return fmt.Sprintf("Tool iteration budget reached before the model produced a final no-tool summary. Completed tool calls: %s. I stopped before making additional tool calls.", strings.Join(parts, ", "))
}

func toolObservationDigestMessage(results []schema.ToolResult) string {
	if len(results) < 3 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Tool observation digest:\n")
	start := len(results) - 6
	if start < 0 {
		start = 0
	}
	if start > 0 {
		fmt.Fprintf(&builder, "- %d earlier tool observations omitted from this digest; full observations remain above or in artifacts.\n", start)
	}
	for _, result := range results[start:] {
		status := "ok"
		if result.Suspended {
			status = "suspended"
		} else if result.Denied {
			status = "denied"
		} else if result.IsError {
			status = "error"
		}
		fmt.Fprintf(&builder, "- %s (%s): %s\n", fallbackToolResultName(result.ToolName), status, truncateSummary(result.Content))
	}
	builder.WriteString("Use this digest to avoid rereading or restating earlier tool output unless exact artifact content is needed.")
	return builder.String()
}

func toolCallByID(calls []schema.ToolCall, id string) (schema.ToolCall, bool) {
	for _, call := range calls {
		if call.ID == id {
			return call, true
		}
	}
	return schema.ToolCall{}, false
}

func shouldRunFinalTurnToolCalls(execCtx ExecutionContext, calls []schema.ToolCall) bool {
	for _, call := range calls {
		tool, err := execCtx.validateToolCall(call)
		if err != nil {
			continue
		}
		if config.ToolKind(execCtx.toolKind(tool)) != config.ToolKindRead {
			return true
		}
	}
	return false
}

type toolCallBatchProfile struct {
	ReadOnly         bool
	Mutating         bool
	ReadCalls        int
	ContentReadCalls int
	DiscoverySeen    bool
	Valid            int
}

type actionNudgeTracker struct {
	enabled             bool
	sent                bool
	mutationSeen        bool
	readOnlyBatches     int
	readOnlyResultCount int
}

func newActionNudgeTracker(profile config.AgentProfile, mode string, skill *schema.Skill, input string) *actionNudgeTracker {
	return &actionNudgeTracker{
		enabled: profileCanMutate(profile) && isImplementationActionRequest(profile, mode, skill, input),
	}
}

func (t *actionNudgeTracker) Next(batch toolCallBatchProfile, resultCount int) (string, bool) {
	if t == nil || !t.enabled || t.sent {
		return "", false
	}
	if batch.Mutating {
		t.mutationSeen = true
		return "", false
	}
	if t.mutationSeen || !batch.ReadOnly || resultCount <= 0 {
		return "", false
	}
	t.readOnlyBatches++
	t.readOnlyResultCount += resultCount
	if batch.DiscoverySeen && batch.ContentReadCalls > 0 {
		t.sent = true
		return implementationActionNudgePrompt(), true
	}
	if t.readOnlyBatches < 2 && t.readOnlyResultCount < 2 {
		return "", false
	}
	t.sent = true
	return implementationActionNudgePrompt(), true
}

func classifyToolCallBatch(calls []schema.ToolCall, execCtx ExecutionContext) toolCallBatchProfile {
	profile := toolCallBatchProfile{}
	for _, call := range calls {
		tool, err := execCtx.validateToolCall(call)
		if err != nil {
			continue
		}
		profile.Valid++
		toolName := strings.ToLower(strings.TrimSpace(tool.Name))
		switch config.ToolKind(execCtx.toolKind(tool)) {
		case config.ToolKindWrite, config.ToolKindExec:
			profile.Mutating = true
		case config.ToolKindRead:
			profile.ReadCalls++
			if isImplementationDiscoveryTool(toolName) {
				profile.DiscoverySeen = true
			}
			if isImplementationContentReadTool(toolName) {
				profile.ContentReadCalls++
			}
		}
	}
	profile.ReadOnly = profile.Valid > 0 && profile.ReadCalls == profile.Valid && !profile.Mutating
	return profile
}

func isImplementationDiscoveryTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "list_dir", "list_tree", "search_files", "file_info":
		return true
	default:
		return false
	}
}

func isImplementationContentReadTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file", "read_note":
		return true
	default:
		return false
	}
}

func isImplementationActionRequest(profile config.AgentProfile, mode string, skill *schema.Skill, input string) bool {
	if strings.EqualFold(strings.TrimSpace(mode), "fix") || strings.EqualFold(strings.TrimSpace(profile.Mode), "fix") {
		return true
	}
	if skill != nil {
		skillText := strings.ToLower(strings.Join([]string{skill.Name, skill.Description, skill.Mode}, " "))
		for _, keyword := range []string{"code-writing", "implement", "implementation", "fix", "modify", "write", "refactor", "build", "create", "实现", "修复", "修改", "重构", "开发"} {
			if strings.Contains(skillText, keyword) {
				return true
			}
		}
	}
	text := strings.ToLower(input)
	for _, keyword := range []string{
		"implement", "fix", "modify", "update", "add", "extend", "expand", "enhance", "optimize", "improve", "debug", "patch", "refactor", "rewrite", "edit", "build", "create", "write code",
		"实现", "修复", "修改", "新增", "添加", "拓展", "扩展", "增强", "优化", "完善", "重构", "改造", "调整", "开发", "写个", "写一个",
	} {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}
func implementationActionNudgePrompt() string {
	return "Enough workspace context has been gathered for this implementation request. Stop broad inspection now. If the relevant files are identified, make the smallest safe write/exec changes next; only read more if one specific missing detail blocks the edit. Do not repeat the plan or restate proposed features without acting."
}

func ensureMinimumToolIterations(profile config.AgentProfile) config.AgentProfile {
	if profile.MaxIterations <= 0 {
		profile.MaxIterations = 1
	}
	if profile.MaxIterations >= 2 || !profileCanMutate(profile) {
		return profile
	}
	profile.MaxIterations = 2
	return profile
}

func profileCanMutate(profile config.AgentProfile) bool {
	if strings.EqualFold(profile.Mode, "fix") {
		return true
	}
	for _, kind := range profile.AllowedToolKinds {
		switch config.ToolKind(kind) {
		case config.ToolKindWrite, config.ToolKindExec, config.ToolKindNetwork, config.ToolKindUnknown:
			return true
		}
	}
	for _, tool := range profile.AllowedTools {
		name := strings.ToLower(strings.TrimSpace(tool))
		if strings.Contains(name, "write") || strings.Contains(name, "edit") || strings.Contains(name, "delete") || strings.Contains(name, "exec") || strings.Contains(name, "run") {
			return true
		}
	}
	return len(profile.AllowedToolKinds) == 0 && len(profile.AllowedTools) == 0
}

func summarizeToolResultArguments(result schema.ToolResult, calls []schema.ToolCall) string {
	call, ok := toolCallByID(calls, result.CallID)
	if !ok {
		return ""
	}
	return summarizeToolArguments(call.Arguments)
}

func summarizeToolResultForSession(result schema.ToolResult) string {
	status := "done"
	if result.Suspended {
		status = "suspended"
	} else if result.Denied {
		status = "denied"
	} else if result.IsError {
		status = "error"
	}
	return fmt.Sprintf("%s: %s %s", result.ToolName, status, truncateSummary(result.Content))
}

func skillName(skill *schema.Skill) string {
	if skill == nil {
		return ""
	}
	return skill.Name
}

func auditEntries(audit *runtime.AuditLogger) []schema.AuditEntry {
	if audit == nil {
		return nil
	}
	return audit.Entries()
}

func effectiveFallbackUsed(llm interfaces.LLMClient, fallbackUsed bool) bool {
	if detector, ok := llm.(interface{ FallbackUsed() bool }); ok && detector.FallbackUsed() {
		return true
	}
	return fallbackUsed
}

type modelWaitStatusContext struct {
	AfterToolResults bool
	FinalSummary     bool
}

func emitModelWaitStatus(handler func(event schema.StreamEvent) error, agentID, mode string, waitContext modelWaitStatusContext) {
	if handler == nil {
		return
	}
	content := "waiting for model response..."
	switch {
	case waitContext.FinalSummary:
		content = "waiting for model final summary without additional tool calls..."
	case waitContext.AfterToolResults:
		content = "waiting for model response after tool results..."
	}
	_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: content, AgentID: agentID, Mode: mode, NeedsAction: true})
}

func emitTaskStage(handler func(event schema.StreamEvent) error, sessionState *session.State, agentID, mode, stage, detail string) {
	stage = strings.ToLower(strings.TrimSpace(stage))
	if stage == "" {
		return
	}
	detail = strings.TrimSpace(detail)
	if sessionState != nil {
		sessionState.SetTaskStage(session.TaskStageSnapshot{Stage: stage, AgentID: agentID, Mode: mode, Detail: detail})
	}
	if handler == nil {
		return
	}
	_ = handler(schema.StreamEvent{
		Type:      schema.StreamEventTaskStage,
		Content:   detail,
		AgentID:   agentID,
		Mode:      mode,
		TaskStage: stage,
	})
}

func initialTaskStageForMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan":
		return "plan"
	default:
		return "inspect"
	}
}

func taskStageForToolCalls(calls []schema.ToolCall, execCtx ExecutionContext) string {
	for _, call := range calls {
		tool, err := execCtx.validateToolCall(call)
		if err != nil {
			continue
		}
		switch config.ToolKind(execCtx.toolKind(tool)) {
		case config.ToolKindWrite, config.ToolKindExec:
			return "modify"
		}
	}
	return "inspect"
}

func summarizeToolCallStage(calls []schema.ToolCall, execCtx ExecutionContext) string {
	if len(calls) == 0 {
		return ""
	}
	stage := taskStageForToolCalls(calls, execCtx)
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		if tool, err := execCtx.validateToolCall(call); err == nil && strings.TrimSpace(tool.Server) != "" {
			name = tool.Server + "/" + tool.Name
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 4 {
		names = append(names[:4], fmt.Sprintf("+%d more", len(names)-4))
	}
	if len(names) == 0 {
		return "running tools"
	}
	switch stage {
	case "modify":
		return "running write/exec tools: " + strings.Join(names, ", ")
	default:
		return "gathering context with tools: " + strings.Join(names, ", ")
	}
}

func emitTokenUsage(handler func(event schema.StreamEvent) error, state *session.State, agentID, mode string, usage schema.TokenUsage) {
	if !hasTokenUsage(usage) {
		return
	}
	if state != nil {
		snapshot := state.Snapshot()
		state.AddTokenUsage(schema.TokenUsageSample{
			AgentID:      agentID,
			Mode:         mode,
			WorkflowName: snapshot.Workflow.Name,
			TaskStage:    snapshot.TaskStage.Stage,
			PromptTokens: usage.PromptTokens,
			OutputTokens: usage.OutputTokens,
			CachedTokens: usage.CachedTokens,
			TotalTokens:  usage.PromptTokens + usage.OutputTokens,
		})
	}
	if handler == nil {
		return
	}
	_ = handler(schema.StreamEvent{
		Type:         schema.StreamEventTokenUsage,
		Content:      "token_usage",
		AgentID:      agentID,
		Mode:         mode,
		PromptTokens: usage.PromptTokens,
		OutputTokens: usage.OutputTokens,
		CachedTokens: usage.CachedTokens,
	})
}

func hasTokenUsage(usage schema.TokenUsage) bool {
	return usage.PromptTokens != 0 || usage.OutputTokens != 0 || usage.CachedTokens != 0
}

func runAgentChatWithRecovery(ctx context.Context, llm interfaces.LLMClient, request schema.ChatRequest, agentID, mode string, handler func(event schema.StreamEvent) error, streamedText *bool, messages *[]schema.Message) (schema.ChatResponse, error) {
	const maxToolArgumentRecoveryAttempts = 2
	currentRequest := request
	for attempt := 0; ; attempt++ {
		var bufferedEvents []schema.StreamEvent
		resp, err := llm.StreamChat(ctx, currentRequest, func(event schema.StreamEvent) error {
			event.AgentID = agentID
			event.Mode = mode
			switch event.Type {
			case schema.StreamEventText:
				bufferedEvents = append(bufferedEvents, event)
				return nil
			case schema.StreamEventToolCall:
				return nil
			}
			if handler != nil {
				return handler(event)
			}
			return nil
		})
		if err == nil {
			if len(resp.ToolCalls) == 0 {
				for _, event := range bufferedEvents {
					if streamedText != nil && event.Type == schema.StreamEventText {
						*streamedText = true
					}
					if handler != nil {
						if handleErr := handler(event); handleErr != nil {
							return schema.ChatResponse{}, handleErr
						}
					}
				}
			}
			return resp, nil
		}
		if !isRecoverableToolArgumentError(err) || attempt >= maxToolArgumentRecoveryAttempts {
			return schema.ChatResponse{}, err
		}
		if handler != nil {
			_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "detected incomplete tool arguments; cannot continue with the partial tool call", AgentID: agentID, Mode: mode})
			_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "recovering from incomplete tool arguments; requesting complete valid tool-call JSON", AgentID: agentID, Mode: mode})
		}
		if streamedText != nil {
			*streamedText = false
		}
		recoveryMessage := schema.Message{Role: "user", Content: buildToolArgumentRecoveryPrompt(err, attempt+1, maxToolArgumentRecoveryAttempts)}
		if messages != nil {
			*messages = append(*messages, recoveryMessage)
			currentRequest.Messages = *messages
			continue
		}
		currentRequest.Messages = append(schema.CopyMessages(currentRequest.Messages), recoveryMessage)
	}
}

func assistantToolMessage(resp schema.ChatResponse) schema.Message {
	message := schema.CopyMessage(resp.Message)
	if strings.TrimSpace(message.Role) == "" {
		message.Role = "assistant"
	}
	if len(message.ToolCalls) == 0 && len(resp.ToolCalls) > 0 {
		message.ToolCalls = schema.CopyToolCalls(resp.ToolCalls)
	}
	return message
}

func isRecoverableToolArgumentError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "invalid tool arguments") && (strings.Contains(lower, "unexpected end of json input") || strings.Contains(lower, "tool call has invalid arguments"))
}

func buildToolArgumentRecoveryPrompt(err error, attempt, maxAttempts int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "The previous assistant response attempted a tool call, but its arguments were malformed or incomplete JSON: %s\n", err)
	if attempt >= maxAttempts {
		builder.WriteString("This is the final recovery attempt.\n")
	}
	builder.WriteString("Recovery rules:\n")
	builder.WriteString("- Do not repeat explanatory prose or the previous plan.\n")
	builder.WriteString("- If the next action is still a tool call, emit exactly one complete tool call with valid JSON arguments.\n")
	builder.WriteString("- Include every required field from the tool schema; do not leave partial objects, dangling strings, or trailing commas.\n")
	builder.WriteString("- Reuse the same intended tool only if you can reconstruct the arguments safely from the conversation.\n")
	builder.WriteString("- If you cannot reconstruct the arguments safely, stop calling tools and provide a concise final text response explaining what is missing.\n")
	return builder.String()
}

func buildStructuredSections(output string, skill *schema.Skill, results []schema.ToolResult, mode string, fallbackUsed bool) []schema.StructuredSection {
	trimmedOutput := strings.TrimSpace(output)
	sections := []schema.StructuredSection{{Title: "Summary", Kind: "summary", Summary: trimmedOutput}}
	if fallbackUsed {
		sections = append(sections, schema.StructuredSection{Title: "Provider", Kind: "provider", Items: []string{"fallback"}})
	}
	if mode != "" {
		sections = append(sections, schema.StructuredSection{Title: "Mode", Kind: "mode", Items: []string{mode}})
	}
	if skill != nil {
		sections = append(sections, schema.StructuredSection{Title: "Skill", Kind: "skill", Items: []string{skill.Name}})
		if skill.OutputKind != "" {
			sections = append(sections, schema.StructuredSection{Title: "Output", Kind: "output_kind", Items: []string{skill.OutputKind}})
		}
	}
	if len(results) > 0 {
		items := make([]string, 0, len(results))
		for _, result := range results {
			status := "ok"
			if result.Denied {
				status = "denied"
			} else if result.IsError {
				status = "error"
			}
			items = append(items, fmt.Sprintf("%s (%s)", result.ToolName, status))
		}
		sections = append(sections, schema.StructuredSection{Title: "Tools", Kind: "tools", Items: items})
	}
	if shouldAddReflectionSection(trimmedOutput, mode) {
		sections = append(sections, schema.StructuredSection{Title: "Reflection", Kind: "reflection", Summary: reflectionSummary(trimmedOutput, mode)})
	}
	switch mode {
	case "plan":
		sections = append(sections, schema.StructuredSection{Title: "Plan", Kind: "workflow", Summary: "Structured for planning-oriented output."})
	case "audit":
		sections = append(sections, schema.StructuredSection{Title: "Audit", Kind: "workflow", Summary: "Structured for audit-oriented findings and evidence."})
	case "fix":
		sections = append(sections, schema.StructuredSection{Title: "Fix", Kind: "workflow", Summary: "Structured for remediation-oriented output."})
	}
	return sections
}

func shouldAddReflectionSection(output, mode string) bool {
	if strings.TrimSpace(output) == "" {
		return false
	}
	switch mode {
	case "audit", "fix":
	default:
		return false
	}
	lower := strings.ToLower(output)
	return strings.Contains(lower, "sql injection") ||
		strings.Contains(lower, "security") ||
		strings.Contains(lower, "high-risk") ||
		strings.Contains(lower, "high risk") ||
		strings.Contains(lower, "auth") ||
		strings.Contains(lower, "authentication")
}

func reflectionSummary(output, mode string) string {
	if mode == "audit" {
		return fmt.Sprintf("High-risk audit output detected; performed a self-check on the reported findings and verification cues: %s", truncateSummary(output))
	}
	return fmt.Sprintf("High-risk fix output detected; performed a self-check on the claimed remediation and affected files: %s", truncateSummary(output))
}

func buildStructuredArtifacts(output string, results []schema.ToolResult, mode string) ([]schema.Finding, []schema.Change, []schema.Verification) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil, nil
	}
	files := extractMentionedFiles(trimmed)
	verification := []schema.Verification{{Kind: "response", Status: verificationStatus(results), Detail: trimmed}}
	if mode == "plan" {
		verification = append(verification, schema.Verification{Kind: "plan", Status: "proposed", Detail: fmt.Sprintf("Plan output captured for review: %s", truncateSummary(trimmed))})
	}
	if shouldAddReflectionSection(trimmed, mode) {
		verification = append(verification, schema.Verification{Kind: "reflection", Status: "performed", Detail: reflectionSummary(trimmed, mode)})
	}
	if len(results) > 0 {
		for _, result := range results {
			status := "passed"
			if result.Denied {
				status = "blocked"
			} else if result.IsError {
				status = "failed"
			}
			verification = append(verification, schema.Verification{Kind: "tool", Status: status, Detail: fmt.Sprintf("%s: %s", result.ToolName, truncateSummary(result.Content))})
		}
	}
	switch mode {
	case "audit":
		return []schema.Finding{{Severity: inferFindingSeverity(trimmed), Summary: trimmed, Files: files}}, nil, verification
	case "fix":
		return nil, []schema.Change{{Summary: trimmed, Files: files}}, verification
	default:
		return nil, nil, verification
	}
}

func verificationStatus(results []schema.ToolResult) string {
	for _, result := range results {
		if result.Denied {
			return "blocked"
		}
		if result.IsError {
			return "failed"
		}
	}
	return "passed"
}

func inferFindingSeverity(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "critical"):
		return "critical"
	case strings.Contains(lower, "sql injection"), strings.Contains(lower, "high risk"), strings.Contains(lower, "severity: high"):
		return "high"
	case strings.Contains(lower, "medium"), strings.Contains(lower, "warning"):
		return "medium"
	default:
		return "info"
	}
}

func extractMentionedFiles(output string) []string {
	fields := strings.FieldsFunc(output, func(r rune) bool {
		switch r {
		case ' ', '\n', '\r', '\t', ',', ';', ':', '(', ')', '[', ']', '"', '\'':
			return true
		default:
			return false
		}
	})
	seen := make(map[string]struct{})
	files := make([]string, 0)
	for _, field := range fields {
		candidate := strings.TrimSpace(field)
		candidate = strings.Trim(candidate, ".")
		if candidate == "" || !strings.Contains(candidate, "/") || !strings.Contains(candidate, ".") {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		files = append(files, candidate)
	}
	return files
}

func truncateSummary(input string) string {
	input = strings.TrimSpace(strings.ReplaceAll(input, "\n", " "))
	if len(input) <= 120 {
		return input
	}
	return input[:117] + "..."
}
