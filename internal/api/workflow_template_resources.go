package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
)

type workflowTemplateCaptureRequest struct {
	Workflow    string `json:"workflow,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type workflowTemplateForkRequest struct {
	Source      string `json:"source,omitempty"`
	Template    string `json:"template,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

func (s *Server) handleWorkflowTemplateResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/workflow-templates/validate" {
		s.handleWorkflowTemplateResourceValidate(w, r, "")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.runtime.WorkflowTemplateResources())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowTemplateResourceItem(w http.ResponseWriter, r *http.Request) {
	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/workflow-templates/"), "/")
	if suffix == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(suffix, "/")
	name := parts[0]
	if strings.TrimSpace(name) == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 && parts[0] == "validate" && r.Method == http.MethodPost {
		s.handleWorkflowTemplateResourceValidate(w, r, "")
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "capture":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleWorkflowTemplateResourceCapture(w, r, name)
			return
		case "fork":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleWorkflowTemplateResourceFork(w, r, name)
			return
		case "validate":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleWorkflowTemplateResourceValidate(w, r, name)
			return
		default:
			http.NotFound(w, r)
			return
		}
	}
	if len(parts) > 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		template, ok := s.runtime.WorkflowRunner().WorkflowTemplate(name)
		if !ok || !template.Custom {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, template)
	case http.MethodPut:
		template, err := decodeWorkflowTemplateResource(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved, err := s.runtime.SaveWorkflowTemplateResource(name, template)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.runtime.DeleteWorkflowTemplateResource(name); err != nil {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowTemplateResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	template, err := decodeWorkflowTemplateResource(r)
	if err != nil {
		writeJSON(w, invalidResourceValidation("workflow_template", name, "body", err.Error()))
		return
	}
	normalized, err := s.runtime.ValidateWorkflowTemplateResource(name, template)
	if err != nil {
		writeJSON(w, invalidResourceValidation("workflow_template", firstResourceValidationName(name, template.Name, template.Graph.Name), workflowTemplateValidationField(err.Error()), err.Error()))
		return
	}
	writeJSON(w, validResourceValidation("workflow_template", normalized.Name, normalized))
}

func (s *Server) handleWorkflowTemplateResourceCapture(w http.ResponseWriter, r *http.Request, name string) {
	var req workflowTemplateCaptureRequest
	if r.Body != nil {
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(string(data)) != "" {
			if err := json.Unmarshal(data, &req); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
		}
	}
	template, err := s.runtime.CaptureWorkflowTemplateResource(name, req.Workflow, req.Title, req.Description)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSONStatus(w, http.StatusCreated, template)
}

func (s *Server) handleWorkflowTemplateResourceFork(w http.ResponseWriter, r *http.Request, name string) {
	var req workflowTemplateForkRequest
	if r.Body != nil {
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(string(data)) != "" {
			if err := json.Unmarshal(data, &req); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
		}
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = strings.TrimSpace(req.Template)
	}
	template, err := s.runtime.ForkWorkflowTemplateResource(name, source, req.Title, req.Description)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSONStatus(w, http.StatusCreated, template)
}

func decodeWorkflowTemplateResource(r *http.Request) (agent.WorkflowTemplate, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return agent.WorkflowTemplate{}, err
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return agent.WorkflowTemplate{}, fmt.Errorf("workflow template body is required")
	}
	var template agent.WorkflowTemplate
	if err := json.Unmarshal(data, &template); err == nil && (template.Name != "" || len(template.Graph.Stages) > 0) {
		return template, nil
	}
	var graph agent.WorkflowGraphDocument
	if err := json.Unmarshal(data, &graph); err != nil {
		return agent.WorkflowTemplate{}, fmt.Errorf("invalid workflow template json")
	}
	if strings.TrimSpace(graph.Name) == "" {
		return agent.WorkflowTemplate{}, fmt.Errorf("workflow template requires name or graph.name")
	}
	return agent.WorkflowTemplate{
		WorkflowTemplateSummary: agent.WorkflowTemplateSummary{
			Name:        graph.Name,
			Title:       graph.Name,
			Description: graph.Description,
			Category:    "custom",
		},
		Graph: graph,
	}, nil
}

func workflowTemplateValidationField(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "version"):
		return "version"
	case strings.Contains(lower, "kind"):
		return "kind"
	case strings.Contains(lower, "stage"):
		return "graph.stages"
	case strings.Contains(lower, "next"):
		return "graph.stages.next"
	case strings.Contains(lower, "graph"):
		return "graph"
	case strings.Contains(lower, "agent"):
		return "graph.stages.agent"
	case strings.Contains(lower, "skill"):
		return "graph.stages.skill"
	case strings.Contains(lower, "tool"):
		return "graph.stages.tool"
	case strings.Contains(lower, "name"):
		return "name"
	default:
		return ""
	}
}
