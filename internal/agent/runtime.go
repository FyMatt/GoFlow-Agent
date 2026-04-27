package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/interfaces"
	"github.com/FyMatt/GoFlow-Agent/internal/policy"
	"github.com/FyMatt/GoFlow-Agent/internal/runtime"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type ordinaryHandoffDecision string

const (
	ordinaryHandoffDecisionConfirm   ordinaryHandoffDecision = "confirm"
	ordinaryHandoffDecisionRevise    ordinaryHandoffDecision = "revise"
	ordinaryHandoffDecisionReject    ordinaryHandoffDecision = "reject"
	ordinaryHandoffDecisionQuestion  ordinaryHandoffDecision = "question"
	ordinaryHandoffDecisionAmbiguous ordinaryHandoffDecision = "ambiguous"
)

// Runtime coordinates multiple named agents over shared skills and tools.
type Runtime struct {
	cfg                     *config.Config
	skills                  interfaces.SkillManager
	mcp                     interfaces.MCPClient
	session                 *session.State
	audit                   *runtime.AuditLogger
	runners                 map[string]*AgentRunner
	active                  string
	traceInCLI              bool
	approvals               *approvalStore
	workflowAutoApproval    map[string]map[string]workflowApprovalScope
	autoApprovedResults     map[string]schema.ToolResult
	approvedWorkflowResumes map[string]pendingApproval
	ordinaryApprovalResumes map[string]ordinaryApprovalResume
	ordinaryCallResumes     map[string]string
	ordinaryResumeResults   map[string]map[string]schema.ToolResult
}

// AgentRunner encapsulates one configured agent profile and its provider client.
type AgentRunner struct {
	id           string
	profile      config.AgentProfile
	llm          interfaces.LLMClient
	executor     *Executor
	fallbackUsed bool
}

type pendingApproval struct {
	call            schema.ToolCall
	tool            schema.Tool
	agent           string
	workflow        string
	stage           WorkflowStage
	request         string
	responseContent string
	completed       []WorkflowStageResult
	skillChain      []schema.Skill
	skillIndex      int
	stagePrompt     string
	ordinaryResume  string
}

type ordinaryApprovalResume struct {
	ID               string
	AgentID          string
	Profile          config.AgentProfile
	MatchedSkill     *schema.Skill
	SystemPrompt     string
	Mode             string
	Messages         []schema.Message
	SuspendedCalls   []schema.ToolCall
	CollectedResults []schema.ToolResult
}

type workflowApprovalScope struct {
	Workflow string
	Stage    WorkflowStage
	ToolName string
}

type approvalStore struct {
	mu      sync.Mutex
	pending map[string]pendingApproval
}

type approvalDecision struct {
	Pending  pendingApproval
	Approved bool
	Found    bool
}

func newApprovalStore() *approvalStore {
	return &approvalStore{pending: make(map[string]pendingApproval)}
}

func (s *approvalStore) Add(item pendingApproval) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[item.call.ID] = item
}

func (s *approvalStore) Resolve(id string, approved bool) approvalDecision {
	if s == nil {
		return approvalDecision{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	return approvalDecision{Pending: pending, Approved: approved, Found: ok}
}

func (s *approvalStore) Approve(id string) (pendingApproval, bool) {
	decision := s.Resolve(id, true)
	return decision.Pending, decision.Found
}

func (s *approvalStore) Deny(id string) (pendingApproval, bool) {
	decision := s.Resolve(id, false)
	return decision.Pending, decision.Found
}

func (s *approvalStore) Get(id string) (pendingApproval, bool) {
	if s == nil {
		return pendingApproval{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[id]
	return pending, ok
}

func (s *approvalStore) Annotate(id, workflow string, stage WorkflowStage, request, responseContent string, completed []WorkflowStageResult) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[id]
	if !ok {
		return false
	}
	pending.workflow = workflow
	pending.stage = stage
	pending.request = request
	pending.responseContent = responseContent
	pending.completed = append([]WorkflowStageResult(nil), completed...)
	s.pending[id] = pending
	return true
}

func (s *approvalStore) AnnotateSkillChain(id string, chain []schema.Skill, index int, stagePrompt string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[id]
	if !ok {
		return false
	}
	pending.skillChain = copySkillChain(chain)
	pending.skillIndex = index
	pending.stagePrompt = stagePrompt
	s.pending[id] = pending
	return true
}

func (s *approvalStore) AnnotateOrdinaryResume(id, resumeID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[id]
	if !ok {
		return false
	}
	pending.ordinaryResume = resumeID
	s.pending[id] = pending
	return true
}

func (s *approvalStore) List() []pendingApproval {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]pendingApproval, 0, len(s.pending))
	for _, item := range s.pending {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].call.ID < items[j].call.ID
	})
	return items
}

// NewRuntime constructs a multi-agent runtime.
func NewRuntime(cfg *config.Config, clients map[string]interfaces.LLMClient, skills interfaces.SkillManager, mcp interfaces.MCPClient, state *session.State, audit *runtime.AuditLogger) (*Runtime, error) {
	runners := make(map[string]*AgentRunner, len(cfg.Agents))
	for id, profile := range cfg.Agents {
		client, ok := clients[profile.Provider]
		if !ok {
			return nil, fmt.Errorf("agent %s provider client not found: %s", id, profile.Provider)
		}
		runners[id] = &AgentRunner{
			id:           id,
			profile:      profile,
			llm:          client,
			executor:     NewExecutor(mcp),
			fallbackUsed: false,
		}
	}
	runtimeRef := &Runtime{
		cfg:                     cfg,
		skills:                  skills,
		mcp:                     mcp,
		session:                 state,
		audit:                   audit,
		runners:                 runners,
		traceInCLI:              cfg.Audit.ShowTraceInCLI,
		approvals:               newApprovalStore(),
		workflowAutoApproval:    make(map[string]map[string]workflowApprovalScope),
		autoApprovedResults:     make(map[string]schema.ToolResult),
		approvedWorkflowResumes: make(map[string]pendingApproval),
		ordinaryApprovalResumes: make(map[string]ordinaryApprovalResume),
		ordinaryCallResumes:     make(map[string]string),
		ordinaryResumeResults:   make(map[string]map[string]schema.ToolResult),
	}
	active := runtimeRef.defaultAgentID()
	if state != nil {
		active = runtimeRef.initializeSessionState(active)
	}
	runtimeRef.active = active
	return runtimeRef, nil
}

func firstRuntimeAgent(agents map[string]config.AgentProfile) string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func (r *Runtime) defaultAgentID() string {
	if r == nil || r.cfg == nil {
		return ""
	}
	agentID := strings.TrimSpace(r.cfg.DefaultAgent)
	if agentID == "" {
		agentID = firstRuntimeAgent(r.cfg.Agents)
	}
	return agentID
}

func (r *Runtime) initializeSessionState(defaultAgent string) string {
	if r == nil || r.session == nil {
		return defaultAgent
	}
	if strings.TrimSpace(defaultAgent) == "" {
		defaultAgent = firstRuntimeAgent(r.cfg.Agents)
	}
	snapshot := r.session.Snapshot()
	if r.hasResumableSessionState(snapshot) {
		active := strings.TrimSpace(snapshot.ActiveAgent)
		if _, ok := r.runners[active]; ok {
			if mode := strings.TrimSpace(snapshot.Mode); mode == "" {
				r.session.SetMode(r.runners[active].profile.Mode)
			}
			return active
		}
	}
	if _, ok := r.runners[defaultAgent]; ok {
		r.session.SetActiveAgent(defaultAgent)
		r.session.SetMode(r.runners[defaultAgent].profile.Mode)
	}
	return defaultAgent
}

