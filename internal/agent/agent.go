package agent

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/promptguard"
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
	Profile                config.AgentProfile
	MatchedSkill           *schema.Skill
	SystemPrompt           string
	MemoryText             string
	SessionSnapshot        session.Snapshot
	PromptContext          promptBudgetContext
	Mode                   string
	Input                  string
	Messages               []schema.Message
	CollectedResults       []schema.ToolResult
	Tools                  []schema.Tool
	StartedAt              time.Time
	FallbackUsed           bool
	StreamedText           bool
	ToolOperations         workflowToolOperationScope
	ApprovedPlanScope      bool
	ChangeSliceEnforcement bool
	ChangeSlices           []session.WorkflowChangeSliceSnapshot
	StageToolPolicy        workflowStageToolPolicy
	Transcript             *[]schema.Message
	FailedCallSignatures   map[string]int
	// MaxModelCalls caps how many model calls one stage may spend per entry;
	// 0 disables the cap.
	MaxModelCalls int
	// StageCallBaseline is the stage's call count when this entry started, so a
	// second repair round gets its own budget instead of inheriting the first.
	StageCallBaseline int
	// MaxStageTotalCalls caps every call this stage may spend in the whole run,
	// including retries and continuations; 0 disables the cap.
	MaxStageTotalCalls int
}

type agentRunOptions struct {
	AllowedTools []string
	// SystemPromptOverride replaces the per-agent prompt when a workflow shares
	// one session across stages: an identical prompt is what makes the provider
	// prefix cacheable from one stage to the next.
	SystemPromptOverride string
	// MaxModelCalls bounds one stage entry's model calls.
	MaxModelCalls int
	// MaxStageTotalCalls bounds this stage across the whole run.
	MaxStageTotalCalls int
	// InitialMessages seeds the conversation with a previous stage's transcript
	// so a workflow does not re-discover context it already paid for.
	InitialMessages        []schema.Message
	InitialToolResults     []schema.ToolResult
	ToolOperations         workflowToolOperationScope
	IsolatePromptContext   bool
	ApprovedPlanScope      bool
	ChangeSliceEnforcement bool
	ChangeSlices           []session.WorkflowChangeSliceSnapshot
	StageToolPolicy        workflowStageToolPolicy
}

type workflowToolOperationScope struct {
	RunID    string
	Workflow string
	Stage    string
	Enabled  bool
}

const maxModelOutputContinuations = 3

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
		Pricing:          a.cfg.LLM.Pricing,
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
	profile = applyContextToolBoundary(ctx, profile)
	profile = ensureMinimumToolIterations(profile)
	if state != nil {
		if !options.IsolatePromptContext {
			state.SetActiveAgent(r.id)
			if state.Mode() == "" || state.Mode() == "chat" {
				state.SetMode(profile.Mode)
			}
			state.AddPrompt(input)
			state.SetLastSkillMatch(matchedSkill, matchDiagnostic)
		}
	}
	snapshot := session.Snapshot{}
	if state != nil {
		snapshot = state.Snapshot()
	}
	if options.IsolatePromptContext {
		snapshot.RecentPrompts = nil
		snapshot.RecentTools = nil
		snapshot.LastSkill = ""
		snapshot.LastSkillMatch = nil
	}
	mode := profile.Mode
	if !options.IsolatePromptContext && snapshot.Mode != "" {
		mode = snapshot.Mode
	}
	if mode == "" {
		mode = profile.Mode
	}
	memoryText := ""
	promptContext := promptBudgetContext{}
	if !options.IsolatePromptContext && runtimeRef != nil && runtimeRef.MemoryStore() != nil {
		if memoryContext, err := runtimeRef.MemoryStore().PromptContextFresh(ctx, input, agentRunIDFromContext(ctx)); err == nil {
			memoryText = memoryContext.PromptText()
			promptContext.Memory = &memoryContext
		}
	}
	repositoryInstructions, repositoryInstructionPaths, repositoryInstructionErr := repositoryInstructionPrompt(runtimeRef)
	if repositoryInstructionErr != nil && audit != nil {
		audit.Record(schema.AuditEntry{Type: "repository_instructions", AgentID: r.id, Outcome: "error", Detail: repositoryInstructionErr.Error()})
	}
	systemPrompt := BuildSystemPromptWithRepository(profile, matchedSkill, snapshot, repositoryInstructions)
	if override := strings.TrimSpace(options.SystemPromptOverride); override != "" {
		systemPrompt = override
	}
	startedAt := time.Now()
	fallbackUsed := effectiveFallbackUsed(r.llm, r.fallbackUsed)
	if audit != nil {
		audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(matchedSkill), Outcome: "started", Detail: input})
	}
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: fmt.Sprintf("agent=%s mode=%s", r.id, mode), AgentID: r.id, Mode: mode})
		if len(repositoryInstructionPaths) > 0 {
			_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "repository_instructions=" + strings.Join(repositoryInstructionPaths, ","), AgentID: r.id, Mode: mode})
		}
		if repositoryInstructionErr != nil {
			_ = handler(schema.StreamEvent{Type: schema.StreamEventError, Content: "repository instructions were not loaded: " + repositoryInstructionErr.Error(), AgentID: r.id, Mode: mode, IsError: true})
		}
	}
	emitTaskStage(handler, state, r.id, mode, initialTaskStageForMode(mode), "starting request")
	inputMessage := taskInputMessage(input, taskAttachmentsFromContext(ctx))
	seedMessages := make([]schema.Message, 0, len(options.InitialMessages)+1)
	seedMessages = append(seedMessages, options.InitialMessages...)
	seedMessages = append(seedMessages, inputMessage)
	var transcript []schema.Message
	result, err := r.continueConversation(ctx, agentConversationState{
		Profile:                profile,
		MatchedSkill:           matchedSkill,
		SystemPrompt:           systemPrompt,
		MemoryText:             memoryText,
		SessionSnapshot:        snapshot,
		PromptContext:          promptContext,
		Mode:                   mode,
		Input:                  input,
		Messages:               seedMessages,
		CollectedResults:       append([]schema.ToolResult(nil), options.InitialToolResults...),
		Tools:                  tools,
		StartedAt:              startedAt,
		FallbackUsed:           fallbackUsed,
		ToolOperations:         options.ToolOperations,
		ApprovedPlanScope:      options.ApprovedPlanScope,
		ChangeSliceEnforcement: options.ChangeSliceEnforcement,
		ChangeSlices:           append([]session.WorkflowChangeSliceSnapshot(nil), options.ChangeSlices...),
		StageToolPolicy:        options.StageToolPolicy,
		Transcript:             &transcript,
		FailedCallSignatures:   map[string]int{},
		MaxModelCalls:          options.MaxModelCalls,
		MaxStageTotalCalls:     options.MaxStageTotalCalls,
	}, skills, mcp, state, audit, runtimeRef, handler)
	if err == nil {
		result.Messages = capConversationTranscript(transcript, maxSharedTranscriptMessages)
	}
	return result, err
}

// maxSharedTranscriptMessages bounds the transcript handed to the next stage:
// enough to keep decisions and tool observations, small enough not to replay a
// whole stage into every later request.
const maxSharedTranscriptMessages = 40

