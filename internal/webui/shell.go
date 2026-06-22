package webui

import "net/http"

// shellData holds the context for rendering the shell template
type shellData struct {
	Page     string
	Fragment string
}

func (s *Handler) handleUIShell(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.renderShell(w, "backlog", "/backlog/fragment")
}

// renderShell renders the shell template with the given page and fragment
func (s *Handler) renderShell(w http.ResponseWriter, page, fragment string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "shell", shellData{Page: page, Fragment: fragment}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