func (r *Runtime) hasResumableSessionState(snapshot session.Snapshot) bool {
	if r == nil {
		return false
	}
	if len(snapshot.PendingApprovals) > 0 {
		return true
	}
	if strings.TrimSpace(snapshot.PendingHandoff.TargetAgent) != "" || strings.TrimSpace(snapshot.PendingHandoff.ExpectedAction) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(snapshot.Workflow.Status)) {
	case "running", "awaiting_tool_approval":
		return strings.TrimSpace(snapshot.Workflow.Name) != ""
	default:
		return false
	}
}

func (r *Runtime) ensureOrdinaryChatAgentState() {
	if r == nil || r.session == nil {
		return
	}
	snapshot := r.session.Snapshot()
	if r.hasResumableSessionState(snapshot) {
		return
	}
	active := strings.TrimSpace(snapshot.ActiveAgent)
	runner, ok := r.runners[active]
	if !ok {
		_ = r.RestoreDefaultAgent()
		return
	}
	if strings.EqualFold(runner.profile.Mode, "chat") {
		return
	}
	_ = r.RestoreDefaultAgent()
}

func (r *Runtime) restoreDefaultAgentAfterCompletedTurn() {
	_ = r.restoreAgentAfterCompletedTurn(r.defaultAgentID())
}

func (r *Runtime) restoreAgentAfterCompletedTurn(agentID string) error {
	if r == nil || r.session == nil {
		return nil
	}
	snapshot := r.session.Snapshot()
	if len(snapshot.PendingApprovals) > 0 || strings.TrimSpace(snapshot.PendingHandoff.TargetAgent) != "" || strings.TrimSpace(snapshot.PendingHandoff.ExpectedAction) != "" {
		return nil
	}
	if strings.TrimSpace(agentID) == "" {
		agentID = r.defaultAgentID()
	}
	if _, ok := r.runners[agentID]; !ok {
		return r.RestoreDefaultAgent()
	}
	return r.SetActiveAgent(agentID)
}

func (r *Runtime) applySessionApprovedTools(execCtx *ExecutionContext) {
	if r == nil || execCtx == nil || r.session == nil {
		return
	}
	for _, scope := range r.session.ApprovedToolScopesForWorkspace(workspaceRoot(r)) {
		execCtx.RememberApprovedToolScope(scope.Kind, scope.Name)
	}
}

func (r *Runtime) workflowInProgress() bool {
	if r == nil || r.session == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(r.session.Snapshot().Workflow.Status))
	switch status {
	case "running", "awaiting_tool_approval":
		return true
	default:
		return false
	}
}

func (r *Runtime) RestoreDefaultAgent() error {
	if r == nil || r.cfg == nil {
		return fmt.Errorf("runtime not configured")
	}
	agentID := r.defaultAgentID()
	if agentID == "" {
		return fmt.Errorf("default agent not configured")
	}
	return r.SetActiveAgent(agentID)
}

// ActiveAgent returns the current agent id.
func (r *Runtime) ActiveAgent() string {
	if r.session != nil && r.session.ActiveAgent() != "" {
		return r.session.ActiveAgent()
	}
	return r.active
}

// SetActiveAgent switches the active agent.
func (r *Runtime) SetActiveAgent(agentID string) error {
	runner, ok := r.runners[agentID]
	if !ok {
		return fmt.Errorf("unknown agent: %s", agentID)
	}
	r.active = agentID
	if r.session != nil {
		r.session.SetActiveAgent(agentID)
		r.session.SetMode(runner.profile.Mode)
	}
	return nil
}