func capConversationTranscript(messages []schema.Message, limit int) []schema.Message {
	if limit <= 0 || len(messages) <= limit {
		return append([]schema.Message(nil), messages...)
	}
	return append([]schema.Message(nil), messages[len(messages)-limit:]...)
}

type readOnlyToolsContextKey struct{}

// WithReadOnlyTools restricts every Agent and Workflow stage in this context to
// tools declared as read-only by the runtime tool policy.
func WithReadOnlyTools(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, readOnlyToolsContextKey{}, true)
}

func applyContextToolBoundary(ctx context.Context, profile config.AgentProfile) config.AgentProfile {
	if ctx == nil {
		return profile
	}
	readOnly, _ := ctx.Value(readOnlyToolsContextKey{}).(bool)
	if !readOnly {
		return profile
	}
	profile.AllowedToolKinds = intersectProfileToolKinds(profile.AllowedToolKinds, []string{string(config.ToolKindRead)})
	return profile
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
	controls := runControlsFromContext(ctx, runtimeRef)
	execCtx.RunPermissionMode = controls.PermissionMode
	execCtx.UntrustedInstructionsObserved = toolResultsContainUntrustedInstructions(collectedResults)
	execCtx.AgentRunID = agentRunIDFromContext(ctx)
	execCtx.TargetID = executionTargetIDFromContext(ctx)
	if execCtx.TargetID == "" && sessionState != nil && execCtx.AgentRunID != "" {
		if run, ok := sessionState.AgentRun(execCtx.AgentRunID); ok {
			execCtx.TargetID = run.TargetID
		}
	}
	if strings.TrimSpace(execCtx.TargetID) == "" {
		execCtx.TargetID = session.LocalTargetID
	}
	execCtx.Session = sessionState
	execCtx.WorkflowRunID = state.ToolOperations.RunID
	execCtx.WorkflowName = state.ToolOperations.Workflow
	execCtx.WorkflowStage = state.ToolOperations.Stage
	state.StageCallBaseline = workflowStageModelCallCount(execCtx)
	execCtx.IdempotentToolCalls = state.ToolOperations.Enabled
	execCtx.ApprovedPlanScope = state.ApprovedPlanScope
	execCtx.ChangeSliceEnforcement = state.ChangeSliceEnforcement
	execCtx.ChangeSlices = append([]session.WorkflowChangeSliceSnapshot(nil), state.ChangeSlices...)
	if runtimeRef != nil {
		execCtx.MemoryStore = runtimeRef.MemoryStore()
		execCtx.MemoryTaskID = fallbackText(execCtx.AgentRunID, runtimeRef.currentWorkflowRunID())
		execCtx.RiskPolicy = runtimeRef.toolRiskPolicy()
		execCtx.MCPServers = runtimeRef.MCPServerRefs()
		runtimeRef.applySessionApprovedTools(&execCtx)
		runtimeRef.applyAgentTaskApprovalScope(&execCtx)
	}
	promptTools := filterPromptTools(profile, state.Tools)
	promptToolContext := promptBudgetContextWithTools(state.PromptContext, profile, state.Tools)
	actionNudge := newActionNudgeTracker(profile, mode, state.MatchedSkill, state.Input)

	deliveryCheckpointSent := false
	budgetForcedFinal := false
	for i := 0; i <= profile.MaxIterations; i++ {
		iteration := i + 1
		finalResponseTurn := i == profile.MaxIterations
		totalStageCalls := workflowStageModelCallCount(execCtx)
		if state.MaxModelCalls > 0 || state.MaxStageTotalCalls > 0 {
			recordStageCapDiagnostic(execCtx, totalStageCalls, state.StageCallBaseline, state.MaxModelCalls, state.MaxStageTotalCalls)
		}
		if !finalResponseTurn && state.MaxStageTotalCalls > 0 && totalStageCalls >= state.MaxStageTotalCalls {
			finalResponseTurn = true
			budgetForcedFinal = true
			messages = append(messages, schema.Message{Role: "user", Content: stageCallBudgetNudge(state.MaxStageTotalCalls)})
			if execCtx.Session != nil && strings.TrimSpace(execCtx.WorkflowRunID) != "" {
				execCtx.Session.AppendWorkflowRunEvent(execCtx.WorkflowRunID, session.WorkflowRunEventSnapshot{
					Type:           "workflow_state",
					Stage:          execCtx.WorkflowStage,
					Content:        fmt.Sprintf("stage %s reached its %d-call total budget for this run; forcing the stage report", execCtx.WorkflowStage, state.MaxStageTotalCalls),
					WorkflowStatus: "running",
					NextStage:      execCtx.WorkflowStage,
					Reason:         "stage_total_model_call_budget",
				})
			}
		}
		if !finalResponseTurn && state.MaxModelCalls > 0 && totalStageCalls-state.StageCallBaseline >= state.MaxModelCalls {
			finalResponseTurn = true
			budgetForcedFinal = true
			messages = append(messages, schema.Message{Role: "user", Content: stageCallBudgetNudge(state.MaxModelCalls)})
			if execCtx.Session != nil && strings.TrimSpace(execCtx.WorkflowRunID) != "" {
				execCtx.Session.AppendWorkflowRunEvent(execCtx.WorkflowRunID, session.WorkflowRunEventSnapshot{
					Type:           "workflow_state",
					Stage:          execCtx.WorkflowStage,
					Content:        fmt.Sprintf("stage %s reached its %d model-call budget; forcing the stage report", execCtx.WorkflowStage, state.MaxModelCalls),
					WorkflowStatus: "running",
					NextStage:      execCtx.WorkflowStage,
					Reason:         "stage_model_call_budget",
				})
			}
		}
		// Mid-stage delivery checkpoint: long exploratory stages were measured
		// running 49 tool iterations before producing anything. Ask for a
		// deliverable early so the stage converges instead of exploring.
		if !deliveryCheckpointSent && profile.MaxIterations >= 8 && i > 0 && i == profile.MaxIterations*2/3 {
			deliveryCheckpointSent = true
			messages = append(messages, schema.Message{Role: "user", Content: deliveryCheckpointNudge(iteration, profile.MaxIterations)})
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: deliveryCheckpointNudge(iteration, profile.MaxIterations), AgentID: r.id, Mode: mode, Reason: "delivery_checkpoint", Severity: "warning"})
			}
		}
		// A single workflow run may re-enter stages and continuations many
		// times. Cap the total model calls at the execution layer so no path can
		// loop indefinitely, whatever the stage-level guards miss.
		if execCtx.Session != nil && strings.TrimSpace(execCtx.WorkflowRunID) != "" {
			const maxWorkflowRunModelCalls = 320
			if used := workflowRunModelCallCount(execCtx); used >= maxWorkflowRunModelCalls {
				execCtx.Session.AppendWorkflowRunEvent(execCtx.WorkflowRunID, session.WorkflowRunEventSnapshot{
					Type:           "workflow_state",
					Stage:          execCtx.WorkflowStage,
					Content:        fmt.Sprintf("run used %d model calls; stopping this stage for review", used),
					WorkflowStatus: "running",
					NextStage:      execCtx.WorkflowStage,
					Reason:         "model_call_budget_exhausted",
				})
				return r.finalizeIncompleteOutput(state, schema.ChatResponse{
					Message:    schema.Message{Role: "assistant", Content: fmt.Sprintf("这个运行已使用 %d 次模型调用，按上限停止；继续执行会从本阶段恢复。", used)},
					StopReason: schema.StopReasonLength,
				}, collectedResults, fallbackUsed, 0, iteration, handler, audit)
			}
			execCtx.Session.IncrementWorkflowRunStageCall(execCtx.WorkflowRunID, execCtx.WorkflowStage)
			execCtx.Session.AppendWorkflowRunEvent(execCtx.WorkflowRunID, session.WorkflowRunEventSnapshot{
				Type:           "model_call",
				Stage:          execCtx.WorkflowStage,
				WorkflowStatus: "running",
				NextStage:      execCtx.WorkflowStage,
			})
			// A single stuck slice must not absorb the whole run budget. Once every
			// write slice is blocked there is nothing left to implement, so stop
			// instead of spending the remaining calls on retries.
			if execCtx.enforceChangeSliceCallBudget() {
				return r.finalizeIncompleteOutput(state, schema.ChatResponse{
					Message:    schema.Message{Role: "assistant", Content: "本阶段的可写分片均已超出调用预算，停止继续尝试并交由验证/返修阶段处理。"},
					StopReason: schema.StopReasonLength,
				}, collectedResults, fallbackUsed, 0, iteration, handler, audit)
			}
		}
		if i > 0 && len(collectedResults) > 0 {
			emitTaskStage(handler, sessionState, r.id, mode, "summarize", "reasoning over tool observations")
		}
		emitModelWaitStatus(handler, r.id, mode, modelWaitStatusContext{AfterToolResults: i > 0 && len(collectedResults) > 0})
		var protocolRepairs int
		messages, protocolRepairs = normalizeToolConversationMessages(messages)
		if state.Transcript != nil {
			*state.Transcript = append((*state.Transcript)[:0], messages...)
		}
		if protocolRepairs > 0 && handler != nil {
			_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: fmt.Sprintf("repaired %d incomplete tool message chain entries before model request", protocolRepairs), AgentID: r.id, Mode: mode, NeedsAction: true, Reason: "tool_message_chain_repaired", Severity: "warning"})
		}
		providerMessages := messages
		if runtimeRef != nil {
			var err error
			providerMessages, err = runtimeRef.hydrateMessageAttachments(messages)
			if err != nil {
				return schema.AgentResult{}, err
			}
		}
		if err := requireVisionCapability(r.llm, providerMessages); err != nil {
			return schema.AgentResult{}, err
		}
		request := schema.ChatRequest{
			Model:           profile.Model,
			System:          executionTargetSystemPrompt(state.SystemPrompt, execCtx.TargetID),
			Messages:        providerMessages,
			Tools:           availableWorkflowStageTools(promptTools, collectedResults, execCtx, state.StageToolPolicy),
			Temperature:     profile.Temperature,
			MaxTokens:       profile.MaxTokens,
			ReasoningEffort: controls.ReasoningEffort,
		}
		emitPromptBudget(handler, sessionState, r.id, mode, request, state.MatchedSkill, len(state.Tools), promptToolContext)

		resp, err := runAgentChatWithRecovery(ctx, r.llm, request, r.id, mode, handler, &streamedText, &messages)
		if err != nil {
			if isRecoverableToolArgumentError(err) {
				return r.finalizeWithInvalidToolArguments(state, collectedResults, fallbackUsed, iteration, handler, audit)
			}
			if transientModelInterruptionAfterResults(ctx, err, collectedResults) {
				return r.finalizeWithProviderInterruption(ctx, state, messages, collectedResults, fallbackUsed, iteration, err, handler, audit, runtimeRef)
			}
			execCtx.recordAudit(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "llm_error", Detail: err.Error(), DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: effectiveFallbackUsed(r.llm, fallbackUsed)})
			return schema.AgentResult{}, fmt.Errorf("llm chat: %w", err)
		}
		fallbackUsed = fallbackUsed || resp.FallbackUsed
		emitTokenUsage(handler, sessionState, r.id, mode, resp, promptToolContext)

		if len(resp.ToolCalls) == 0 {
			completedResp, continuationCount, incomplete, err := r.continueOutputLimitResponse(ctx, r.llm, resp, request, messages, mode, sessionState, state.MatchedSkill, promptToolContext, len(state.Tools), collectedResults, fallbackUsed, iteration, handler, audit, &streamedText)
			if err != nil {
				return schema.AgentResult{}, err
			}
			resp = completedResp
			emitTaskStage(handler, sessionState, r.id, mode, "summarize", "preparing final response")
			if !streamedText && handler != nil && resp.Message.Content != "" {
				if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: resp.Message.Content, AgentID: r.id, Mode: mode}); err != nil {
					return schema.AgentResult{}, err
				}
			}
			if incomplete {
				return r.finalizeIncompleteOutput(state, resp, collectedResults, fallbackUsed, continuationCount, iteration, handler, audit)
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
			return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: profile.Model, StopReason: resp.StopReason, ContinuationCount: continuationCount, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification, ResponseMessage: schema.CopyMessage(resp.Message)}, nil
		}
		runFinalToolCalls := finalResponseTurn && shouldRunFinalTurnToolCalls(execCtx, resp.ToolCalls)
		if finalResponseTurn && !runFinalToolCalls {
			result, err := r.finalizeAfterIterationBudget(ctx, state, messages, collectedResults, fallbackUsed, iteration, handler, audit, runtimeRef)
			if budgetForcedFinal {
				result.StageBudgetExhausted = true
			}
			return result, err
		}

		assistantMessage := assistantToolMessage(resp)
		messages = append(messages, assistantMessage)
		messages = compactMessagesForConversation(messages)
		batchProfile := classifyToolCallBatch(resp.ToolCalls, execCtx)
		resumeMessages := schema.CopyMessages(messages)
		resumeCollected := append([]schema.ToolResult(nil), collectedResults...)
		emitTaskStage(handler, sessionState, r.id, mode, taskStageForToolCalls(resp.ToolCalls, execCtx), summarizeToolCallStage(resp.ToolCalls, execCtx))
		allowedCalls, blockedResults := enforceWorkflowStageToolPolicy(resp.ToolCalls, collectedResults, execCtx, state.StageToolPolicy)
		repeatedFailureBlocked := map[string]schema.ToolResult{}
		if state.FailedCallSignatures != nil {
			allowedAfterFailureGuard, guardBlocked := blockRepeatedFailingCalls(allowedCalls, state.FailedCallSignatures)
			allowedCalls = allowedAfterFailureGuard
			for id, result := range guardBlocked {
				blockedResults[id] = result
				repeatedFailureBlocked[id] = result
			}
		}
		var executedResults []schema.ToolResult
		if len(allowedCalls) > 0 {
			var err error
			executedResults, err = r.executor.RunToolCalls(ctx, execCtx, allowedCalls, runtimeRef, handler)
			if err != nil {
				if handler != nil {
					_ = handler(schema.StreamEvent{Type: schema.StreamEventError, Content: err.Error(), AgentID: r.id, Mode: mode, IsError: true})
				}
				return schema.AgentResult{}, err
			}
		}
		results := mergeWorkflowStageToolResults(resp.ToolCalls, executedResults, blockedResults)
		// Record the first few tool errors on the run so a stage that "produces
		// no mutations" can be diagnosed from durable state instead of guessing.
		if execCtx.Session != nil && strings.TrimSpace(execCtx.WorkflowRunID) != "" {
			recorded := 0
			for _, result := range results {
				if !result.IsError || recorded >= 6 {
					continue
				}
				recorded++
				detail := strings.TrimSpace(result.Content)
				if len(detail) > 300 {
					detail = detail[:300]
				}
				execCtx.Session.AppendWorkflowRunEvent(execCtx.WorkflowRunID, session.WorkflowRunEventSnapshot{
					Type:           "tool_error",
					Stage:          execCtx.WorkflowStage,
					Content:        fmt.Sprintf("%s: %s", result.ToolName, detail),
					WorkflowName:   execCtx.WorkflowName,
					WorkflowStatus: "running",
					NextStage:      execCtx.WorkflowStage,
					Reason:         "tool_error_observed",
				})
			}
		}
		if state.FailedCallSignatures != nil {
			recordFailedCallSignatures(results, resp.ToolCalls, state.FailedCallSignatures)
		}
		collectedResults = append(collectedResults, results...)
		suspendedCalls := make([]schema.ToolCall, 0)
		duplicateReplayCount := 0
		for _, result := range results {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(result.Summary)), "reused unchanged") {
				duplicateReplayCount++
			}
			assessment := promptguard.AssessUntrustedText(result.Content)
			if assessment.Suspicious {
				firstDetection := !execCtx.UntrustedInstructionsObserved
				execCtx.UntrustedInstructionsObserved = true
				if firstDetection && handler != nil {
					_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "检测到工具结果包含疑似指令注入；后续写入、执行和联网操作必须重新审批", AgentID: r.id, Mode: mode, NeedsAction: true, Reason: "untrusted_tool_data", Severity: "warning"})
				}
			}
			if sessionState != nil {
				summary := summarizeToolResultForSession(result)
				sessionState.AddToolSummary(summary)
			}
			if handler != nil {
				resultEvent := schema.StreamEvent{Type: schema.StreamEventToolResult, ToolName: result.ToolName, ToolCallID: result.CallID, ArgumentsSummary: summarizeToolResultArguments(result, resp.ToolCalls), Content: result.Content, AgentID: r.id, Mode: mode, IsError: result.IsError, Suspended: result.Suspended}
				if call, ok := toolCallByID(resp.ToolCalls, result.CallID); ok {
					resultEvent.ArgumentsHash = toolArgumentsHash(call.Arguments)
				}
				event := result.WithStreamEvent(resultEvent)
				if err := handler(event); err != nil {
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
			promptContent, _ = promptguard.ProtectToolResultContent(result.ToolName, promptContent)
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
		if duplicateReplayCount > 0 {
			messages = append(messages, schema.Message{Role: "user", Content: duplicateToolReplayNudge(duplicateReplayCount)})
			if handler != nil {
				_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: duplicateToolReplayNudge(duplicateReplayCount), AgentID: r.id, Mode: mode, NeedsAction: true, Reason: "duplicate_tool_replay", Severity: "warning"})
			}
		}
		if runFinalToolCalls {
			return r.finalizeAfterIterationBudget(ctx, state, messages, collectedResults, fallbackUsed, iteration, handler, audit, runtimeRef)
		}
		if len(repeatedFailureBlocked) > 0 {
			messages = append(messages, schema.Message{Role: "user", Content: repeatedFailureNudge(repeatedFailureBlocked)})
		}
		if len(blockedResults) > 0 {
			messages = append(messages, schema.Message{Role: "user", Content: workflowStageToolPolicyNudge(state.StageToolPolicy, blockedResults)})
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

func duplicateToolReplayNudge(count int) string {
	return fmt.Sprintf("%d repeated unchanged tool result(s) were served from the run cache without executing again. Do not request those reads or validations again. Use the evidence already present and proceed directly to the next missing implementation or verification step.", count)
}

func toolResultsContainUntrustedInstructions(results []schema.ToolResult) bool {
	for _, result := range results {
		if promptguard.AssessUntrustedText(result.Content).Suspicious {
			return true
		}
	}
	return false
}

func (r *AgentRunner) finalizeAfterIterationBudget(ctx context.Context, state agentConversationState, messages []schema.Message, collectedResults []schema.ToolResult, fallbackUsed bool, iteration int, handler func(event schema.StreamEvent) error, audit *runtime.AuditLogger, runtimeRef *Runtime) (schema.AgentResult, error) {
	mode := state.Mode
	if mode == "" {
		mode = state.Profile.Mode
	}
	if runtimeRef != nil {
		runtimeRef.CaptureOrdinaryContinuationContext(agentRunIDFromContext(ctx), r.id, state.MatchedSkill, state.SystemPrompt, mode, messages, collectedResults)
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
		_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: "tool iteration budget reached; requesting final summary without additional tool calls", AgentID: r.id, Mode: mode, NeedsAction: true, Reason: "iteration_budget", Severity: "warning", BudgetScope: "iteration", StopReason: schema.StopReasonIterationBudget, Incomplete: true})
	}
	finalMessages := compactMessagesForSummaryPrompt(messages)
	finalMessages = append(finalMessages, schema.Message{Role: "user", Content: "Tool iteration budget has been reached. Do not call more tools. Do not repeat earlier planning text. Produce a concise final summary from the observations already in this conversation: completed work, verification already available, remaining risk, and the next concrete step if any."})
	streamedText := false
	request := schema.ChatRequest{
		Model:           summaryModel,
		System:          state.SystemPrompt,
		Messages:        finalMessages,
		Tools:           nil,
		Temperature:     summaryTemperature,
		MaxTokens:       summaryMaxTokens,
		ReasoningEffort: runControlsFromContext(ctx, runtimeRef).ReasoningEffort,
	}
	emitPromptBudget(handler, sessionState, r.id, mode, request, state.MatchedSkill, len(state.Tools), promptBudgetContextWithTools(state.PromptContext, state.Profile, state.Tools))
	emitModelWaitStatus(handler, r.id, mode, modelWaitStatusContext{FinalSummary: true})
	resp, err := runAgentChatWithRecovery(ctx, summaryLLM, request, r.id, mode, handler, &streamedText, &finalMessages)
	if err != nil {
		if audit != nil {
			audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "iteration_budget_summary_error", Detail: err.Error(), DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: effectiveFallbackUsed(summaryLLM, fallbackUsed)})
		}
		return r.finalizeWithLocalBudgetSummary(state, collectedResults, fallbackUsed, iteration, handler, audit)
	}
	emitTokenUsage(handler, sessionState, r.id, mode, resp, promptBudgetContextWithTools(state.PromptContext, state.Profile, state.Tools))
	completedResp, continuationCount, incomplete, err := r.continueOutputLimitResponse(ctx, summaryLLM, resp, request, finalMessages, mode, sessionState, state.MatchedSkill, promptBudgetContextWithTools(state.PromptContext, state.Profile, state.Tools), len(state.Tools), collectedResults, fallbackUsed, iteration, handler, audit, &streamedText)
	if err != nil {
		return schema.AgentResult{}, err
	}
	resp = completedResp
	if len(resp.ToolCalls) > 0 && strings.TrimSpace(resp.Message.Content) == "" {
		return r.finalizeWithLocalBudgetSummary(state, collectedResults, fallbackUsed, iteration, handler, audit)
	}
	if !streamedText && handler != nil && strings.TrimSpace(resp.Message.Content) != "" {
		if err := handler(schema.StreamEvent{Type: schema.StreamEventText, Content: resp.Message.Content, AgentID: r.id, Mode: mode}); err != nil {
			return schema.AgentResult{}, err
		}
	}
	incompleteReason := "tool iteration budget reached before the task could safely continue"
	if incomplete {
		incompleteReason = incompleteOutputReason(resp.StopReason, continuationCount)
	}
	stopReason := schema.StopReasonIterationBudget
	if incomplete {
		stopReason = fallbackText(resp.StopReason, schema.StopReasonIterationBudget)
	}
	emitIncompleteDone(handler, r.id, mode, resp.Message.Content, incompleteReason, "iteration_budget", "iteration", stopReason, continuationCount)
	if audit != nil {
		fallbackUsed = effectiveFallbackUsed(summaryLLM, fallbackUsed)
		audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "incomplete_after_iteration_budget", Detail: resp.Message.Content, DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: fallbackUsed, PromptTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.OutputTokens, CachedTokens: resp.Usage.CachedTokens})
	}
	structured := buildStructuredSections(resp.Message.Content, state.MatchedSkill, collectedResults, mode, fallbackUsed)
	findings, changes, verification := buildStructuredArtifacts(resp.Message.Content, collectedResults, mode)
	verification = append(verification, schema.Verification{Kind: "completion_integrity", Status: "blocked", Detail: incompleteReason})
	result := schema.AgentResult{Output: resp.Message.Content, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: summaryModel, StopReason: schema.StopReasonIterationBudget, Incomplete: true, IncompleteReason: incompleteReason, ContinuationCount: continuationCount, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification, ResponseMessage: schema.CopyMessage(resp.Message)}
	if incomplete {
		result.StopReason = resp.StopReason
	}
	return result, nil
}

