package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"gopkg.in/yaml.v3"
)

func (s *Server) handleWorkflowGraphCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.runtime.WorkflowRunner().ListWorkflowGraphs())
	case http.MethodPost:
		if strings.Trim(r.URL.Path, "/") == "api/workflow-graphs/validate" {
			s.handleWorkflowGraphValidate(w, r, "")
			return
		}
		if strings.Trim(r.URL.Path, "/") == "api/workflow-graphs/validate-expression" {
			s.handleWorkflowGraphExpressionValidate(w, r, "")
			return
		}
		if strings.Trim(r.URL.Path, "/") == "api/workflow-graphs/import" {
			s.handleWorkflowGraphImport(w, r)
			return
		}
		var doc agent.WorkflowGraphDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(doc.Name) == "" {
			http.Error(w, "workflow name is required", http.StatusBadRequest)
			return
		}
		if err := s.runtime.WorkflowRunner().SaveWorkflowGraphDocument(doc.Name, doc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSONStatus(w, http.StatusCreated, doc)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowGraphItem(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workflow-graphs/"), "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		http.NotFound(w, r)
		return
	}
	if parts[0] == "validate" && len(parts) == 1 && r.Method == http.MethodPost {
		s.handleWorkflowGraphValidate(w, r, "")
		return
	}
	if parts[0] == "validate-expression" && len(parts) == 1 && r.Method == http.MethodPost {
		s.handleWorkflowGraphExpressionValidate(w, r, "")
		return
	}
	if parts[0] == "import" && len(parts) == 1 && r.Method == http.MethodPost {
		s.handleWorkflowGraphImport(w, r)
		return
	}
	name := parts[0]
	if name == "" {
		http.NotFound(w, r)
		return
	}
	runner := s.runtime.WorkflowRunner()
	switch r.Method {
	case http.MethodGet:
		if len(parts) == 2 && parts[1] == "export" {
			s.handleWorkflowGraphExport(w, r, name)
			return
		}
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		doc, err := runner.LoadWorkflowGraphDocument(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, doc)
	case http.MethodPut:
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		var doc agent.WorkflowGraphDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if err := runner.SaveWorkflowGraphDocument(name, doc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		updated, err := runner.LoadWorkflowGraphDocument(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, updated)
	case http.MethodPost:
		if len(parts) == 2 && parts[1] == "validate" {
			s.handleWorkflowGraphValidate(w, r, name)
			return
		}
		if len(parts) == 2 && parts[1] == "validate-expression" {
			s.handleWorkflowGraphExpressionValidate(w, r, name)
			return
		}
		http.NotFound(w, r)
	case http.MethodDelete:
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		if err := runner.DeleteWorkflowGraphDocument(name); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowGraphValidate(w http.ResponseWriter, r *http.Request, name string) {
	doc, err := decodeWorkflowGraphDocument(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(name) == "" {
		name = doc.Name
	}
	writeJSON(w, s.runtime.WorkflowRunner().ValidateWorkflowGraphDocument(name, doc))
}

type workflowGraphExpressionValidationRequest struct {
	Workflow   agent.WorkflowGraphDocument `json:"workflow,omitempty"`
	Graph      agent.WorkflowGraphDocument `json:"graph,omitempty"`
	Document   agent.WorkflowGraphDocument `json:"document,omitempty"`
	Expression string                      `json:"expression,omitempty"`
	Reference  string                      `json:"reference,omitempty"`
	Mode       string                      `json:"mode,omitempty"`
	Stage      string                      `json:"stage,omitempty"`
	Field      string                      `json:"field,omitempty"`
	RunID      string                      `json:"run_id,omitempty"`
}

func (s *Server) handleWorkflowGraphExpressionValidate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req workflowGraphExpressionValidationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	doc := firstWorkflowGraphExpressionDocument(req.Workflow, req.Graph, req.Document)
	if len(doc.Stages) == 0 && strings.TrimSpace(name) != "" {
		loaded, err := s.runtime.WorkflowRunner().LoadWorkflowGraphDocument(name)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		doc = loaded
	}
	if strings.TrimSpace(name) == "" {
		name = doc.Name
	}
	expression := strings.TrimSpace(req.Expression)
	mode := req.Mode
	if strings.TrimSpace(req.Reference) != "" {
		expression = strings.TrimSpace(req.Reference)
		mode = "reference"
	}
	if runID := strings.TrimSpace(req.RunID); runID != "" {
		run, ok := workflowRunByID(s.runtime.SessionSnapshot().WorkflowRuns, runID)
		if !ok {
			http.Error(w, "workflow run not found", http.StatusNotFound)
			return
		}
		result := s.runtime.WorkflowRunner().ValidateWorkflowExpressionWithRun(name, doc, run, expression, mode)
		writeJSON(w, result)
		return
	}
	result := s.runtime.WorkflowRunner().ValidateWorkflowExpression(name, doc, expression, mode)
	writeJSON(w, result)
}

func firstWorkflowGraphExpressionDocument(values ...agent.WorkflowGraphDocument) agent.WorkflowGraphDocument {
	for _, value := range values {
		if strings.TrimSpace(value.Name) != "" || len(value.Stages) > 0 {
			return value
		}
	}
	return agent.WorkflowGraphDocument{}
}

func (s *Server) handleWorkflowGraphImport(w http.ResponseWriter, r *http.Request) {
	doc, err := decodeWorkflowGraphDocument(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(doc.Name) == "" {
		http.Error(w, "workflow name is required", http.StatusBadRequest)
		return
	}
	if err := s.runtime.WorkflowRunner().SaveWorkflowGraphDocument(doc.Name, doc); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	updated, err := s.runtime.WorkflowRunner().LoadWorkflowGraphDocument(doc.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONStatus(w, http.StatusCreated, updated)
}

func (s *Server) handleWorkflowGraphExport(w http.ResponseWriter, r *http.Request, name string) {
	doc, err := s.runtime.WorkflowRunner().LoadWorkflowGraphDocument(name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, contentType, err := agent.RenderWorkflowGraphDocument(doc, r.URL.Query().Get("format"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ext := "yaml"
	if strings.Contains(contentType, "json") {
		ext = "json"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, doc.Name, ext))
	_, _ = w.Write(data)
}

func decodeWorkflowGraphDocument(r *http.Request) (agent.WorkflowGraphDocument, error) {
	var doc agent.WorkflowGraphDocument
	data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return doc, fmt.Errorf("read workflow graph: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return doc, fmt.Errorf("workflow graph body is required")
	}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "yaml") || strings.Contains(contentType, "yml") {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return doc, fmt.Errorf("invalid workflow yaml: %w", err)
		}
		return doc, nil
	}
	if err := json.Unmarshal(data, &doc); err == nil {
		return doc, nil
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("invalid workflow graph: %w", err)
	}
	return doc, nil
}

func (s *Server) handleWorkflowOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.runtime.WorkflowRunner().WorkflowOptions())
}

func (s *Server) handleWorkflowExpressionFunctions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	functions := s.runtime.WorkflowRunner().WorkflowExpressionFunctions()
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	nodeType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("node_type")))
	if mode != "" || nodeType != "" {
		filtered := make([]agent.WorkflowExpressionFunctionOption, 0, len(functions))
		for _, function := range functions {
			if mode != "" && !workflowExpressionOptionContains(function.Modes, mode) {
				continue
			}
			if nodeType != "" && !workflowExpressionOptionContains(function.NodeTypes, nodeType) {
				continue
			}
			filtered = append(filtered, function)
		}
		functions = filtered
	}
	writeJSON(w, functions)
}

func workflowExpressionOptionContains(values []string, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return true
	}
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), needle) {
			return true
		}
	}
	return false
}

func (s *Server) handleWorkflowTemplateCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.runtime.WorkflowRunner().WorkflowTemplates())
}

func (s *Server) handleWorkflowTemplateItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workflow-templates/"), "/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	template, ok := s.runtime.WorkflowRunner().WorkflowTemplate(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, template)
}

func writeJSON(w http.ResponseWriter, value any) {
	writeJSONStatus(w, http.StatusOK, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
