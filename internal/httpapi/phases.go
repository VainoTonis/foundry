package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

func approvePhaseUpdate(now time.Time) db.UpdatePhaseParams {
	done := "done"
	pass := "pass"
	return db.UpdatePhaseParams{Status: &done, ReviewVerdict: &pass, FinishedAt: &now}
}

func rejectPhaseUpdate(now time.Time) db.UpdatePhaseParams {
	failed := "failed"
	fail := "fail"
	return db.UpdatePhaseParams{Status: &failed, ReviewVerdict: &fail, FinishedAt: &now}
}

func (h *Handler) HandlePhase(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/phases/")
	parts := strings.SplitN(path, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}

	switch {
	case suffix == "" && r.Method == http.MethodGet:
		ph, err := db.GetPhase(r.Context(), h.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, ph, http.StatusOK)
	case suffix == "logs" && r.Method == http.MethodGet:
		logs, err := db.ListPhaseLogs(r.Context(), h.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, logs, http.StatusOK)
	case suffix == "diff" && r.Method == http.MethodGet:
		ph, err := db.GetPhase(r.Context(), h.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if ph.CerberusSession == nil {
			jsonErr(w, "no cerberus session", http.StatusConflict)
			return
		}
		diff, err := h.cerb.Diff(r.Context(), *ph.CerberusSession)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, diff)
	case suffix == "approve" && r.Method == http.MethodPost:
		_, err := db.UpdatePhase(r.Context(), h.pool, id, approvePhaseUpdate(time.Now()))
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ph, _ := db.GetPhase(r.Context(), h.pool, id)
		jsonOK(w, ph, http.StatusOK)
	case suffix == "reject" && r.Method == http.MethodPost:
		_, err := db.UpdatePhase(r.Context(), h.pool, id, rejectPhaseUpdate(time.Now()))
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ph, _ := db.GetPhase(r.Context(), h.pool, id)
		jsonOK(w, ph, http.StatusOK)
	case suffix == "clean" && r.Method == http.MethodPost:
		ph, err := db.GetPhase(r.Context(), h.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if ph.CerberusSession != nil {
			var cerberusCmd interface {
				Clean(context.Context, string) error
			} = h.cerb
			if h.projectRepoForWorkflow != nil {
				if repo, err := h.projectRepoForWorkflow(r.Context(), ph.WorkflowID); err == nil && strings.TrimSpace(repo) != "" {
					cerberusCmd = h.cerb.WithRepo(repo)
				}
			}
			if err := cerberusCmd.Clean(r.Context(), *ph.CerberusSession); err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
			db.DeleteCerberusEvents(r.Context(), h.pool, *ph.CerberusSession)
			if h.removeProfileFile != nil {
				h.removeProfileFile(*ph.CerberusSession)
			}
		}
		jsonOK(w, map[string]string{"status": "cleaned"}, http.StatusOK)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}