// AgentNames lists configured agents.
func (r *Runtime) AgentNames() []string {
	names := make([]string, 0, len(r.runners))
	for name := range r.runners {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Profile returns a configured agent profile.
func (r *Runtime) Profile(agentID string) (config.AgentProfile, bool) {
	runner, ok := r.runners[agentID]
	if !ok {
		return config.AgentProfile{}, false
	}
	return runner.profile, true
}

// SetMode overrides the current session mode.
func (r *Runtime) SetMode(mode string) error {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "chat"
	}
	switch mode {
	case "chat", "plan", "audit", "fix":
	default:
		return fmt.Errorf("unsupported mode: %s", mode)
	}
	if r.session != nil {
		r.session.SetMode(mode)
	}
	return nil
}

// Mode returns the effective session mode.
func (r *Runtime) Mode() string {
	if r.session != nil {
		return r.session.Mode()
	}
	if runner, ok := r.runners[r.ActiveAgent()]; ok && runner.profile.Mode != "" {
		return runner.profile.Mode
	}
	return "chat"
}

// SetTrace toggles CLI trace visibility.
func (r *Runtime) SetTrace(enabled bool) {
	r.traceInCLI = enabled
}

// TraceEnabled reports whether trace events should be rendered in the CLI.
func (r *Runtime) TraceEnabled() bool {
	return r.traceInCLI
}

// SessionSnapshot exposes bounded session memory.
func (r *Runtime) SessionSnapshot() session.Snapshot {
	if r.session == nil {
		return session.Snapshot{}
	}
	return r.session.Snapshot()
}

func (r *Runtime) PendingHandoff() session.PendingHandoffSnapshot {
	if r == nil || r.session == nil {
		return session.PendingHandoffSnapshot{}
	}
	return r.session.PendingHandoff()
}

func (r *Runtime) HasPendingHandoff() bool {
	handoff := r.PendingHandoff()
	return strings.TrimSpace(handoff.TargetAgent) != "" || strings.TrimSpace(handoff.ExpectedAction) != ""
}

func (r *Runtime) PendingHandoffDecision(input string) string {
	return string(classifyOrdinaryChatHandoffReply(input))
}

func (r *Runtime) LastRouting() session.RoutingSnapshot {
	if r == nil || r.session == nil {
		return session.RoutingSnapshot{}
	}
	return r.session.LastRouting()
}

func (r *Runtime) recordOrdinaryChatRoute(request, targetAgent string, intent ordinaryChatIntent, outcome string) {
	if r == nil || r.session == nil {
		return
	}
	targetMode := ""
	if runner, ok := r.runners[targetAgent]; ok {
		targetMode = runner.profile.Mode
	}
	reason := strings.TrimSpace(intent.Reason)
	if reason == "" {
		reason = strings.TrimSpace(intent.Mode)
	}
	r.session.SetLastRouting(session.RoutingSnapshot{
		Request:     truncateSummary(request),
		SourceAgent: r.ActiveAgent(),
		TargetAgent: targetAgent,
		TargetMode:  targetMode,
		Outcome:     outcome,
		Reason:      reason,
	})
}

func (r *Runtime) handlePendingOrdinaryChatHandoff(ctx context.Context, input string, handler func(event schema.StreamEvent) error) (schema.AgentResult, bool, error) {
	if r == nil || r.session == nil || !r.HasPendingHandoff() || r.workflowInProgress() {
		return schema.AgentResult{}, false, nil
	}
	handoff := r.PendingHandoff()
	decision := classifyOrdinaryChatHandoffReply(input)
	requestSummary := strings.TrimSpace(handoff.Request)
	if requestSummary == "" {
		requestSummary = "the approved plan"
	}
	switch decision {
	case ordinaryHandoffDecisionConfirm:
		r.recordOrdinaryChatRoute(input, strings.TrimSpace(handoff.TargetAgent), ordinaryChatIntent{}, "handoff_confirmed")
		result, err := r.ConfirmPendingHandoff(ctx, handler)
		return result, true, err
	case ordinaryHandoffDecisionReject:
		r.recordOrdinaryChatRoute(input, strings.TrimSpace(handoff.TargetAgent), ordinaryChatIntent{}, "handoff_rejected")
		r.RejectPendingHandoff()
		return schema.AgentResult{
			Output:     fmt.Sprintf("Cancelled pending implementation handoff for %s.", requestSummary),
			AgentID:    r.ActiveAgent(),
			Mode:       r.Mode(),
			AuditTrail: r.AuditTrail(),
		}, true, nil
	case ordinaryHandoffDecisionRevise, ordinaryHandoffDecisionQuestion:
		r.recordOrdinaryChatRoute(input, strings.TrimSpace(handoff.SourceAgent), ordinaryChatIntent{}, "handoff_revised")
		sourceAgent := strings.TrimSpace(handoff.SourceAgent)
		if sourceAgent != "" {
			if _, ok := r.runners[sourceAgent]; ok && sourceAgent != r.ActiveAgent() {
				if err := r.SetActiveAgent(sourceAgent); err != nil {
					return schema.AgentResult{}, true, err
				}
			}
		}
		if r.audit != nil {
			r.audit.Record(schema.AuditEntry{Type: "handoff", AgentID: r.ActiveAgent(), Outcome: string(decision), Detail: input})
		}
		return schema.AgentResult{}, false, nil
	case ordinaryHandoffDecisionAmbiguous:
		r.recordOrdinaryChatRoute(input, strings.TrimSpace(handoff.TargetAgent), ordinaryChatIntent{}, "waiting_confirmation")
		if r.audit != nil {
			r.audit.Record(schema.AuditEntry{Type: "handoff", AgentID: r.ActiveAgent(), Outcome: string(decision), Detail: input})
		}
		return schema.AgentResult{
			Output:     "A plan is waiting for confirmation before execution. Reply with yes / continue / go ahead to proceed, or describe the change you want.",
			AgentID:    r.ActiveAgent(),
			Mode:       r.Mode(),
			AuditTrail: r.AuditTrail(),
		}, true, nil
	default:
		return schema.AgentResult{}, false, nil
	}
}

func classifyOrdinaryChatHandoffReply(input string) ordinaryHandoffDecision {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return ordinaryHandoffDecisionAmbiguous
	}
	if containsAny(text,
		"\u53ef\u4ee5", "\u53ef\u4ee5\uff0c\u7ee7\u7eed", "\u53ef\u4ee5\u7ee7\u7eed", "\u7ee7\u7eed", "\u7ee7\u7eed\u5b9e\u73b0", "\u786e\u8ba4", "\u786e\u8ba4\u6267\u884c", "\u540c\u610f", "\u6279\u51c6", "\u5168\u90e8\u6dfb\u52a0", "\u5168\u90e8\u4fee\u6539", "\u5c31\u6309\u8fd9\u4e2a\u6267\u884c", "\u5c31\u6309\u8fd9\u4e2a\u505a", "\u76f4\u63a5\u4fee\u6539\u5373\u53ef", "\u76f4\u63a5\u505a", "\u76f4\u63a5\u6539\u5427",
		"approve", "approved", "yes", "ok", "okay", "continue", "go ahead", "ship it", "do it",
	) {
		return ordinaryHandoffDecisionConfirm
	}
	if containsAny(text,
		"\u4e0d\u8981", "\u4e0d\u505a", "\u62d2\u7edd", "\u53d6\u6d88", "\u7b97\u4e86", "\u5148\u522b", "\u505c\u6b62",
		"cancel", "stop", "reject", "deny", "don't", "do not",
	) {
		return ordinaryHandoffDecisionReject
	}
	if containsAny(text,
		"\u6539\u4e00\u4e0b", "\u8c03\u6574", "\u8865\u5145", "\u7ec6\u5316", "\u7f29\u5c0f", "\u6539\u6210", "\u6362\u6210", "\u5148\u53ea", "\u5148\u505a", "\u53ea\u505a", "\u91cd\u65b0\u89c4\u5212",
		"revise", "change", "adjust", "update the plan", "narrow", "scope", "only do", "instead",
	) {
		return ordinaryHandoffDecisionRevise
	}
	if strings.Contains(text, "?") || strings.Contains(text, "\uff1f") || containsAny(text,
		"\u4e3a\u4ec0\u4e48", "\u600e\u4e48", "\u80fd\u4e0d\u80fd", "\u662f\u5426", "\u8bf4\u660e", "\u89e3\u91ca",
		"why", "how", "what", "can you", "could you", "explain",
	) {
		return ordinaryHandoffDecisionQuestion
	}
	return ordinaryHandoffDecisionAmbiguous
}

func (r *Runtime) queueOrdinaryChatHandoff(request, planSummary, targetAgent, targetMode string) {
	if r == nil || r.session == nil {
		return
	}
	if strings.TrimSpace(request) == "" || strings.TrimSpace(planSummary) == "" || strings.TrimSpace(targetAgent) == "" {
		return
	}
	r.session.SetPendingHandoff(session.PendingHandoffSnapshot{
		Request:        request,
		SourceAgent:    r.ActiveAgent(),
		TargetAgent:    targetAgent,
		TargetMode:     targetMode,
		PlanSummary:    planSummary,
		ExpectedAction: "confirm_execution",
	})
	r.session.SetLastRouting(session.RoutingSnapshot{
		Request:     truncateSummary(request),
		SourceAgent: r.ActiveAgent(),
		TargetAgent: targetAgent,
		TargetMode:  targetMode,
		Outcome:     "waiting_confirmation",
		Reason:      "planner result queued for execution handoff",
	})
}

func (r *Runtime) clearPendingHandoff() {
	if r == nil || r.session == nil {
		return
	}
	r.session.ClearPendingHandoff()
}

func (r *Runtime) ConfirmPendingHandoff(ctx context.Context, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	if r == nil || r.session == nil {
		return schema.AgentResult{}, fmt.Errorf("runtime session not configured")
	}
	handoff := r.session.PendingHandoff()
	targetAgent := strings.TrimSpace(handoff.TargetAgent)
	if targetAgent == "" {
		return schema.AgentResult{}, fmt.Errorf("pending handoff not found")
	}
	r.session.SetLastRouting(session.RoutingSnapshot{
		Request:     truncateSummary(handoff.Request),
		SourceAgent: strings.TrimSpace(handoff.SourceAgent),
		TargetAgent: targetAgent,
		TargetMode:  strings.TrimSpace(handoff.TargetMode),
		Outcome:     "handoff_confirmed",
		Reason:      strings.TrimSpace(handoff.ExpectedAction),
	})
	runner, ok := r.runners[targetAgent]
	if !ok {
		return schema.AgentResult{}, fmt.Errorf("handoff target agent not configured: %s", targetAgent)
	}
	prompt := fmt.Sprintf("Implement the approved plan for the following request. Reuse the provided plan, make minimal necessary changes, and summarize what changed.\n\nOriginal request:\n%s\n\nApproved plan:\n%s", handoff.Request, handoff.PlanSummary)
	r.clearPendingHandoff()
	if err := r.SetActiveAgent(targetAgent); err != nil {
		return schema.AgentResult{}, err
	}
	if r.audit != nil {
		r.audit.Record(schema.AuditEntry{Type: "handoff", AgentID: targetAgent, Outcome: "confirmed", Detail: prompt})
	}
	result, err := runner.RunStream(ctx, prompt, r.skills, r.mcp, r.session, r.audit, r, handler)
	if err != nil {
		return schema.AgentResult{}, err
	}
	result.AuditTrail = r.AuditTrail()
	result.AgentID = runner.id
	if strings.TrimSpace(result.Mode) == "" {
		result.Mode = r.Mode()
	}
	if err := r.maybeRunVerifierPass(ctx, handoff.Request, &result, handler); err != nil {
		return schema.AgentResult{}, err
	}
	result.AuditTrail = r.AuditTrail()
	r.restoreDefaultAgentAfterCompletedTurn()
	return result, nil
}

