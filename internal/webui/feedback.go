package webui

import (
	"net/http"

	"github.com/tonis2/foundry/internal/db"
)

func (s *Handler) handleUIFeedbackPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/feedback" {
		http.NotFound(w, r)
		return
	}
	s.renderShell(w, "feedback", "/feedback/fragment")
}

func (s *Handler) handleUIFeedbackFragment(w http.ResponseWriter, r *http.Request) {
	feedback, err := db.ListFeedback(r.Context(), s.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "feedback.list", struct {
		Feedback []db.Feedback
	}{feedback}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