func (r *AgentRunner) finalizeWithLocalBudgetSummary(state agentConversationState, collectedResults []schema.ToolResult, fallbackUsed bool, iteration int, handler func(event schema.StreamEvent) error, audit *runtime.AuditLogger) (schema.AgentResult, error) {
	mode := state.Mode
	if mode == "" {
		mode = state.Profile.Mode
	}
	output := buildLocalBudgetSummary(collectedResults)
	reason := "tool iteration budget reached before a final model response was available"
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventText, Content: output, AgentID: r.id, Mode: mode})
		emitIncompleteDone(handler, r.id, mode, output, reason, "iteration_budget", "iteration", schema.StopReasonIterationBudget, 0)
	}
	if audit != nil {
		fallbackUsed = effectiveFallbackUsed(r.llm, fallbackUsed)
		audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "incomplete_after_iteration_budget", Detail: output, DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: fallbackUsed})
	}
	structured := buildStructuredSections(output, state.MatchedSkill, collectedResults, mode, fallbackUsed)
	findings, changes, verification := buildStructuredArtifacts(output, collectedResults, mode)
	verification = append(verification, schema.Verification{Kind: "completion_integrity", Status: "blocked", Detail: reason})
	return schema.AgentResult{Output: output, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: state.Profile.Model, StopReason: schema.StopReasonIterationBudget, Incomplete: true, IncompleteReason: reason, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification}, nil
}