func (r *Runtime) RejectPendingHandoff() {
	if r == nil {
		return
	}
	handoff := r.PendingHandoff()
	if r.session != nil {
		r.session.SetLastRouting(session.RoutingSnapshot{
			Request:     truncateSummary(handoff.Request),
			SourceAgent: strings.TrimSpace(handoff.SourceAgent),
			TargetAgent: strings.TrimSpace(handoff.TargetAgent),
			TargetMode:  strings.TrimSpace(handoff.TargetMode),
			Outcome:     "handoff_rejected",
			Reason:      strings.TrimSpace(handoff.ExpectedAction),
		})
	}
	r.clearPendingHandoff()
	if r.audit != nil {
		r.audit.Record(schema.AuditEntry{Type: "handoff", AgentID: handoff.TargetAgent, Outcome: "rejected", Detail: handoff.Request})
	}
}

// AuditTrail returns collected audit entries.
func (r *Runtime) AuditTrail() []schema.AuditEntry {
	if r.audit == nil {
		return nil
	}
	return r.audit.Entries()
}

// ClearAudit resets the audit buffer.
func (r *Runtime) ClearAudit() {
	if r.audit != nil {
		r.audit.Clear()
	}
}

func (r *Runtime) queueApproval(call schema.ToolCall, tool schema.Tool, agentID string) {
	item := pendingApproval{call: call, tool: tool, agent: agentID}
	if r != nil && r.session != nil {
		workflow := r.session.Snapshot().Workflow
		if strings.TrimSpace(workflow.Name) != "" && strings.TrimSpace(workflow.NextStage) != "" {
			item.workflow = workflow.Name
			item.stage = WorkflowStage(workflow.NextStage)
			item.request = workflow.Request
		}
	}
	if r.approvals != nil {
		r.approvals.Add(item)
	}
	r.syncPendingApprovals()
}

func (r *Runtime) syncPendingApprovals() {
	if r == nil || r.session == nil || r.approvals == nil {
		return
	}
	items := r.approvals.List()
	summaries := make([]session.PendingApprovalSnapshot, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, session.PendingApprovalSnapshot{
			CallID:           item.call.ID,
			ToolName:         item.tool.Name,
			AgentID:          item.agent,
			ArgumentsSummary: summarizeApprovalArguments(item.call.Arguments),
			Arguments:        string(item.call.Arguments),
			WorkflowName:     item.workflow,
			Stage:            string(item.stage),
			Request:          item.request,
			CompletedSummary: summarizeWorkflow(item.completed),
		})
	}
	r.session.SetPendingApprovals(summaries)
}

func summarizeApprovalArguments(arguments []byte) string {
	return summarizeToolArguments(arguments)
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (r *Runtime) PendingApprovalSummaries() []session.PendingApprovalSnapshot {
	if r == nil || r.approvals == nil {
		return nil
	}
	items := r.approvals.List()
	summaries := make([]session.PendingApprovalSnapshot, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, session.PendingApprovalSnapshot{
			CallID:           item.call.ID,
			ToolName:         item.tool.Name,
			AgentID:          item.agent,
			ArgumentsSummary: summarizeApprovalArguments(item.call.Arguments),
			Arguments:        string(item.call.Arguments),
			WorkflowName:     item.workflow,
			Stage:            string(item.stage),
			Request:          item.request,
			CompletedSummary: summarizeWorkflow(item.completed),
		})
	}
	return summaries
}

// PendingApprovals returns queued tool approvals.
func (r *Runtime) PendingApprovals() []schema.ToolCall {
	items := r.approvals.List()
	calls := make([]schema.ToolCall, 0, len(items))
	for _, item := range items {
		calls = append(calls, item.call)
	}
	return calls
}

func (r *Runtime) ResolvePendingApproval(id string, approved bool) approvalDecision {
	if r == nil || r.approvals == nil {
		return approvalDecision{}
	}
	decision := r.approvals.Resolve(id, approved)
	if approved && decision.Found && strings.TrimSpace(decision.Pending.workflow) != "" {
		if r.approvedWorkflowResumes == nil {
			r.approvedWorkflowResumes = make(map[string]pendingApproval)
		}
		r.approvedWorkflowResumes[id] = decision.Pending
	} else if !approved && r.approvedWorkflowResumes != nil {
		delete(r.approvedWorkflowResumes, id)
	}
	r.syncPendingApprovals()
	return decision
}

func (r *Runtime) PendingApproval(id string) (pendingApproval, bool) {
	if r == nil {
		return pendingApproval{}, false
	}
	if r.approvals != nil {
		if pending, ok := r.approvals.Get(id); ok {
			return pending, true
		}
	}
	if r.approvedWorkflowResumes != nil {
		pending, ok := r.approvedWorkflowResumes[id]
		if ok {
			return pending, true
		}
	}
	return pendingApproval{}, false
}

func (r *Runtime) AnnotatePendingApproval(id, workflow string, stage WorkflowStage, request, responseContent string, completed []WorkflowStageResult) bool {
	if r == nil || r.approvals == nil {
		return false
	}
	updated := r.approvals.Annotate(id, workflow, stage, request, responseContent, completed)
	if updated {
		if pending, ok := r.approvals.Get(id); ok && r.shouldAutoApproveWorkflowCall(pending) {
			r.ResolvePendingApproval(id, true)
			if r.audit != nil {
				r.audit.Record(schema.AuditEntry{Type: "tool_call", AgentID: pending.agent, ToolName: pending.tool.Name, Outcome: "approved", Detail: "workflow auto-approved"})
			}
		}
		r.syncPendingApprovals()
	}
	return updated
}

func (r *Runtime) AnnotateSkillChainPendingApproval(id, workflow string, stage WorkflowStage, request, responseContent string, completed []WorkflowStageResult, chain []schema.Skill, index int, stagePrompt string) bool {
	if r == nil || r.approvals == nil {
		return false
	}
	updated := r.approvals.Annotate(id, workflow, stage, request, responseContent, completed)
	if updated {
		_ = r.approvals.AnnotateSkillChain(id, chain, index, stagePrompt)
		if pending, ok := r.approvals.Get(id); ok && r.shouldAutoApproveWorkflowCall(pending) {
			r.ResolvePendingApproval(id, true)
			if r.audit != nil {
				r.audit.Record(schema.AuditEntry{Type: "tool_call", AgentID: pending.agent, ToolName: pending.tool.Name, Outcome: "approved", Detail: "workflow auto-approved"})
			}
		}
		r.syncPendingApprovals()
	}
	return updated
}

