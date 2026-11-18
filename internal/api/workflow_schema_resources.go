package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

type workflowSchemaResourceActionRequest struct {
	Merge       *bool  `json:"merge,omitempty"`
	Description string `json:"description,omitempty"`
}

func (s *Server) handleWorkflowSchemaResourceCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && strings.Trim(r.URL.Path, "/") == "api/resources/workflow-schemas/validate" {
		s.handleWorkflowSchemaResourceValidate(w, r, "")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.runtime.WorkflowSchemaResources())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowSchemaResourceItem(w http.ResponseWriter, r *http.Request) {
	suffix := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/resources/workflow-schemas/"), "/")
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
		s.handleWorkflowSchemaResourceValidate(w, r, "")
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "activate":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleWorkflowSchemaResourceActivate(w, r, name)
			return
		case "capture":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleWorkflowSchemaResourceCapture(w, r, name)
			return
		case "validate":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleWorkflowSchemaResourceValidate(w, r, name)
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
		resource, err := s.runtime.WorkflowSchemaResource(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, resource)
	case http.MethodPut:
		resource, err := decodeWorkflowSchemaResource(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		saved, err := s.runtime.SaveWorkflowSchemaResource(name, resource)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, saved)
	case http.MethodDelete:
		if err := s.runtime.DeleteWorkflowSchemaResource(name); err != nil {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowSchemaResourceValidate(w http.ResponseWriter, r *http.Request, name string) {
	resource, err := decodeWorkflowSchemaResource(r)
	if err != nil {
		writeJSON(w, invalidResourceValidation("workflow_schema", name, "body", err.Error()))
		return
	}
	normalized, err := s.runtime.ValidateWorkflowSchemaResource(name, resource)
	if err != nil {
		writeJSON(w, invalidResourceValidation("workflow_schema", firstResourceValidationName(name, resource.Name, resource.Schema.Workflow), workflowSchemaValidationField(err.Error()), err.Error()))
		return
	}
	writeJSON(w, validResourceValidation("workflow_schema", normalized.Name, normalized))
}

func (s *Server) handleWorkflowSchemaResourceActivate(w http.ResponseWriter, r *http.Request, name string) {
	merge, err := decodeWorkflowSchemaResourceMerge(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	schema, err := s.runtime.ActivateWorkflowSchemaResource(name, merge)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, schema)
}

func (s *Server) handleWorkflowSchemaResourceCapture(w http.ResponseWriter, r *http.Request, name string) {
	var req workflowSchemaResourceActionRequest
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
	resource, err := s.runtime.CaptureWorkflowSchemaResource(name, req.Description)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSONStatus(w, http.StatusCreated, resource)
}

func decodeWorkflowSchemaResource(r *http.Request) (agent.WorkflowSchemaResource, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return agent.WorkflowSchemaResource{}, err
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return agent.WorkflowSchemaResource{}, fmt.Errorf("workflow schema resource body is required")
	}
	var resource agent.WorkflowSchemaResource
	if err := json.Unmarshal(data, &resource); err == nil && (resource.Schema.Workflow != "" || resource.Name != "" || resource.Version != 0) {
		return resource, nil
	}
	var schema session.WorkflowSchemaSnapshot
	if err := json.Unmarshal(data, &schema); err != nil {
		return agent.WorkflowSchemaResource{}, fmt.Errorf("invalid workflow schema resource json")
	}
	if strings.TrimSpace(schema.Workflow) == "" {
		return agent.WorkflowSchemaResource{}, fmt.Errorf("workflow schema resource requires schema.workflow")
	}
	return agent.WorkflowSchemaResource{Version: 1, Name: schema.Workflow, Schema: schema}, nil
}

func decodeWorkflowSchemaResourceMerge(r *http.Request) (bool, error) {
	merge := true
	if queryMerge := strings.TrimSpace(r.URL.Query().Get("merge")); strings.EqualFold(queryMerge, "false") || strings.EqualFold(queryMerge, "replace") {
		merge = false
	}
	var req workflowSchemaResourceActionRequest
	if r.Body != nil {
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			return merge, err
		}
		if strings.TrimSpace(string(data)) != "" {
			if err := json.Unmarshal(data, &req); err != nil {
				return merge, fmt.Errorf("invalid json")
			}
			if req.Merge != nil {
				merge = *req.Merge
			}
		}
	}
	return merge, nil
}

func workflowSchemaValidationField(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "version"):
		return "version"
	case strings.Contains(lower, "kind"):
		return "kind"
	case strings.Contains(lower, "stage"):
		return "schema.stages"
	case strings.Contains(lower, "output"):
		return "schema.stages.outputs"
	case strings.Contains(lower, "workflow"):
		return "schema.workflow"
	case strings.Contains(lower, "name"):
		return "name"
	default:
		return ""
	}
}