func transientModelInterruptionAfterResults(ctx context.Context, err error, results []schema.ToolResult) bool {
	return len(results) > 0 && workflowGraphStageErrorIsRetryable(ctx, err)
}

func (r *AgentRunner) finalizeWithProviderInterruption(ctx context.Context, state agentConversationState, messages []schema.Message, collectedResults []schema.ToolResult, fallbackUsed bool, iteration int, interruption error, handler func(event schema.StreamEvent) error, audit *runtime.AuditLogger, runtimeRef *Runtime) (schema.AgentResult, error) {
	mode := fallbackText(state.Mode, state.Profile.Mode)
	reason := "provider response was interrupted after tool results were collected"
	if len(collectedResults) == 0 {
		reason = "provider response was interrupted before tool execution"
	}
	if interruption != nil {
		reason += ": " + interruption.Error()
	}
	languageHint := state.Input
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			languageHint += "\n" + message.Content
		}
	}
	output := providerInterruptionSummary(languageHint, collectedResults)
	if runtimeRef != nil {
		runtimeRef.CaptureOrdinaryContinuationContext(agentRunIDFromContext(ctx), r.id, state.MatchedSkill, state.SystemPrompt, mode, messages, collectedResults)
	}
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: reason, AgentID: r.id, Mode: mode, NeedsAction: true, Reason: "provider_interruption", Severity: "warning", StopReason: schema.StopReasonError, Incomplete: true})
		_ = handler(schema.StreamEvent{Type: schema.StreamEventDone, Content: output, AgentID: r.id, Mode: mode, NeedsAction: true, Reason: "provider_interruption", Severity: "warning", StopReason: schema.StopReasonError, Incomplete: true})
	}
	if audit != nil {
		fallbackUsed = effectiveFallbackUsed(r.llm, fallbackUsed)
		audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "incomplete_provider_interruption", Detail: reason, DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: fallbackUsed})
	}
	structured := buildStructuredSections(output, state.MatchedSkill, collectedResults, mode, fallbackUsed)
	findings, changes, verification := buildStructuredArtifacts(output, collectedResults, mode)
	verification = append(verification, schema.Verification{Kind: "completion_integrity", Status: "blocked", Detail: reason})
	return schema.AgentResult{Output: output, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: state.Profile.Model, StopReason: schema.StopReasonError, Incomplete: true, IncompleteReason: reason, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification}, nil
}