func (r *Runtime) AnnotateWorkflowGraphPendingApproval(id, workflow string, stage WorkflowStage, request, responseContent string, completed []WorkflowStageResult, index int, stagePrompt string) bool {
	if r == nil || r.approvals == nil {
		return false
	}
	updated := r.approvals.Annotate(id, workflow, stage, request, responseContent, completed)
	if updated {
		_ = r.approvals.AnnotateSkillChain(id, nil, index, stagePrompt)
		if pending, ok := r.approvals.Get(id); ok && r.shouldAutoApproveWorkflowCall(pending) {
			r.ResolvePendingApproval(id, true)
			if r.audit != nil {
				r.audit.Record(schema.AuditEntry{Type: "tool_call", AgentID: pending.agent, ToolName: pending.tool.Name, Outcome: "approved", Detail: "workflow auto-approved"})
			}
		}
		r.syncPendingApprovals()
	}
	return updated
}

func (r *Runtime) CaptureOrdinaryApprovalContext(agentID string, profile config.AgentProfile, matchedSkill *schema.Skill, systemPrompt, mode string, messages []schema.Message, suspendedCalls []schema.ToolCall, collectedResults []schema.ToolResult) {
	if r == nil || r.approvals == nil || len(suspendedCalls) == 0 {
		return
	}
	resumeID := strings.TrimSpace(suspendedCalls[0].ID)
	if resumeID == "" {
		return
	}
	if r.ordinaryApprovalResumes == nil {
		r.ordinaryApprovalResumes = make(map[string]ordinaryApprovalResume)
	}
	if r.ordinaryCallResumes == nil {
		r.ordinaryCallResumes = make(map[string]string)
	}
	if r.ordinaryResumeResults == nil {
		r.ordinaryResumeResults = make(map[string]map[string]schema.ToolResult)
	}
	var skillCopy *schema.Skill
	if matchedSkill != nil {
		copied := *matchedSkill
		skillCopy = &copied
	}
	resume := ordinaryApprovalResume{
		ID:               resumeID,
		AgentID:          agentID,
		Profile:          profile,
		MatchedSkill:     skillCopy,
		SystemPrompt:     systemPrompt,
		Mode:             mode,
		Messages:         append([]schema.Message(nil), messages...),
		SuspendedCalls:   append([]schema.ToolCall(nil), suspendedCalls...),
		CollectedResults: append([]schema.ToolResult(nil), collectedResults...),
	}
	r.ordinaryApprovalResumes[resumeID] = resume
	for _, call := range suspendedCalls {
		if strings.TrimSpace(call.ID) == "" {
			continue
		}
		r.ordinaryCallResumes[call.ID] = resumeID
		r.approvals.AnnotateOrdinaryResume(call.ID, resumeID)
	}
	r.syncPendingApprovals()
}

func (r *Runtime) recordOrdinaryResumeResult(pending pendingApproval, result schema.ToolResult) {
	if r == nil || strings.TrimSpace(pending.ordinaryResume) == "" || strings.TrimSpace(result.CallID) == "" {
		return
	}
	if r.ordinaryResumeResults == nil {
		r.ordinaryResumeResults = make(map[string]map[string]schema.ToolResult)
	}
	if r.ordinaryResumeResults[pending.ordinaryResume] == nil {
		r.ordinaryResumeResults[pending.ordinaryResume] = make(map[string]schema.ToolResult)
	}
	r.ordinaryResumeResults[pending.ordinaryResume][result.CallID] = result
	if r.ordinaryCallResumes == nil {
		r.ordinaryCallResumes = make(map[string]string)
	}
	r.ordinaryCallResumes[result.CallID] = pending.ordinaryResume
}

// ResumeApprovedOrdinaryToolCall continues an ordinary chat tool loop after its pending approvals are resolved.
func (r *Runtime) ResumeApprovedOrdinaryToolCall(ctx context.Context, callID string, handler func(event schema.StreamEvent) error) (schema.AgentResult, bool, error) {
	if r == nil || strings.TrimSpace(callID) == "" {
		return schema.AgentResult{}, false, nil
	}
	resumeID := ""
	if r.ordinaryCallResumes != nil {
		resumeID = r.ordinaryCallResumes[callID]
	}
	if strings.TrimSpace(resumeID) == "" {
		return schema.AgentResult{}, false, nil
	}
	resume, ok := r.ordinaryApprovalResumes[resumeID]
	if !ok {
		return schema.AgentResult{}, false, nil
	}
	resultsByCall := r.ordinaryResumeResults[resumeID]
	if len(resultsByCall) < len(resume.SuspendedCalls) {
		return schema.AgentResult{}, false, nil
	}
	approvedResults := make([]schema.ToolResult, 0, len(resume.SuspendedCalls))
	for _, call := range resume.SuspendedCalls {
		result, ok := resultsByCall[call.ID]
		if !ok {
			return schema.AgentResult{}, false, nil
		}
		approvedResults = append(approvedResults, result)
	}
	result, err := r.resumeOrdinaryRun(ctx, resume, approvedResults, handler)
	if err != nil {
		return schema.AgentResult{}, true, err
	}
	r.clearOrdinaryResume(resumeID)
	return result, true, nil
}

func (r *Runtime) clearOrdinaryResume(resumeID string) {
	if r == nil || strings.TrimSpace(resumeID) == "" {
		return
	}
	resume, ok := r.ordinaryApprovalResumes[resumeID]
	if ok && r.ordinaryCallResumes != nil {
		for _, call := range resume.SuspendedCalls {
			delete(r.ordinaryCallResumes, call.ID)
		}
	}
	if r.ordinaryApprovalResumes != nil {
		delete(r.ordinaryApprovalResumes, resumeID)
	}
	if r.ordinaryResumeResults != nil {
		delete(r.ordinaryResumeResults, resumeID)
	}
}

func (r *Runtime) resumeOrdinaryRun(ctx context.Context, resume ordinaryApprovalResume, approvedResults []schema.ToolResult, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	runner, ok := r.runners[resume.AgentID]
	if !ok {
		return schema.AgentResult{}, fmt.Errorf("ordinary resume agent not configured: %s", resume.AgentID)
	}
	if err := r.SetActiveAgent(resume.AgentID); err != nil {
		return schema.AgentResult{}, err
	}
	messages := append([]schema.Message(nil), resume.Messages...)
	collectedResults := append([]schema.ToolResult(nil), resume.CollectedResults...)
	for _, result := range approvedResults {
		messages = append(messages, schema.Message{Role: "tool", Name: result.ToolName, ToolCallID: result.CallID, Content: result.Content})
		collectedResults = append(collectedResults, result)
	}
	result, err := runner.continueConversation(ctx, agentConversationState{
		Profile:          resume.Profile,
		MatchedSkill:     resume.MatchedSkill,
		SystemPrompt:     resume.SystemPrompt,
		Mode:             resume.Mode,
		Messages:         messages,
		CollectedResults: collectedResults,
	}, r.skills, r.mcp, r.session, r.audit, r, handler)
	if err != nil {
		return schema.AgentResult{}, err
	}
	result.AuditTrail = r.AuditTrail()
	result.AgentID = runner.id
	if strings.TrimSpace(result.Mode) == "" {
		result.Mode = r.Mode()
	}
	if err := r.maybeRunVerifierPass(ctx, firstUserMessage(resume.Messages), &result, handler); err != nil {
		return schema.AgentResult{}, err
	}
	result.AuditTrail = r.AuditTrail()
	r.restoreDefaultAgentAfterCompletedTurn()
	return result, nil
}

// ApproveToolCall executes a pending tool call immediately.
func (r *Runtime) ApproveToolCall(ctx context.Context, id string) (schema.ToolResult, error) {
	return r.approveToolCall(ctx, id, false)
}

