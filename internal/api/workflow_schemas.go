package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/session"
)

const (
	workflowSchemaBundleKind                = "goflow.workflow_schemas"
	workflowSchemaBundleVersion             = 2
	workflowSchemaBundleMinSupportedVersion = 1
)

type workflowSchemaExportResponse struct {
	Kind       string                           `json:"kind,omitempty"`
	Version    int                              `json:"version"`
	MinVersion int                              `json:"min_supported_version,omitempty"`
	Schemas    []session.WorkflowSchemaSnapshot `json:"schemas"`
}

type workflowSchemaImportRequest struct {
	Kind       string                           `json:"kind,omitempty"`
	Version    int                              `json:"version,omitempty"`
	MinVersion int                              `json:"min_supported_version,omitempty"`
	Merge      *bool                            `json:"merge,omitempty"`
	Schema     *session.WorkflowSchemaSnapshot  `json:"schema,omitempty"`
	Schemas    []session.WorkflowSchemaSnapshot `json:"schemas,omitempty"`
}

type workflowSchemaImportResponse struct {
	Imported int                              `json:"imported"`
	Schemas  []session.WorkflowSchemaSnapshot `json:"schemas"`
}

func (s *Server) handleWorkflowSchemaCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.runtime.WorkflowSchemas())
	case http.MethodPost:
		writeJSON(w, s.runtime.RebuildWorkflowSchemas())
	case http.MethodDelete:
		writeJSON(w, map[string]int{"cleared": s.runtime.ClearWorkflowSchemas()})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowSchemaItem(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workflow-schemas/"), "/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 1 && parts[0] == "rebuild" && r.Method == http.MethodPost {
		writeJSON(w, s.runtime.RebuildWorkflowSchemas())
		return
	}
	if len(parts) == 1 && parts[0] == "export" && r.Method == http.MethodGet {
		writeJSON(w, newWorkflowSchemaExportResponse(s.runtime.WorkflowSchemas()))
		return
	}
	if len(parts) == 1 && parts[0] == "import" && r.Method == http.MethodPost {
		s.handleWorkflowSchemaImport(w, r)
		return
	}
	name := parts[0]
	if name == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "export" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		schema, ok := s.runtime.WorkflowSchema(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, newWorkflowSchemaExportResponse([]session.WorkflowSchemaSnapshot{schema}))
		return
	}
	if len(parts) > 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		schema, ok := s.runtime.WorkflowSchema(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, schema)
	case http.MethodDelete:
		if !s.runtime.ClearWorkflowSchema(name) {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkflowSchemaImport(w http.ResponseWriter, r *http.Request) {
	schemas, merge, err := decodeWorkflowSchemaImportRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	imported, err := s.runtime.ImportWorkflowSchemas(schemas, merge)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSONStatus(w, http.StatusCreated, workflowSchemaImportResponse{Imported: len(imported), Schemas: imported})
}

func decodeWorkflowSchemaImportRequest(r *http.Request) ([]session.WorkflowSchemaSnapshot, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return nil, true, err
	}
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return nil, true, fmt.Errorf("workflow schema import body is required")
	}
	merge := true
	if queryMerge := strings.TrimSpace(r.URL.Query().Get("merge")); strings.EqualFold(queryMerge, "false") || strings.EqualFold(queryMerge, "replace") {
		merge = false
	}
	var request workflowSchemaImportRequest
	if err := json.Unmarshal(data, &request); err == nil && (request.Schema != nil || len(request.Schemas) > 0 || request.Merge != nil) {
		if err := validateWorkflowSchemaImportBundle(request.Kind, request.Version, request.MinVersion); err != nil {
			return nil, merge, err
		}
		if request.Merge != nil {
			merge = *request.Merge
		}
		schemas := append([]session.WorkflowSchemaSnapshot(nil), request.Schemas...)
		if request.Schema != nil {
			schemas = append(schemas, *request.Schema)
		}
		if len(schemas) == 0 {
			return nil, merge, fmt.Errorf("workflow schema import requires schema or schemas")
		}
		return schemas, merge, nil
	}
	var schemas []session.WorkflowSchemaSnapshot
	if err := json.Unmarshal(data, &schemas); err == nil && len(schemas) > 0 {
		return schemas, merge, nil
	}
	var schema session.WorkflowSchemaSnapshot
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, merge, fmt.Errorf("invalid workflow schema import json")
	}
	if strings.TrimSpace(schema.Workflow) == "" {
		return nil, merge, fmt.Errorf("workflow schema import requires workflow")
	}
	return []session.WorkflowSchemaSnapshot{schema}, merge, nil
}

func newWorkflowSchemaExportResponse(schemas []session.WorkflowSchemaSnapshot) workflowSchemaExportResponse {
	return workflowSchemaExportResponse{
		Kind:       workflowSchemaBundleKind,
		Version:    workflowSchemaBundleVersion,
		MinVersion: workflowSchemaBundleMinSupportedVersion,
		Schemas:    schemas,
	}
}

func validateWorkflowSchemaImportBundle(kind string, version, minVersion int) error {
	if strings.TrimSpace(kind) != "" && strings.TrimSpace(kind) != workflowSchemaBundleKind {
		return fmt.Errorf("unsupported workflow schema bundle kind %q", kind)
	}
	if version == 0 {
		version = 1
	}
	if version < workflowSchemaBundleMinSupportedVersion {
		return fmt.Errorf("workflow schema bundle version %d is no longer supported", version)
	}
	if version > workflowSchemaBundleVersion {
		return fmt.Errorf("workflow schema bundle version %d is newer than supported version %d", version, workflowSchemaBundleVersion)
	}
	if minVersion > workflowSchemaBundleVersion {
		return fmt.Errorf("workflow schema bundle requires reader version %d, supported version is %d", minVersion, workflowSchemaBundleVersion)
	}
	return nil
}