func providerInterruptionSummary(input string, results []schema.ToolResult) string {
	names := make([]string, 0, len(results))
	seen := map[string]struct{}{}
	for _, result := range results {
		name := fallbackToolResultName(result.ToolName)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	tools := strings.Join(names, "、")
	for _, char := range input {
		if unicode.Is(unicode.Han, char) {
			if len(results) == 0 {
				return "模型服务暂时不可用，当前阶段已保存并暂停。点击“继续执行”可从当前阶段重试，先前已完成的阶段和文件变更会保留。"
			}
			return fmt.Sprintf("模型响应在工具执行后超时，本阶段已安全暂停，没有重放工具调用。已保留 %d 条工具结果（%s）；可点击“继续执行”从已保存的任务暂存区完成剩余实现和验证。", len(results), tools)
		}
	}
	if len(results) == 0 {
		return "The model service is temporarily unavailable. This stage is saved and paused; continue this stage while preserving completed stages and workspace changes."
	}
	return fmt.Sprintf("The model response timed out after tool execution. This stage paused safely without replaying tools. %d tool results were preserved (%s); use Continue task to finish the remaining implementation and verification in the saved task workspace.", len(results), strings.Join(names, ", "))
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
	reason := "model produced incomplete tool-call JSON repeatedly"
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventText, Content: output, AgentID: r.id, Mode: mode})
		emitIncompleteDone(handler, r.id, mode, output, reason, "invalid_tool_arguments", "tool_call", schema.StopReasonError, 0)
	}
	if audit != nil {
		fallbackUsed = effectiveFallbackUsed(r.llm, fallbackUsed)
		audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "invalid_tool_arguments_stopped", Detail: output, DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: fallbackUsed})
	}
	structured := buildStructuredSections(output, state.MatchedSkill, collectedResults, mode, fallbackUsed)
	findings, changes, verification := buildStructuredArtifacts(output, collectedResults, mode)
	verification = append(verification, schema.Verification{Kind: "completion_integrity", Status: "blocked", Detail: reason})
	return schema.AgentResult{Output: output, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: state.Profile.Model, StopReason: schema.StopReasonError, Incomplete: true, IncompleteReason: reason, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification}, nil
}

