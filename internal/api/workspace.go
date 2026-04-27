package api

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"github.com/FyMatt/GoFlow-Agent/internal/workspace"
	"github.com/FyMatt/GoFlow-Agent/pkg/schema"
)

type workspaceActionRequest struct {
	Path string `json:"path"`
}

type workspaceResponse struct {
	workspace.Snapshot
	Message         string   `json:"message,omitempty"`
	RestartRequired bool     `json:"restart_required,omitempty"`
	SuggestedArgs   []string `json:"suggested_args,omitempty"`
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.workspaceResponse(""))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	action := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workspace/"), "/")
	switch action {
	case "confirm":
		if s.workspace != nil {
			s.workspace.Confirm()
		}
		if s.runtime != nil {
			s.runtime.SetWorkspaceConfirmed(true)
		}
		writeJSON(w, s.workspaceResponse("workspace confirmed"))
	case "clear":
		if s.workspace != nil {
			s.workspace.ClearConfirmation()
		}
		if s.runtime != nil {
			s.runtime.SetWorkspaceConfirmed(false)
		}
		writeJSON(w, s.workspaceResponse("workspace confirmation cleared"))
	case "select":
		var req workspaceActionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		path := workspace.NormalizePath(req.Path)
		if path == "" {
			http.Error(w, "workspace path is required", http.StatusBadRequest)
			return
		}
		if s.workspace != nil && workspace.SamePath(path, s.workspace.Root()) {
			s.workspace.Confirm()
			if s.runtime != nil {
				s.runtime.SetWorkspaceConfirmed(true)
			}
			writeJSON(w, s.workspaceResponse("workspace confirmed"))
			return
		}
		response := s.workspaceResponse("workspace switching requires restarting GoFlow so MCP servers are rebound under the new root")
		response.RestartRequired = true
		response.SuggestedArgs = []string{"--workspace", path}
		writeJSONStatus(w, http.StatusConflict, response)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) workspaceResponse(message string) workspaceResponse {
	snapshot := workspace.Snapshot{Status: "confirmed", Source: "none", Display: "(none)"}
	if s != nil && s.workspace != nil {
		snapshot = s.workspace.Snapshot()
	}
	return workspaceResponse{Snapshot: snapshot, Message: message}
}

func (s *Server) ensureWorkspaceConfirmedForRun(w http.ResponseWriter, input string) bool {
	if s == nil || s.workspace == nil || s.workspace.Confirmed() {
		return true
	}
	req := workspace.RequirementForInput(input)
	if !req.Required {
		return true
	}
	writeWorkspaceRequired(w, req.Reason, s.workspace.Snapshot())
	return false
}

func (s *Server) ensureWorkspaceConfirmedForJSON(w http.ResponseWriter, reason string) bool {
	if s == nil || s.workspace == nil || s.workspace.Confirmed() {
		return true
	}
	writeWorkspaceRequired(w, reason, s.workspace.Snapshot())
	return false
}

func (s *Server) ensureWorkspaceConfirmedForStream(w http.ResponseWriter, input string) bool {
	if s == nil || s.workspace == nil || s.workspace.Confirmed() {
		return true
	}
	req := workspace.RequirementForInput(input)
	if !req.Required {
		return true
	}
	writer, ok := newSSEWriter(w)
	if !ok {
		return false
	}
	_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: workspaceRequiredMessage(req.Reason, s.workspace.Snapshot()), IsError: true})
	return false
}

func (s *Server) ensureWorkspaceConfirmedForWorkflowStream(w http.ResponseWriter) bool {
	if s == nil || s.workspace == nil || s.workspace.Confirmed() {
		return true
	}
	writer, ok := newSSEWriter(w)
	if !ok {
		return false
	}
	_ = writer.write(schema.StreamEvent{Type: schema.StreamEventError, Content: workspaceRequiredMessage("workflow execution runs workspace-scoped stages", s.workspace.Snapshot()), IsError: true})
	return false
}