func (r *Runtime) ApproveToolCallAndRemember(ctx context.Context, id string) (schema.ToolResult, error) {
	return r.approveToolCall(ctx, id, true)
}

func (r *Runtime) approveToolCall(ctx context.Context, id string, remember bool) (schema.ToolResult, error) {
	decision := r.ResolvePendingApproval(id, true)
	if !decision.Found {
		return schema.ToolResult{}, fmt.Errorf("pending approval not found: %s", id)
	}
	pending := decision.Pending
	result, err := r.mcp.CallTool(ctx, pending.call.Name, pending.call.Arguments)
	if err != nil {
		return schema.ToolResult{}, err
	}
	result.CallID = pending.call.ID
	if result.ToolName == "" {
		result.ToolName = pending.tool.Name
	}
	if r.audit != nil {
		r.audit.Record(schema.AuditEntry{Type: "tool_call", AgentID: pending.agent, ToolName: pending.tool.Name, Outcome: "approved", Detail: result.Content})
	}
	if remember {
		r.rememberApprovedToolScope(pending.tool)
	}
	r.recordOrdinaryResumeResult(pending, result)
	return result, nil
}

func (r *Runtime) RememberPendingToolApproval(id string) error {
	if r == nil || strings.TrimSpace(id) == "" {
		return fmt.Errorf("pending approval not found: %s", id)
	}
	pending, ok := r.PendingApproval(id)
	if !ok {
		return fmt.Errorf("pending approval not found: %s", id)
	}
	r.rememberApprovedToolScope(pending.tool)
	return nil
}