func (r *AgentRunner) continueOutputLimitResponse(ctx context.Context, llm interfaces.LLMClient, resp schema.ChatResponse, request schema.ChatRequest, messages []schema.Message, mode string, sessionState *session.State, skill *schema.Skill, promptContext promptBudgetContext, totalToolCount int, collectedResults []schema.ToolResult, fallbackUsed bool, iteration int, handler func(event schema.StreamEvent) error, audit *runtime.AuditLogger, streamedText *bool) (schema.ChatResponse, int, bool, error) {
	if !isOutputLimitStopReason(resp.StopReason) || len(resp.ToolCalls) > 0 {
		return resp, 0, false, nil
	}
	if strings.TrimSpace(mode) == "" {
		mode = r.profile.Mode
	}
	content := resp.Message.Content
	currentMessages := schema.CopyMessages(messages)
	stopReason := resp.StopReason
	responseFallbackUsed := resp.FallbackUsed
	var continuationCount int
	for continuationCount < maxModelOutputContinuations && isOutputLimitStopReason(stopReason) {
		continuationCount++
		emitOutputContinuationEvent(handler, r.id, mode, continuationCount, "model_output_truncated", stopReason, "model stopped at its output limit; continuing instead of marking the task complete")
		assistant := schema.CopyMessage(resp.Message)
		if strings.TrimSpace(assistant.Role) == "" {
			assistant.Role = "assistant"
		}
		assistant.Content = strings.TrimSpace(content)
		continuationPrompt := outputContinuationPrompt(continuationCount)
		if assistant.Content != "" || len(assistant.ToolCalls) > 0 {
			currentMessages = append(currentMessages, assistant, schema.Message{Role: "user", Content: continuationPrompt})
		} else if len(currentMessages) > 0 && strings.EqualFold(strings.TrimSpace(currentMessages[len(currentMessages)-1].Role), "user") {
			currentMessages[len(currentMessages)-1].Content = strings.TrimSpace(currentMessages[len(currentMessages)-1].Content) + "\n\n" + continuationPrompt
		} else {
			currentMessages = append(currentMessages, schema.Message{Role: "user", Content: continuationPrompt})
		}
		continuationRequest := request
		continuationRequest.Messages = compactMessagesForConversation(currentMessages)
		continuationRequest.Tools = nil
		emitPromptBudget(handler, sessionState, r.id, mode, continuationRequest, skill, totalToolCount, promptContext)
		emitModelWaitStatus(handler, r.id, mode, modelWaitStatusContext{FinalSummary: true})
		emitOutputContinuationEvent(handler, r.id, mode, continuationCount, "continuation_started", stopReason, "requesting the next segment of the truncated model output")
		if streamedText != nil {
			*streamedText = false
		}
		nextResp, err := runAgentChatWithRecovery(ctx, llm, continuationRequest, r.id, mode, handler, streamedText, &currentMessages)
		if err != nil {
			if audit != nil {
				audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(skill), Outcome: "continuation_error", Detail: err.Error(), Iteration: iteration, FallbackUsed: effectiveFallbackUsed(llm, fallbackUsed)})
			}
			return resp, continuationCount, true, fmt.Errorf("continue truncated output: %w", err)
		}
		responseFallbackUsed = responseFallbackUsed || nextResp.FallbackUsed
		emitTokenUsage(handler, sessionState, r.id, mode, nextResp, promptContext)
		if len(nextResp.ToolCalls) > 0 {
			resp.Message.Content = strings.TrimSpace(content)
			resp.StopReason = schema.StopReasonToolCalls
			resp.ToolCalls = nextResp.ToolCalls
			resp.FallbackUsed = responseFallbackUsed
			return resp, continuationCount, true, nil
		}
		nextContent := strings.TrimSpace(nextResp.Message.Content)
		if nextContent != "" {
			content = joinContinuationContent(content, nextContent)
		}
		stopReason = nextResp.StopReason
		resp = nextResp
		resp.FallbackUsed = responseFallbackUsed
		resp.Message.Content = content
		emitOutputContinuationEvent(handler, r.id, mode, continuationCount, "continuation_completed", stopReason, "received continuation segment")
	}
	if isOutputLimitStopReason(stopReason) {
		resp.Message.Content = strings.TrimSpace(content)
		resp.StopReason = stopReason
		resp.FallbackUsed = responseFallbackUsed
		return resp, continuationCount, true, nil
	}
	resp.Message.Content = strings.TrimSpace(content)
	resp.StopReason = normalizeCompletionStopReason(stopReason)
	resp.FallbackUsed = responseFallbackUsed
	return resp, continuationCount, false, nil
}

// workflowRunModelCallCount counts this run's recorded model calls so one run
// cannot loop forever across stage re-entries. It reads the durable usage
// ledger: session events are kept in a bounded window, so counting events
// silently stops increasing once the window is full.
// workflowStageModelCallCount counts model calls already spent by this stage,
// including continuations, so a stage budget cannot be bypassed by re-entering.
func workflowStageModelCallCount(execCtx ExecutionContext) int {
	if execCtx.Session == nil || strings.TrimSpace(execCtx.WorkflowRunID) == "" {
		return 0
	}
	run, ok := execCtx.Session.WorkflowRun(execCtx.WorkflowRunID)
	if !ok {
		return 0
	}
	stage := strings.TrimSpace(execCtx.WorkflowStage)
	if durable := execCtx.Session.WorkflowRunStageCallCount(execCtx.WorkflowRunID, stage); durable > 0 {
		return durable
	}
	recordCount := 0
	for _, record := range session.WorkflowUsageRecords(run) {
		if strings.TrimSpace(record.Stage) == stage {
			recordCount++
		}
	}
	// Usage records come from token_usage events, which do not always carry the
	// stage. Model-call events do, so use the larger count: under-counting made
	// the stage budget a no-op in measured runs.
	eventCount := 0
	for _, event := range run.Events {
		if strings.EqualFold(event.Type, "model_call") && strings.TrimSpace(event.Stage) == stage {
			eventCount++
		}
	}
	if eventCount > recordCount {
		return eventCount
	}
	return recordCount
}

func workflowRunModelCallCount(execCtx ExecutionContext) int {
	if execCtx.Session == nil || strings.TrimSpace(execCtx.WorkflowRunID) == "" {
		return 0
	}
	run, ok := execCtx.Session.WorkflowRun(execCtx.WorkflowRunID)
	if !ok {
		return 0
	}
	if records := session.WorkflowUsageRecords(run); len(records) > 0 {
		return len(records)
	}
	count := 0
	for _, event := range run.Events {
		if event.Type == "model_call" {
			count++
		}
	}
	return count
}

