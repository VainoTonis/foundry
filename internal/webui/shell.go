package webui

import "net/http"

func (s *Handler) handleUIShell(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.renderShell(w, "backlog", "/backlog/fragment")
}
