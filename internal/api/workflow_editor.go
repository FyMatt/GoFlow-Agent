package api

import "net/http"

func (s *Server) handleWorkflowEditor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.handleConsole(w, r)
}