func (r *AgentRunner) finalizeIncompleteOutput(state agentConversationState, resp schema.ChatResponse, collectedResults []schema.ToolResult, fallbackUsed bool, continuationCount, iteration int, handler func(event schema.StreamEvent) error, audit *runtime.AuditLogger) (schema.AgentResult, error) {
	mode := state.Mode
	if mode == "" {
		mode = state.Profile.Mode
	}
	reason := incompleteOutputReason(resp.StopReason, continuationCount)
	if handler != nil {
		_ = handler(schema.StreamEvent{Type: schema.StreamEventStatus, Content: reason, AgentID: r.id, Mode: mode, NeedsAction: true, Reason: "output_limit", Severity: "warning", BudgetScope: "output", StopReason: resp.StopReason, ContinuationCount: continuationCount, Incomplete: true})
		_ = handler(schema.StreamEvent{Type: schema.StreamEventDone, Content: resp.Message.Content, AgentID: r.id, Mode: mode, NeedsAction: true, Reason: "incomplete", Severity: "warning", StopReason: resp.StopReason, ContinuationCount: continuationCount, Incomplete: true})
	}
	if audit != nil {
		fallbackUsed = effectiveFallbackUsed(r.llm, fallbackUsed)
		audit.Record(schema.AuditEntry{Type: "request", AgentID: r.id, SkillName: skillName(state.MatchedSkill), Outcome: "incomplete_output_limit", Detail: reason, DurationMs: time.Since(state.StartedAt).Milliseconds(), Iteration: iteration, FallbackUsed: fallbackUsed, PromptTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.OutputTokens, CachedTokens: resp.Usage.CachedTokens})
	}
	structured := buildStructuredSections(resp.Message.Content, state.MatchedSkill, collectedResults, mode, fallbackUsed)
	findings, changes, verification := buildStructuredArtifacts(resp.Message.Content, collectedResults, mode)
	verification = append(verification, schema.Verification{Kind: "completion_integrity", Status: "blocked", Detail: reason})
	return schema.AgentResult{Output: resp.Message.Content, MatchedSkill: state.MatchedSkill, ToolResults: collectedResults, AgentID: r.id, Mode: mode, Model: state.Profile.Model, StopReason: resp.StopReason, Incomplete: true, IncompleteReason: reason, ContinuationCount: continuationCount, Structured: structured, AuditTrail: auditEntries(audit), Findings: findings, Changes: changes, Verification: verification, ResponseMessage: schema.CopyMessage(resp.Message)}, nil
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

// stageCallBudgetNudge tells the model its stage budget is spent and that only
// a report is expected now.
func stageCallBudgetNudge(limit int) string {
	return fmt.Sprintf("本阶段模型调用预算（%d 次）已用完。不要再调用工具，立即用已有结果输出本阶段报告并说明未完成项。", limit)
}

// deliveryCheckpointNudge asks a long stage to land its work before the hard
// iteration ceiling, so exploration turns do not crowd out the deliverable.
func deliveryCheckpointNudge(iteration, maxIterations int) string {
	return fmt.Sprintf("已用 %d/%d 轮迭代。请立即收敛：先把已确认的改动写入工作区并运行必要的验证，再输出本阶段报告；不要开启新的探索或大范围重读。", iteration, maxIterations)
}

func isOutputLimitStopReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case schema.StopReasonMaxTokens, schema.StopReasonLength, "max_output_tokens", "output_limit":
		return true
	default:
		return false
	}
}

func normalizeCompletionStopReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return schema.StopReasonStop
	}
	if isOutputLimitStopReason(reason) {
		return schema.StopReasonMaxTokens
	}
	return reason
}

func outputContinuationPrompt(continuation int) string {
	return fmt.Sprintf("The previous assistant response stopped at the model output limit. Continue from the exact point where it stopped. Do not restart, summarize, omit remaining required details, or call tools. This is continuation %d.", continuation)
}

func joinContinuationContent(left, right string) string {
	left = strings.TrimRight(left, "\r\n\t ")
	right = strings.TrimLeft(right, "\r\n\t ")
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	return left + "\n" + right
}

func incompleteOutputReason(stopReason string, continuationCount int) string {
	if continuationCount > 0 {
		return fmt.Sprintf("model output remained incomplete after %d continuation attempt(s); stopped with reason %s", continuationCount, fallbackText(stopReason, schema.StopReasonMaxTokens))
	}
	return fmt.Sprintf("model output stopped before completion with reason %s", fallbackText(stopReason, schema.StopReasonMaxTokens))
}

func emitIncompleteDone(handler func(event schema.StreamEvent) error, agentID, mode, content, reason, eventReason, budgetScope, stopReason string, continuationCount int) {
	if handler == nil {
		return
	}
	_ = handler(schema.StreamEvent{
		Type:              schema.StreamEventDone,
		Content:           content,
		AgentID:           agentID,
		Mode:              mode,
		NeedsAction:       true,
		Reason:            fallbackText(eventReason, "incomplete"),
		Severity:          "warning",
		BudgetScope:       budgetScope,
		StopReason:        stopReason,
		ContinuationCount: continuationCount,
		Incomplete:        true,
		ContractCheck:     "completion_integrity",
		SourceRef:         reason,
	})
}

func emitOutputContinuationEvent(handler func(event schema.StreamEvent) error, agentID, mode string, continuation int, eventName, stopReason, detail string) {
	if handler == nil {
		return
	}
	_ = handler(schema.StreamEvent{
		Type:              schema.StreamEventStatus,
		Content:           eventName + ": " + detail,
		AgentID:           agentID,
		Mode:              mode,
		NeedsAction:       true,
		Reason:            eventName,
		Severity:          "warning",
		BudgetScope:       "output",
		StopReason:        stopReason,
		ContinuationCount: continuation,
		Incomplete:        eventName != "continuation_completed" || isOutputLimitStopReason(stopReason),
	})
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
	WriteCalls       int
	ExecCalls        int
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
	postNudgeBatches    int
	followUpSent        bool
}

func newActionNudgeTracker(profile config.AgentProfile, mode string, skill *schema.Skill, input string) *actionNudgeTracker {
	return &actionNudgeTracker{
		enabled: profileCanMutate(profile) && isImplementationActionRequest(profile, mode, skill, input),
	}
}

