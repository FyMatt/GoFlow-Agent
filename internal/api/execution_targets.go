package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/FyMatt/GoFlow-Agent/internal/executiontarget"
	"github.com/FyMatt/GoFlow-Agent/internal/sshhostkey"
)

type executionTargetDocument struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	DisplayName     string   `json:"display_name"`
	Host            string   `json:"host,omitempty"`
	Port            int      `json:"port,omitempty"`
	User            string   `json:"user,omitempty"`
	CredentialRef   string   `json:"credential_ref,omitempty"`
	HostKeySHA256   string   `json:"host_key_sha256,omitempty"`
	AllowedCommands []string `json:"allowed_commands,omitempty"`
	Enabled         bool     `json:"enabled"`
	CreatedAt       string   `json:"created_at,omitempty"`
	UpdatedAt       string   `json:"updated_at,omitempty"`
}

type executionTargetHostKeyScanRequest struct {
	Host            string `json:"host"`
	Port            int    `json:"port,omitempty"`
	AuthorizedScope bool   `json:"authorized_scope"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
}

type executionTargetHostKeyScanResponse struct {
	Host        string           `json:"host"`
	Port        int              `json:"port"`
	Keys        []sshhostkey.Key `json:"keys"`
	Count       int              `json:"count"`
	Stderr      string           `json:"stderr,omitempty"`
	Truncated   bool             `json:"truncated,omitempty"`
	CollectedAt string           `json:"collected_at"`
}

func (s *Server) executionTargetStore() *executiontarget.Store {
	if s == nil || s.runtime == nil {
		return nil
	}
	return executiontarget.NewStore(s.runtime.DataRoot())
}

func (s *Server) handleExecutionTargetCollection(w http.ResponseWriter, r *http.Request) {
	store := s.executionTargetStore()
	if store == nil {
		http.Error(w, "runtime not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := store.List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, executionTargetDocuments(items))
	case http.MethodPost:
		var doc executionTargetDocument
		if err := decodeJSONBody(r, &doc); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(doc.ID) == "" {
			generated, err := availableExecutionTargetID(store, doc)
			if err != nil {
				writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			doc.ID = generated
		} else if _, exists, err := store.Get(doc.ID); err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		} else if exists {
			writeJSONStatus(w, http.StatusConflict, map[string]string{"error": "target id already exists; update the existing target instead"})
			return
		}
		saved, err := store.Put(executionTargetFromDocument(doc))
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusCreated, executionTargetDocumentFrom(saved))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func availableExecutionTargetID(store *executiontarget.Store, doc executionTargetDocument) (string, error) {
	port := doc.Port
	if port == 0 {
		port = 22
	}
	base := executionTargetIDSlug("ssh-" + doc.Host + "-" + strconv.Itoa(port) + "-" + doc.User)
	if base == "ssh" || base == "" {
		base = "ssh-target"
	}
	for suffix := 1; suffix <= 999; suffix++ {
		candidate := base
		if suffix > 1 {
			tail := "-" + strconv.Itoa(suffix)
			candidate = strings.TrimRight(base[:min(len(base), 64-len(tail))], "-") + tail
		}
		_, exists, err := store.Get(candidate)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not allocate a unique target id")
}

func executionTargetIDSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	lastHyphen := false
	for _, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if valid {
			if out.Len() >= 64 {
				break
			}
			out.WriteRune(char)
			lastHyphen = false
			continue
		}
		if out.Len() > 0 && out.Len() < 64 && !lastHyphen {
			out.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func (s *Server) handleExecutionTargetHostKeyScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input executionTargetHostKeyScanRequest
	if err := decodeJSONBody(r, &input); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if !input.AuthorizedScope {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "authorized_scope must be true before scanning a host"})
		return
	}
	host := strings.Trim(strings.TrimSpace(input.Host), "[]")
	port := input.Port
	if port == 0 {
		port = 22
	}
	if err := executiontarget.ValidateSSHAddress(host, port); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	timeoutSeconds := input.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = 12
	}
	if timeoutSeconds < 1 || timeoutSeconds > 30 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "timeout_seconds must be between 1 and 30"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	keys, output, err := s.scanSSHHostKeys(ctx, host, port)
	if err != nil {
		status := http.StatusBadGateway
		if ctx.Err() != nil {
			status = http.StatusGatewayTimeout
		}
		writeJSONStatus(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, executionTargetHostKeyScanResponse{
		Host: host, Port: port, Keys: keys, Count: len(keys), Stderr: strings.TrimSpace(output.Stderr),
		Truncated: output.Truncated, CollectedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) scanSSHHostKeys(ctx context.Context, host string, port int) ([]sshhostkey.Key, sshhostkey.CommandOutput, error) {
	if s != nil && s.sshHostKeyScanner != nil {
		return s.sshHostKeyScanner(ctx, host, port)
	}
	return sshhostkey.Scan(ctx, strings.TrimSpace(os.Getenv("GOFLOW_SSH_KEYSCAN_CMD")), host, port, nil)
}

func (s *Server) handleExecutionTargetItem(w http.ResponseWriter, r *http.Request) {
	rawID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/targets/"), "/")
	id, err := url.PathUnescape(rawID)
	if err != nil || strings.TrimSpace(id) == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	store := s.executionTargetStore()
	if store == nil {
		http.Error(w, "runtime not configured", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		item, ok, err := store.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, executionTargetDocumentFrom(item))
	case http.MethodPut:
		var doc executionTargetDocument
		if err := decodeJSONBody(r, &doc); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(doc.ID) != "" && !strings.EqualFold(strings.TrimSpace(doc.ID), strings.TrimSpace(id)) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "target id does not match request path"})
			return
		}
		doc.ID = id
		saved, err := store.Put(executionTargetFromDocument(doc))
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, executionTargetDocumentFrom(saved))
	case http.MethodDelete:
		deleted, err := store.Delete(id)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !deleted {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{"deleted": true, "id": strings.ToLower(strings.TrimSpace(id))})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func executionTargetFromDocument(doc executionTargetDocument) executiontarget.Target {
	return executiontarget.Target{
		ID:              doc.ID,
		Kind:            doc.Kind,
		DisplayName:     doc.DisplayName,
		Host:            doc.Host,
		Port:            doc.Port,
		User:            doc.User,
		CredentialRef:   doc.CredentialRef,
		HostKeySHA256:   doc.HostKeySHA256,
		AllowedCommands: append([]string(nil), doc.AllowedCommands...),
		Enabled:         doc.Enabled,
	}
}

func executionTargetDocumentFrom(item executiontarget.Target) executionTargetDocument {
	return executionTargetDocument{
		ID:              item.ID,
		Kind:            item.Kind,
		DisplayName:     item.DisplayName,
		Host:            item.Host,
		Port:            item.Port,
		User:            item.User,
		CredentialRef:   item.CredentialRef,
		HostKeySHA256:   item.HostKeySHA256,
		AllowedCommands: append([]string(nil), item.AllowedCommands...),
		Enabled:         item.Enabled,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

func executionTargetDocuments(items []executiontarget.Target) []executionTargetDocument {
	out := make([]executionTargetDocument, 0, len(items))
	for _, item := range items {
		out = append(out, executionTargetDocumentFrom(item))
	}
	return out
}

func executionTargetSummaryFrom(item executiontarget.Target) executionTargetSummary {
	return executionTargetSummary{ID: item.ID, Kind: item.Kind, DisplayName: item.DisplayName, Enabled: item.Enabled}
}

func (s *Server) executionTargetSummaries() []executionTargetSummary {
	store := s.executionTargetStore()
	if store == nil {
		return []executionTargetSummary{localExecutionTarget()}
	}
	items, err := store.List()
	if err != nil {
		return []executionTargetSummary{localExecutionTarget()}
	}
	out := make([]executionTargetSummary, 0, len(items))
	for _, item := range items {
		out = append(out, executionTargetSummaryFrom(item))
	}
	return out
}