func (r *Runtime) ApproveAllPendingToolCalls(ctx context.Context) ([]schema.ToolResult, error) {
	pendingItems := r.approvals.List()
	results := make([]schema.ToolResult, 0, len(pendingItems))
	for _, item := range pendingItems {
		result, err := r.ApproveToolCall(ctx, item.call.ID)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (r *Runtime) ApprovePendingToolCallsForWorkflow(ctx context.Context, workflowName string) ([]schema.ToolResult, error) {
	pendingItems := r.approvals.List()
	results := make([]schema.ToolResult, 0, len(pendingItems))
	for _, item := range pendingItems {
		if item.workflow != workflowName {
			continue
		}
		result, err := r.ApproveToolCall(ctx, item.call.ID)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
		r.EnableWorkflowAutoApproval(workflowName, item.stage, item.tool.Name)
	}
	return results, nil
}

func (r *Runtime) EnableWorkflowAutoApproval(workflowName string, stage WorkflowStage, toolName string) {
	if r == nil || strings.TrimSpace(workflowName) == "" || strings.TrimSpace(toolName) == "" {
		return
	}
	if r.workflowAutoApproval == nil {
		r.workflowAutoApproval = make(map[string]map[string]workflowApprovalScope)
	}
	if r.workflowAutoApproval[workflowName] == nil {
		r.workflowAutoApproval[workflowName] = make(map[string]workflowApprovalScope)
	}
	key := workflowAutoApprovalKey(stage, toolName)
	r.workflowAutoApproval[workflowName][key] = workflowApprovalScope{Workflow: workflowName, Stage: stage, ToolName: toolName}
}

func (r *Runtime) DisableWorkflowAutoApproval(workflowName string) {
	if r == nil || r.workflowAutoApproval == nil {
		return
	}
	delete(r.workflowAutoApproval, workflowName)
}

func (r *Runtime) shouldAutoApproveWorkflowCall(item pendingApproval) bool {
	if r == nil || r.workflowAutoApproval == nil {
		return false
	}
	scopes, ok := r.workflowAutoApproval[item.workflow]
	if !ok {
		return false
	}
	scope, ok := scopes[workflowAutoApprovalKey(item.stage, item.tool.Name)]
	if !ok {
		return false
	}
	return scope.Workflow == item.workflow && scope.Stage == item.stage && scope.ToolName == item.tool.Name
}

func workflowAutoApprovalKey(stage WorkflowStage, toolName string) string {
	return string(stage) + "\x00" + strings.TrimSpace(toolName)
}

func (r *Runtime) AutoApprovedToolResult(ctx context.Context, id string) (schema.ToolResult, bool) {
	if r == nil || strings.TrimSpace(id) == "" {
		return schema.ToolResult{}, false
	}
	if r.autoApprovedResults != nil {
		if result, ok := r.autoApprovedResults[id]; ok {
			delete(r.autoApprovedResults, id)
			return result, true
		}
	}
	pending, ok := r.approvals.Get(id)
	if !ok || !r.shouldAutoApproveWorkflowCall(pending) {
		return schema.ToolResult{}, false
	}
	result, err := r.ApproveToolCall(ctx, id)
	if err != nil {
		return schema.ToolResult{}, false
	}
	if strings.TrimSpace(result.Content) == "ok" {
		result.Content = fmt.Sprintf("done: %s", result.ToolName)
	}
	return result, true
}

func (r *Runtime) rememberApprovedToolScope(tool schema.Tool) {
	if r == nil || r.session == nil {
		return
	}
	workspace := workspaceRoot(r)
	toolName := strings.TrimSpace(tool.Name)
	if strings.TrimSpace(workspace) == "" || toolName == "" {
		return
	}
	kind := string(policy.KindForTool(tool))
	if strings.TrimSpace(kind) == "" {
		kind = "unknown"
	}
	r.session.RememberApprovedToolScope(workspace, kind, toolName)
}

func workspaceRoot(r *Runtime) string {
	if r == nil || r.cfg == nil {
		return ""
	}
	return strings.TrimSpace(r.cfg.WorkspaceRoot)
}

type ordinaryChatIntent struct {
	Mode   string
	Reason string
	Score  int
	Term   string
	Hits   []string
}

func classifyOrdinaryChatIntentDetail(input string) ordinaryChatIntent {
	text := strings.ToLower(strings.TrimSpace(input))
	if text == "" {
		return ordinaryChatIntent{}
	}
	if shouldKeepOrdinaryChatOnDefaultAgent(text) {
		return ordinaryChatIntent{}
	}

	planIntent := scoreOrdinaryChatIntent(text, "plan", []string{
		"plan", "design", "proposal", "architecture", "approach", "strategy", "break down", "roadmap", "steps", "step by step", "how should", "how do we", "how to approach", "execution plan", "implementation plan",
		"\u8ba1\u5212", "\u6267\u884c\u8ba1\u5212", "\u5236\u5b9a\u8ba1\u5212", "\u89c4\u5212", "\u65b9\u6848", "\u62c6\u89e3\u4efb\u52a1",
		"\u8bbe\u8ba1", "\u67b6\u6784", "\u601d\u8def", "\u62c6\u89e3", "\u8def\u7ebf", "\u6b65\u9aa4", "\u5982\u4f55", "\u600e\u4e48", "\u5e94\u8be5\u600e\u4e48", "\u600e\u4e48\u89c4\u5212",
	})
	auditIntent := scoreOrdinaryChatIntent(text, "audit", []string{
		"review", "audit", "inspect", "analyze", "assessment", "risk", "security review", "code review", "evaluate", "compatibility", "impact", "trade-off", "take a look", "judge whether", "worth doing", "worth it", "do you think", "safe", "reasonable", "vulnerability", "vulnerability research", "exploitability", "cve", "attack surface", "web vulnerability", "binary vulnerability", "reverse engineering", "binary audit", "memory corruption",
		"\u5ba1\u8ba1", "\u4ee3\u7801\u5ba1\u8ba1", "\u6f0f\u6d1e", "\u6f0f\u6d1e\u6316\u6398", "\u6f0f\u6d1e\u5206\u6790", "\u5b89\u5168", "\u5b89\u5168\u5ba1\u8ba1", "\u5b89\u5168\u7814\u7a76", "\u6e17\u900f", "web\u6f0f\u6d1e", "web \u6f0f\u6d1e", "\u7f51\u9875\u6f0f\u6d1e", "\u7f51\u7ad9\u6f0f\u6d1e", "\u524d\u7aef\u6f0f\u6d1e", "\u4e8c\u8fdb\u5236\u6f0f\u6d1e", "\u4e8c\u8fdb\u5236\u9006\u5411", "\u9006\u5411\u6f0f\u6d1e", "\u7cfb\u7edf\u8f6f\u4ef6\u6f0f\u6d1e", "\u56fa\u4ef6\u6f0f\u6d1e", "\u5185\u5b58\u7834\u574f",
		"\u5ba1\u67e5", "\u8bc4\u4f30", "\u68c0\u67e5", "\u590d\u67e5", "\u5206\u6790", "\u98ce\u9669", "\u5b89\u5168\u68c0\u67e5", "\u4ee3\u7801\u5ba1\u67e5", "\u517c\u5bb9\u6027", "\u5f71\u54cd", "\u6743\u8861", "\u5148\u5e2e\u6211\u770b", "\u770b\u4e00\u4e0b", "\u503c\u4e0d\u503c\u5f97", "\u5224\u65ad", "\u4f60\u89c9\u5f97", "\u9760\u8c31",
	})
	fixIntent := scoreOrdinaryChatIntent(text, "fix", []string{
		"implement", "fix", "update", "modify", "add", "extend", "expand", "enhance", "debug", "patch", "refactor", "rewrite", "edit", "build", "create", "write code", "apply the change", "complete", "finish", "ship", "integrate", "wire up", "support",
		"\u4f18\u5316", "\u4fee\u6539", "\u4fee\u590d", "\u5b9e\u73b0", "\u5b8c\u5584", "\u91cd\u6784", "\u8c03\u6574", "\u6539\u8fdb", "\u6dfb\u52a0", "\u65b0\u589e", "\u62d3\u5c55", "\u6269\u5c55", "\u589e\u5f3a", "\u7f16\u5199\u4ee3\u7801", "\u521b\u5efa", "\u63a5\u5165",
		"\u65b0\u529f\u80fd", "\u8c03\u8bd5", "\u8865\u4e01", "\u6539\u4ee3\u7801", "\u76f4\u63a5\u4fee\u6539", "\u5e94\u7528\u4fee\u6539", "\u505a\u5b8c", "\u5b8c\u6210", "\u843d\u5730", "\u63a5\u4e0a", "\u652f\u6301",
	})

	best := ordinaryChatIntent{}
	for _, candidate := range []ordinaryChatIntent{fixIntent, auditIntent, planIntent} {
		if candidate.Score > best.Score {
			best = candidate
		}
	}
	if best.Score == 0 {
		return ordinaryChatIntent{}
	}
	return best
}

func shouldKeepOrdinaryChatOnDefaultAgent(text string) bool {
	if !containsAny(text,
		"advice", "rough", "where to start",
		"\u5efa\u8bae", "\u5927\u6982", "\u4ece\u54ea\u91cc\u5f00\u59cb",
	) {
		return false
	}
	if containsAny(text,
		"how should", "how do we", "how to approach", "step by step",
		"\u5e94\u8be5\u600e\u4e48", "\u600e\u4e48\u89c4\u5212", "\u89c4\u5212", "\u6b65\u9aa4", "\u62c6\u89e3",
		"review", "audit", "inspect", "analyze", "assessment", "risk", "security review", "code review", "evaluate", "compatibility", "impact", "trade-off",
		"\u5ba1\u67e5", "\u5ba1\u8ba1", "\u8bc4\u5ba1", "\u68c0\u67e5", "\u590d\u67e5", "\u5206\u6790", "\u98ce\u9669", "\u5b89\u5168\u68c0\u67e5", "\u4ee3\u7801\u5ba1\u67e5", "\u8bc4\u4f30", "\u517c\u5bb9\u6027", "\u5f71\u54cd", "\u6743\u8861",
		"implement", "fix", "update", "modify", "add", "extend", "expand", "enhance", "debug", "patch", "refactor", "rewrite", "edit", "build", "create", "write code", "apply the change", "complete", "finish", "ship", "integrate", "wire up", "support",
		"\u4f18\u5316", "\u4fee\u6539", "\u4fee\u590d", "\u5b9e\u73b0", "\u5b8c\u5584", "\u91cd\u6784", "\u8c03\u6574", "\u6539\u8fdb", "\u6dfb\u52a0", "\u65b0\u589e", "\u62d3\u5c55", "\u6269\u5c55", "\u65b0\u529f\u80fd", "\u589e\u5f3a", "\u8c03\u8bd5", "\u8865\u4e01", "\u6539\u4ee3\u7801", "\u76f4\u63a5\u4fee\u6539", "\u521b\u5efa", "\u7f16\u5199\u4ee3\u7801", "\u5e94\u7528\u4fee\u6539", "\u505a\u5b8c", "\u5b8c\u6210", "\u843d\u5730", "\u63a5\u5165", "\u63a5\u4e0a", "\u652f\u6301",
	) {
		return false
	}
	return true
}

func scoreOrdinaryChatIntent(text, mode string, terms []string) ordinaryChatIntent {
	intent := ordinaryChatIntent{Mode: mode}
	for _, term := range terms {
		if strings.Contains(text, term) {
			intent.Score++
			intent.Hits = append(intent.Hits, term)
			if intent.Term == "" {
				intent.Term = term
			}
		}
	}
	if intent.Score > 0 {
		intent.Reason = fmt.Sprintf("%s:%s", mode, strings.Join(intent.Hits, ","))
	}
	return intent
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func (r *Runtime) routeOrdinaryChat(input string) (string, ordinaryChatIntent, bool) {
	if r == nil {
		return "", ordinaryChatIntent{}, false
	}
	intent := classifyOrdinaryChatIntentDetail(input)
	if intent.Mode == "" {
		return r.ActiveAgent(), ordinaryChatIntent{}, false
	}
	for _, agentID := range r.AgentNames() {
		profile, ok := r.Profile(agentID)
		if !ok {
			continue
		}
		if profile.Mode == intent.Mode {
			return agentID, intent, agentID != r.ActiveAgent()
		}
	}
	return r.ActiveAgent(), intent, false
}

// DenyToolCall rejects a pending tool call.
func (r *Runtime) DenyToolCall(id string) (schema.ToolResult, error) {
	decision := r.ResolvePendingApproval(id, false)
	if !decision.Found {
		return schema.ToolResult{}, fmt.Errorf("pending approval not found: %s", id)
	}
	pending := decision.Pending
	result := schema.ToolResult{
		CallID:   pending.call.ID,
		ToolName: pending.tool.Name,
		Content:  fmt.Sprintf("tool %s denied by operator", pending.tool.Name),
		IsError:  true,
		Denied:   true,
	}
	if r.audit != nil {
		r.audit.Record(schema.AuditEntry{Type: "tool_call", AgentID: pending.agent, ToolName: pending.tool.Name, Outcome: "denied", Detail: result.Content})
	}
	r.recordOrdinaryResumeResult(pending, result)
	return result, nil
}

// RunStream executes the request with the active agent.
func (r *Runtime) RunStream(ctx context.Context, input string, handler func(event schema.StreamEvent) error) (schema.AgentResult, error) {
	r.ensureOrdinaryChatAgentState()
	sourceAgent := r.ActiveAgent()
	if result, handled, err := r.handlePendingOrdinaryChatHandoff(ctx, input, handler); handled {
		return result, err
	}
	targetAgent := r.ActiveAgent()
	rerouted := false
	intent := ordinaryChatIntent{}
	if r == nil || r.session == nil || !r.workflowInProgress() {
		targetAgent, intent, rerouted = r.routeOrdinaryChat(input)
	}
	if strings.TrimSpace(targetAgent) != "" && targetAgent != r.ActiveAgent() {
		if err := r.SetActiveAgent(targetAgent); err != nil {
			return schema.AgentResult{}, err
		}
	}
	if rerouted {
		outcome := "rerouted"
		if targetAgent == r.ActiveAgent() {
			outcome = "matched_active"
		}
		r.recordOrdinaryChatRoute(input, targetAgent, intent, outcome)
	}
	if !rerouted && intent.Mode == "" {
		r.recordOrdinaryChatRoute(input, r.ActiveAgent(), ordinaryChatIntent{}, "stayed_default")
	}
	if rerouted && r.audit != nil {
		detail := intent.Mode
		if strings.TrimSpace(intent.Reason) != "" {
			detail = intent.Reason
		}
		r.audit.Record(schema.AuditEntry{Type: "intent_route", AgentID: targetAgent, Outcome: "rerouted", Detail: detail})
	}
	runner, ok := r.runners[r.ActiveAgent()]
	if !ok {
		return schema.AgentResult{}, fmt.Errorf("active agent not configured: %s", r.ActiveAgent())
	}
	result, err := runner.RunStream(ctx, input, r.skills, r.mcp, r.session, r.audit, r, handler)
	if err != nil {
		return schema.AgentResult{}, err
	}
	result.AgentID = runner.id
	if strings.TrimSpace(result.Mode) == "" {
		result.Mode = r.Mode()
	}
	if strings.EqualFold(result.Mode, "plan") && !strings.EqualFold(intent.Mode, "plan") && len(result.ToolResults) == 0 {
		r.queueOrdinaryChatHandoff(input, result.Output, workflowAgentFixer, "fix")
	}
	if err := r.maybeRunVerifierPass(ctx, input, &result, handler); err != nil {
		return schema.AgentResult{}, err
	}
	result.AuditTrail = r.AuditTrail()
	_ = r.restoreAgentAfterCompletedTurn(sourceAgent)
	return result, nil
}

// WorkflowRunner constructs a staged workflow helper for this runtime.
func (r *Runtime) WorkflowRunner() *WorkflowRunner {
	return NewWorkflowRunner(r)
}

// RuntimeHome returns the framework runtime directory used for configs, skills, and workflow graphs.
func (r *Runtime) RuntimeHome() string {
	if r == nil || r.cfg == nil {
		return ""
	}
	return strings.TrimSpace(r.cfg.RuntimeHome)
}

// SkillList returns loaded skills for workflow authoring and diagnostics.
func (r *Runtime) SkillList() []schema.Skill {
	if r == nil || r.skills == nil {
		return nil
	}
	return r.skills.List()
}

// StatusLines returns human-readable runtime status information.
func (r *Runtime) StatusLines(ctx context.Context) []string {
	lines := []string{
		fmt.Sprintf("active agent: %s", r.ActiveAgent()),
		fmt.Sprintf("mode: %s", r.Mode()),
		fmt.Sprintf("trace: %t", r.TraceEnabled()),
	}
	snapshot := r.SessionSnapshot()
	if strings.TrimSpace(snapshot.Workflow.Name) != "" || strings.TrimSpace(snapshot.Workflow.Status) != "" {
		lines = append(lines, fmt.Sprintf("workflow %s: %s (next=%s)", snapshot.Workflow.Name, snapshot.Workflow.Status, fallbackText(snapshot.Workflow.NextStage, "-")))
	}
	if snapshot.LastSkillMatch != nil && strings.TrimSpace(snapshot.LastSkillMatch.SkillName) != "" {
		lines = append(lines, formatSkillMatchStatus(*snapshot.LastSkillMatch))
	} else if strings.TrimSpace(snapshot.LastSkill) != "" {
		lines = append(lines, fmt.Sprintf("skill: %s", snapshot.LastSkill))
	}
	if strings.TrimSpace(snapshot.TaskStage.Stage) != "" {
		lines = append(lines, fmt.Sprintf("task: stage=%s agent=%s mode=%s detail=%s", snapshot.TaskStage.Stage, fallbackText(snapshot.TaskStage.AgentID, "-"), fallbackText(snapshot.TaskStage.Mode, "-"), fallbackText(truncateSummary(snapshot.TaskStage.Detail), "-")))
	}
	if strings.TrimSpace(snapshot.PendingHandoff.TargetAgent) != "" || strings.TrimSpace(snapshot.PendingHandoff.ExpectedAction) != "" {
		lines = append(lines, fmt.Sprintf("handoff: target=%s mode=%s action=%s request=%s", fallbackText(snapshot.PendingHandoff.TargetAgent, "-"), fallbackText(snapshot.PendingHandoff.TargetMode, "-"), fallbackText(snapshot.PendingHandoff.ExpectedAction, "-"), fallbackText(truncateSummary(snapshot.PendingHandoff.Request), "-")))
	}
	if strings.TrimSpace(snapshot.LastRouting.Outcome) != "" || strings.TrimSpace(snapshot.LastRouting.TargetAgent) != "" {
		lines = append(lines, fmt.Sprintf("routing: outcome=%s target=%s mode=%s request=%s reason=%s", fallbackText(snapshot.LastRouting.Outcome, "-"), fallbackText(snapshot.LastRouting.TargetAgent, "-"), fallbackText(snapshot.LastRouting.TargetMode, "-"), fallbackText(truncateSummary(snapshot.LastRouting.Request), "-"), fallbackText(truncateSummary(snapshot.LastRouting.Reason), "-")))
	}
	for _, pending := range snapshot.PendingApprovals {
		lines = append(lines, fmt.Sprintf("approval %s: %s (%s) workflow=%s stage=%s args=%s", pending.CallID, pending.ToolName, pending.AgentID, fallbackText(pending.WorkflowName, "-"), fallbackText(pending.Stage, "-"), fallbackText(pending.ArgumentsSummary, "-")))
	}
	status := r.mcp.HealthStatus(ctx)
	keys := make([]string, 0, len(status))
	for key := range status {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("mcp %s: %s", key, status[key]))
	}
	return lines
}

func formatSkillMatchStatus(diagnostic schema.SkillMatchDiagnostic) string {
	parts := []string{
		fmt.Sprintf("skill: %s", diagnostic.SkillName),
		fmt.Sprintf("score=%d", diagnostic.Score),
	}
	if len(diagnostic.KeywordHits) > 0 {
		parts = append(parts, "keywords="+strings.Join(diagnostic.KeywordHits, ","))
	}
	if strings.TrimSpace(diagnostic.Reason) != "" {
		parts = append(parts, "reason="+truncateSummary(diagnostic.Reason))
	}
	return strings.Join(parts, " ")
}