func writeWorkspaceRequired(w http.ResponseWriter, reason string, snapshot workspace.Snapshot) {
	writeJSONStatus(w, http.StatusConflict, map[string]any{
		"error":     "workspace_required",
		"message":   workspaceRequiredMessage(reason, snapshot),
		"reason":    strings.TrimSpace(reason),
		"workspace": snapshot,
		"action":    "confirm the current workspace or restart with --workspace <path>",
	})
}

func workspaceRequiredMessage(reason string, snapshot workspace.Snapshot) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "workspace-scoped action requested"
	}
	root := strings.TrimSpace(snapshot.Root)
	if root == "" {
		root = "(none)"
	}
	return "workspace confirmation required: " + reason + "; current workspace: " + root
}

func (s *Server) handleWorkspacePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = workspacePageTemplate.Execute(w, s.workspaceResponse(""))
}

var workspacePageTemplate = template.Must(template.New("workspace").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>GoFlow Workspace</title>
  <style>
    body { margin: 0; font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif; background: #f5f7fb; color: #18202f; }
    main { max-width: 860px; margin: 0 auto; padding: 32px 20px; }
    h1 { font-size: 24px; margin: 0 0 18px; }
    .panel { background: white; border: 1px solid #d8deea; border-radius: 8px; padding: 20px; box-shadow: 0 8px 24px rgba(27,39,67,.08); }
    dl { display: grid; grid-template-columns: 150px 1fr; gap: 12px 18px; margin: 0 0 20px; }
    dt { color: #647086; font-weight: 600; }
    dd { margin: 0; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; overflow-wrap: anywhere; }
    button { border: 1px solid #2f6fed; background: #2f6fed; color: white; border-radius: 6px; padding: 9px 14px; font-weight: 700; cursor: pointer; }
    button.secondary { background: white; color: #2f405f; border-color: #b7c0d1; }
    input { border: 1px solid #b7c0d1; border-radius: 6px; padding: 9px 10px; min-width: 320px; }
    .actions { display: flex; flex-wrap: wrap; gap: 10px; }
    .note { color: #647086; margin-top: 16px; line-height: 1.45; }
    #message { margin-top: 14px; font-weight: 700; color: #2f6fed; }
  </style>
</head>
<body>
<main>
  <h1>GoFlow Workspace</h1>
  <section class="panel">
    <dl>
      <dt>Root</dt><dd id="root">{{.Root}}</dd>
      <dt>Status</dt><dd id="status">{{.Status}}</dd>
      <dt>Source</dt><dd id="source">{{.Source}}</dd>
    </dl>
    <div class="actions">
      <button id="confirm">Confirm Workspace</button>
      <button class="secondary" id="clear">Clear Confirmation</button>
      <input id="path" placeholder="Path for restart suggestion">
      <button class="secondary" id="select">Use Path</button>
    </div>
    <p class="note">Changing to a different workspace requires restarting GoFlow so MCP servers are rebound under the new root.</p>
    <div id="message"></div>
  </section>
</main>
<script>
async function api(path, body) {
  const options = body ? { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : { method: "POST" };
  const res = await fetch(path, options);
  const data = await res.json();
  render(data);
  if (!res.ok && data.message) throw new Error(data.message);
  return data;
}
function render(data) {
  if (!data) return;
  document.getElementById("root").textContent = data.root || "(none)";
  document.getElementById("status").textContent = data.status || "";
  document.getElementById("source").textContent = data.source || "";
  document.getElementById("message").textContent = data.message || (data.restart_required ? "Restart required: goflow --workspace " + (data.suggested_args || []).slice(-1)[0] : "");
}
document.getElementById("confirm").onclick = () => api("/api/workspace/confirm").catch(err => document.getElementById("message").textContent = err.message);
document.getElementById("clear").onclick = () => api("/api/workspace/clear").catch(err => document.getElementById("message").textContent = err.message);
document.getElementById("select").onclick = () => api("/api/workspace/select", { path: document.getElementById("path").value }).catch(err => document.getElementById("message").textContent = err.message);
</script>
</body>
</html>`))