func (t *actionNudgeTracker) Next(batch toolCallBatchProfile, resultCount int) (string, bool) {
	if t == nil || !t.enabled {
		return "", false
	}
	if batch.WriteCalls > 0 {
		t.mutationSeen = true
		return "", false
	}
	if t.mutationSeen || resultCount <= 0 {
		return "", false
	}
	if t.sent {
		t.postNudgeBatches++
		if t.followUpSent || t.postNudgeBatches < 2 {
			return "", false
		}
		t.followUpSent = true
		return implementationStalledNudgePrompt(), true
	}
	if !batch.ReadOnly {
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
		case config.ToolKindWrite:
			profile.Mutating = true
			profile.WriteCalls++
		case config.ToolKindExec:
			profile.Mutating = true
			profile.ExecCalls++
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
	return "Enough workspace context has been gathered. Stop broad inspection now. If the request asks to change the implementation and the relevant files are identified, make the smallest safe file write next; validation-only commands do not satisfy a change request. If the request only asks to inspect or test, run one bounded verification instead. Read more only when one specific missing detail blocks the next action, and do not repeat the plan without acting."
}

func implementationStalledNudgePrompt() string {
	return "The change request is still unchanged after the action prompt. Do not call more read, search, hash, or validation tools. Use the file content and SHA-256 already returned to apply the smallest safe patch now. If a write is genuinely impossible, stop and report the exact blocker without claiming completion."
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
	if scoped, ok := llm.(interface{ FallbackUsageIsRequestScoped() bool }); ok && scoped.FallbackUsageIsRequestScoped() {
		return fallbackUsed
	}
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

func emitTokenUsage(handler func(event schema.StreamEvent) error, state *session.State, agentID, mode string, resp schema.ChatResponse, contexts ...promptBudgetContext) {
	if !shouldRecordLLMCall(resp) {
		return
	}
	usage := resp.Usage
	context := promptBudgetContext{}
	if len(contexts) > 0 {
		context = contexts[0]
	}
	inputCost, outputCost, totalCost, currency, source := tokenUsageCostEstimate(usage, context)
	kind, toolNames := classifyLLMOutput(resp)
	hitRate := cacheHitRate(usage)
	taskStage := ""
	callIndex := 0
	if state != nil {
		snapshot := state.Snapshot()
		taskStage = snapshot.TaskStage.Stage
		callIndex = len(snapshot.TokenUsages) + 1
		state.AddTokenUsage(schema.TokenUsageSample{
			AgentID:             agentID,
			Mode:                mode,
			WorkflowName:        snapshot.Workflow.Name,
			TaskStage:           taskStage,
			CallIndex:           callIndex,
			OutputKind:          kind,
			StopReason:          resp.StopReason,
			ToolNames:           toolNames,
			PromptTokens:        usage.PromptTokens,
			OutputTokens:        usage.OutputTokens,
			CachedTokens:        usage.CachedTokens,
			TotalTokens:         usage.PromptTokens + usage.OutputTokens,
			CacheHitRate:        hitRate,
			EstimatedInputCost:  inputCost,
			EstimatedOutputCost: outputCost,
			EstimatedTotalCost:  totalCost,
			CostCurrency:        currency,
			PricingSource:       source,
		})
	}
	if handler == nil {
		return
	}
	toolName := ""
	if len(toolNames) > 0 {
		toolName = strings.Join(toolNames, ", ")
	}
	_ = handler(schema.StreamEvent{
		Type:                      schema.StreamEventTokenUsage,
		Content:                   kind,
		AgentID:                   agentID,
		Mode:                      mode,
		TaskStage:                 taskStage,
		ToolName:                  toolName,
		StopReason:                resp.StopReason,
		PromptTokens:              usage.PromptTokens,
		OutputTokens:              usage.OutputTokens,
		CachedTokens:              usage.CachedTokens,
		OutputKind:                kind,
		CacheHitRate:              hitRate,
		BudgetEstimatedInputCost:  inputCost,
		BudgetEstimatedOutputCost: outputCost,
		BudgetEstimatedTotalCost:  totalCost,
		BudgetCostCurrency:        currency,
		BudgetPricingSource:       source,
	})
}

func tokenUsageCostEstimate(usage schema.TokenUsage, context promptBudgetContext) (float64, float64, float64, string, string) {
	if !pricingHasRates(context.Pricing) {
		return 0, 0, 0, "", ""
	}
	inputCost := estimateInputTokenCost(usage.PromptTokens, usage.CachedTokens, context.Pricing)
	outputCost := estimateOutputTokenCost(usage.OutputTokens, context.Pricing)
	totalCost := roundEstimatedCost(inputCost + outputCost)
	return inputCost, outputCost, totalCost, context.Pricing.Currency, context.Pricing.Source
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
				for _, event := range mergeBufferedTextEvents(bufferedEvents) {
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

func mergeBufferedTextEvents(events []schema.StreamEvent) []schema.StreamEvent {
	if len(events) <= 1 {
		return events
	}
	merged := events[0]
	var content strings.Builder
	for _, event := range events {
		content.WriteString(event.Content)
		if event.IsError {
			merged.IsError = true
		}
	}
	merged.Content = content.String()
	return []schema.StreamEvent{merged}
}

func assistantToolMessage(resp schema.ChatResponse) schema.Message {
	message := schema.CopyMessage(resp.Message)
	if strings.TrimSpace(message.Role) == "" {
		message.Role = "assistant"
	}
	if len(resp.ToolCalls) > 0 {
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
		changes := []schema.Change{{Summary: trimmed, Files: files}}
		if !ResultClaimsFileChanges(schema.AgentResult{Output: trimmed, Changes: changes, ToolResults: results}) {
			changes = nil
		}
		return nil, changes, verification
	default:
		return nil, nil, verification
	}
}

var noFileChangesPattern = regexp.MustCompile(`(?i)(?:未|没有)(?:修改|改动|创建|新增|删除|更新)(?:[、，,或和及]*(?:修改|改动|创建|新增|删除|更新))*(?:任何)?(?:项目)?文件|(?:未|没有)(?:执行|进行)(?:任何)?文件(?:修改|改动|写入|增删改)(?:操作)?|no (?:project )?files (?:were )?(?:changed|modified)|(?:did not|didn't) (?:modify|change) (?:any )?(?:project )?files`)
var positiveFileChangePattern = regexp.MustCompile(`(?i)(?:修改|新增|删除|更新|创建|改动|\bmodified\b|\bchanged\b|\bcreated\b|\bdeleted\b|\bupdated\b|\badded\b|\bwrote\b)[^\n。;；]{0,100}\.[a-z][a-z0-9]{0,7}`)

// ResultClaimsFileChanges also corrects legacy fix-mode summaries of purely
// operational tasks, without rewriting their original saved tool evidence.
func ResultClaimsFileChanges(result schema.AgentResult) bool {
	if len(result.Changes) == 0 {
		return false
	}
	output := result.Output
	for i := len(result.Verification) - 1; i >= 0; i-- {
		verification := result.Verification[i]
		if strings.EqualFold(verification.Kind, "verifier") && strings.TrimSpace(verification.Detail) != "" {
			output = strings.TrimSuffix(output, "\n\n"+formatVerifierBlock(verification.Detail))
		}
	}
	if !noFileChangesPattern.MatchString(output) || workflowGraphResultHasWorkspaceMutation(schema.AgentResult{ToolResults: result.ToolResults}) {
		return true
	}
	return positiveFileChangePattern.MatchString(noFileChangesPattern.ReplaceAllString(output, ""))
}

func verificationStatus(results []schema.ToolResult) string {
	for _, result := range results {
		if workflowGraphQualityToolPolicyIntervention(result) {
			continue
		}
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
		case ' ', '\n', '\r', '\t', ',', ';', '(', ')', '[', ']', '"', '\'':
			return true
		default:
			return false
		}
	})
	seen := make(map[string]struct{})
	files := make([]string, 0)
	for _, field := range fields {
		candidate := strings.TrimSpace(field)
		candidate = strings.Trim(candidate, "`.*:;")
		if strings.Contains(candidate, "://") || strings.HasPrefix(candidate, "//") {
			continue
		}
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
	end := 117
	for !utf8.ValidString(input[:end]) {
		end--
	}
	return input[:end] + "..."
}
