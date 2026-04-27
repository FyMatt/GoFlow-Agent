package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/agent"
)

func (s *Server) handleWorkflowGraphCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.runtime.WorkflowRunner().ListWorkflowGraphs())
	case http.MethodPost:
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
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workflow-graphs/"), "/")
	if name == "" || strings.Contains(name, "/") {
		http.NotFound(w, r)
		return
	}
	runner := s.runtime.WorkflowRunner()
	switch r.Method {
	case http.MethodGet:
		if doc, ok := runner.BuiltInWorkflowGraphDocument(name); ok {
			writeJSON(w, doc)
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
	case http.MethodDelete:
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

func (s *Server) handleWorkflowOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, s.runtime.WorkflowRunner().WorkflowOptions())
}

func writeJSON(w http.ResponseWriter, value any) {
	writeJSONStatus(w, http.StatusOK, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
