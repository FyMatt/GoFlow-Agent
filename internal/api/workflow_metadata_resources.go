package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
)

func (s *Server) handleWorkflowNodeMetadataResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/workflow-node-metadata/validate" {
		s.handleWorkflowNodeMetadataResourceValidate(w, r, "")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.runtime.WorkflowNodeMetadataResources())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowNodeMetadataResourceItem(w http.ResponseWriter, r *http.Request) {
	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/workflow-node-metadata/"), "/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 1 && parts[0] == "validate" && r.Method == http.MethodPost {
		s.handleWorkflowNodeMetadataResourceValidate(w, r, "")
		return
	}
	if len(parts) == 2 && parts[1] == "validate" && r.Method == http.MethodPost {
		s.handleWorkflowNodeMetadataResourceValidate(w, r, parts[0])
		return
	}
	if suffix == "" || strings.Contains(suffix, "/") {
		http.NotFound(w, r)
		return
	}
	name := suffix
	switch r.Method {
	case http.MethodGet:
		resource, ok := s.runtime.WorkflowNodeMetadataResource(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, resource)
	case http.MethodPut:
		resource, err := decodeWorkflowNodeMetadataResource(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved, err := s.runtime.SaveWorkflowNodeMetadataResource(name, resource)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.runtime.DeleteWorkflowNodeMetadataResource(name); err != nil {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowNodeMetadataResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resource, err := decodeWorkflowNodeMetadataResource(r)
	if err != nil {
		writeJSON(w, invalidResourceValidation("workflow_node_metadata", name, "body", err.Error()))
		return
	}
	normalized, err := s.runtime.ValidateWorkflowNodeMetadataResource(name, resource)
	if err != nil {
		writeJSON(w, invalidResourceValidation("workflow_node_metadata", firstResourceValidationName(name, resource.Type), workflowMetadataValidationField(err.Error()), err.Error()))
		return
	}
	writeJSON(w, validResourceValidation("workflow_node_metadata", normalized.Type, normalized))
}

func (s *Server) handleExpressionHelperResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/expression-helpers/validate" {
		s.handleExpressionHelperResourceValidate(w, r, "")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.runtime.WorkflowExpressionMetadataResources())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleExpressionHelperResourceItem(w http.ResponseWriter, r *http.Request) {
	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/expression-helpers/"), "/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 1 && parts[0] == "validate" && r.Method == http.MethodPost {
		s.handleExpressionHelperResourceValidate(w, r, "")
		return
	}
	if len(parts) == 2 && parts[1] == "validate" && r.Method == http.MethodPost {
		s.handleExpressionHelperResourceValidate(w, r, parts[0])
		return
	}
	if suffix == "" || strings.Contains(suffix, "/") {
		http.NotFound(w, r)
		return
	}
	name := suffix
	switch r.Method {
	case http.MethodGet:
		resource, ok := s.runtime.WorkflowExpressionMetadataResource(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, resource)
	case http.MethodPut:
		resource, err := decodeExpressionHelperResource(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved, err := s.runtime.SaveWorkflowExpressionMetadataResource(name, resource)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.runtime.DeleteWorkflowExpressionMetadataResource(name); err != nil {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleExpressionHelperResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resource, err := decodeExpressionHelperResource(r)
	if err != nil {
		writeJSON(w, invalidResourceValidation("expression_helper", name, "body", err.Error()))
		return
	}
	normalized, err := s.runtime.ValidateWorkflowExpressionMetadataResource(name, resource)
	if err != nil {
		writeJSON(w, invalidResourceValidation("expression_helper", firstResourceValidationName(name, resource.Name), workflowMetadataValidationField(err.Error()), err.Error()))
		return
	}
	writeJSON(w, validResourceValidation("expression_helper", normalized.Name, normalized))
}

func decodeWorkflowNodeMetadataResource(r *http.Request) (agent.WorkflowNodeTypeOption, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return agent.WorkflowNodeTypeOption{}, err
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return agent.WorkflowNodeTypeOption{}, fmt.Errorf("workflow node metadata body is required")
	}
	var resource agent.WorkflowNodeTypeOption
	if err := json.Unmarshal(data, &resource); err != nil {
		return agent.WorkflowNodeTypeOption{}, fmt.Errorf("invalid workflow node metadata json")
	}
	return resource, nil
}

func workflowMetadataValidationField(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "kind"):
		return "kind"
	case strings.Contains(lower, "version"):
		return "version"
	case strings.Contains(lower, "unknown node type") || strings.Contains(lower, "type"):
		return "type"
	case strings.Contains(lower, "unknown helper") || strings.Contains(lower, "name"):
		return "name"
	case strings.Contains(lower, "field"):
		return "fields"
	case strings.Contains(lower, "output"):
		return "outputs"
	case strings.Contains(lower, "arg"):
		return "args"
	default:
		return ""
	}
}

func decodeExpressionHelperResource(r *http.Request) (agent.WorkflowExpressionFunctionOption, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return agent.WorkflowExpressionFunctionOption{}, err
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return agent.WorkflowExpressionFunctionOption{}, fmt.Errorf("expression helper metadata body is required")
	}
	var resource agent.WorkflowExpressionFunctionOption
	if err := json.Unmarshal(data, &resource); err != nil {
		return agent.WorkflowExpressionFunctionOption{}, fmt.Errorf("invalid expression helper metadata json")
	}
	return resource, nil
}
