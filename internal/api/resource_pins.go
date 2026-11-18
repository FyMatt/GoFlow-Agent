package api

import (
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/config"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
	"github.com/FyMatt/GoFlow-Agent/internal/version"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

func (s *Server) agentTaskResourcePins(agentID, skillName, toolName string) []session.ResourcePin {
	if s == nil || s.runtime == nil {
		return nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = strings.TrimSpace(s.runtime.ActiveAgent())
	}
	pins := s.agentAndProviderResourcePins(agentID)
	if skill, ok := s.runtimeSkillByName(skillName); ok {
		pins = append(pins, s.skillAndMCPResourcePins(skill)...)
	}
	if server, ok := s.runtimeMCPServerForTool(toolName); ok {
		pins = append(pins, mcpServerResourcePin(server))
	}
	return session.NormalizeResourcePins(pins)
}

func (s *Server) workflowTaskResourcePins(name string) []session.ResourcePin {
	name = strings.TrimSpace(name)
	if s == nil || s.runtime == nil || name == "" {
		return nil
	}
	runner := s.runtime.WorkflowRunner()
	doc, err := runner.LoadRunnableWorkflowGraphDocument(name)
	if err != nil {
		// Built-in workflows have no editable graph document. The runtime version
		// is part of their implementation identity.
		pins := []session.ResourcePin{session.NewResourcePin("workflow", name, struct {
			Name           string `json:"name"`
			RuntimeVersion string `json:"runtime_version"`
		}{Name: name, RuntimeVersion: version.Version})}
		return session.MergeResourcePins(pins, s.builtInWorkflowResourcePins(name))
	}
	pins := []session.ResourcePin{session.NewResourcePin("workflow", name, doc)}
	for _, stage := range doc.Stages {
		pins = append(pins, s.agentAndProviderResourcePins(stage.Agent)...)
		if provider := strings.TrimSpace(stage.Model.Provider); provider != "" {
			pins = append(pins, s.providerAndFallbackResourcePins(provider, nil)...)
		}
		if skill, ok := s.runtimeSkillByName(stage.Skill); ok {
			pins = append(pins, s.skillAndMCPResourcePins(skill)...)
		}
		if server, ok := s.runtimeMCPServerForTool(stage.Tool); ok {
			pins = append(pins, mcpServerResourcePin(server))
		}
	}
	for _, requirement := range runner.WorkflowGraphStageRequirements(doc) {
		for _, name := range requirement.Tools {
			if server, ok := s.runtimeMCPServerForTool(name); ok {
				pins = append(pins, mcpServerResourcePin(server))
			}
		}
	}
	return session.NormalizeResourcePins(pins)
}

func (s *Server) agentAndProviderResourcePins(agentID string) []session.ResourcePin {
	agentID = strings.TrimSpace(agentID)
	if s == nil || s.runtime == nil || agentID == "" {
		return nil
	}
	profile, ok := s.runtime.Profile(agentID)
	if !ok {
		return nil
	}
	pins := []session.ResourcePin{session.NewResourcePin("agent", agentID, struct {
		ID      string              `json:"id"`
		Profile config.AgentProfile `json:"profile"`
	}{ID: agentID, Profile: profile})}
	pins = append(pins, s.providerAndFallbackResourcePins(profile.Provider, nil)...)
	return pins
}

func (s *Server) builtInWorkflowResourcePins(name string) []session.ResourcePin {
	if s == nil || s.runtime == nil {
		return nil
	}
	var pins []session.ResourcePin
	addAgent := func(agentID string) {
		pins = append(pins, s.agentAndProviderResourcePins(agentID)...)
	}
	addSkill := func(skillName string) {
		if skill, ok := s.runtimeSkillByName(skillName); ok {
			pins = append(pins, s.skillAndMCPResourcePins(skill)...)
		}
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "plan-fix-audit":
		for _, agentID := range []string{"planner", "fixer", "auditor"} {
			addAgent(agentID)
		}
		for _, skillName := range []string{"execution-plan", "code-writing", "code-audit"} {
			addSkill(skillName)
		}
	case "skill-chain":
		for _, skill := range s.runtime.SkillList() {
			pins = append(pins, s.skillAndMCPResourcePins(skill)...)
			if agentID := strings.TrimSpace(skill.PreferredAgent); agentID != "" {
				addAgent(agentID)
			}
		}
		addAgent("chat")
	}
	return session.NormalizeResourcePins(pins)
}

func (s *Server) skillAndMCPResourcePins(skill schema.Skill) []session.ResourcePin {
	pins := []session.ResourcePin{session.NewResourcePin("skill", skill.Name, skill)}
	for _, tool := range skill.Tools {
		if server, ok := s.runtimeMCPServerForTool(tool.Name); ok {
			pins = append(pins, mcpServerResourcePin(server))
		}
	}
	return session.NormalizeResourcePins(pins)
}

func (s *Server) providerAndFallbackResourcePins(name string, seen map[string]struct{}) []session.ResourcePin {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if seen == nil {
		seen = make(map[string]struct{})
	}
	key := strings.ToLower(name)
	if _, ok := seen[key]; ok {
		return nil
	}
	seen[key] = struct{}{}
	provider, ok := s.runtime.Provider(name)
	if !ok {
		return nil
	}
	provider.APIKey = ""
	pins := []session.ResourcePin{session.NewResourcePin("provider", name, provider)}
	pins = append(pins, s.providerAndFallbackResourcePins(provider.FallbackProvider, seen)...)
	return session.NormalizeResourcePins(pins)
}

func (s *Server) runtimeSkillByName(name string) (schema.Skill, bool) {
	name = strings.TrimSpace(name)
	if s == nil || s.runtime == nil || name == "" {
		return schema.Skill{}, false
	}
	for _, skill := range s.runtime.SkillList() {
		if strings.EqualFold(strings.TrimSpace(skill.Name), name) {
			return skill, true
		}
	}
	return schema.Skill{}, false
}

func (s *Server) runtimeMCPServerForTool(toolName string) (config.MCPServerRef, bool) {
	toolName = strings.TrimSpace(toolName)
	if s == nil || s.runtime == nil || toolName == "" {
		return config.MCPServerRef{}, false
	}
	serverName := toolName
	if slash := strings.Index(serverName, "/"); slash >= 0 {
		serverName = serverName[:slash]
	} else {
		return config.MCPServerRef{}, false
	}
	serverName = strings.TrimSpace(serverName)
	for _, server := range s.runtime.MCPServerRefs() {
		if strings.EqualFold(strings.TrimSpace(server.Name), serverName) {
			return server, true
		}
	}
	return config.MCPServerRef{}, false
}

func mcpServerResourcePin(server config.MCPServerRef) session.ResourcePin {
	server.WorkspaceRoot = ""
	return session.NewResourcePin("mcp", server.Name, server)
}
