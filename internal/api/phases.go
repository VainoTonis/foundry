package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

// ---- phases ----

func resumeFailedPhaseUpdate() db.UpdatePhaseParams {
	pending := "pending"
	zero := 0
	return db.UpdatePhaseParams{Status: &pending, RetryCount: &zero}
}

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

func (s *Server) handlePhase(w http.ResponseWriter, r *http.Request) {
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
		ph, err := db.GetPhase(r.Context(), s.pool, id)
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
		logs, err := db.ListPhaseLogs(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, logs, http.StatusOK)
	case suffix == "logs/stream":
		s.streamLogs(w, r, id)
	case suffix == "diff" && r.Method == http.MethodGet:
		ph, err := db.GetPhase(r.Context(), s.pool, id)
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
		diff, err := s.cerb.Diff(r.Context(), *ph.CerberusSession)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, diff)
	case suffix == "approve" && r.Method == http.MethodPost:
		_, err := db.UpdatePhase(r.Context(), s.pool, id, approvePhaseUpdate(time.Now()))
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ph, _ := db.GetPhase(r.Context(), s.pool, id)
		jsonOK(w, ph, http.StatusOK)
	case suffix == "reject" && r.Method == http.MethodPost:
		_, err := db.UpdatePhase(r.Context(), s.pool, id, rejectPhaseUpdate(time.Now()))
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ph, _ := db.GetPhase(r.Context(), s.pool, id)
		jsonOK(w, ph, http.StatusOK)
	case suffix == "clean" && r.Method == http.MethodPost:
		ph, err := db.GetPhase(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if ph.CerberusSession != nil {
			if _, _, proj, err := s.workflowProject(r.Context(), ph.WorkflowID); err == nil {
				s.cerb.SetRepoPath(proj.RepoPath)
			}
			if err := s.cerb.Clean(r.Context(), *ph.CerberusSession); err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
			db.DeleteCerberusEvents(r.Context(), s.pool, *ph.CerberusSession)
			removeProfileFile(*ph.CerberusSession)
		}
		jsonOK(w, map[string]string{"status": "cleaned"}, http.StatusOK)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request, phaseID int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	ph, err := db.GetPhase(r.Context(), s.pool, phaseID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var lastID int64
	if raw := r.URL.Query().Get("after_id"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			lastID = parsed
		}
	}

	sendCatchup := func() bool {
		logs, err := db.StreamPhaseLogs(r.Context(), s.pool, phaseID, lastID)
		if err != nil {
			return false
		}
		for _, l := range logs {
			data, _ := json.Marshal(l)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", l.ID, data)
			lastID = l.ID
		}
		flusher.Flush()
		return true
	}
	isTerminal := func() bool {
		ph, err := db.GetPhase(r.Context(), s.pool, phaseID)
		if err != nil {
			return true
		}
		return ph.Status == "done" || ph.Status == "failed"
	}

	if !sendCatchup() {
		return
	}
	if isTerminal() {
		fmt.Fprintf(w, "event: done\ndata: {}\n\n")
		flusher.Flush()
		return
	}

	key := fmt.Sprintf("wf:%d", ph.WorkflowID)
	ch := s.eventHub.Subscribe(key)
	defer s.eventHub.Unsubscribe(key, ch)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if !sendCatchup() {
				return
			}
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
			if isTerminal() {
				fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		case data, ok := <-ch:
			if !ok {
				return
			}
			var evt struct {
				Event   string `json:"event"`
				PhaseID int64  `json:"phase_id"`
			}
			if json.Unmarshal(data, &evt) != nil {
				continue
			}
			if evt.Event == "log" && evt.PhaseID == phaseID {
				if !sendCatchup() {
					return
				}
			} else if evt.Event == "phase_update" && evt.PhaseID == phaseID && (isTerminal()) {
				if !sendCatchup() {
					return
				}
				fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

func (s *Server) writeWorkflowSnapshot(ctx context.Context, w io.Writer, workflowID int64) bool {
	wf, err := db.GetWorkflow(ctx, s.pool, workflowID)
	if err != nil {
		log.Printf("workflow snapshot: get workflow %d: %v", workflowID, err)
		return false
	}
	phases, err := db.ListPhasesByWorkflow(ctx, s.pool, workflowID)
	if err != nil {
		log.Printf("workflow snapshot: list phases for workflow %d: %v", workflowID, err)
		return false
	}
	data, _ := json.Marshal(map[string]any{
		"event":    "snapshot",
		"workflow": wf,
		"phases":   phases,
	})
	fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data)
	return true
}

func (s *Server) streamWorkflow(w http.ResponseWriter, r *http.Request, workflowID int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	key := fmt.Sprintf("wf:%d", workflowID)
	ch := s.eventHub.Subscribe(key)
	defer s.eventHub.Unsubscribe(key, ch)

	// Send a database-backed snapshot first. If the browser reconnects after
	// dropped high-volume live events, this catches it up to durable state.
	if !s.writeWorkflowSnapshot(r.Context(), w, workflowID) {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		case data, ok := <-ch:
			if !ok {
				return
			}
			var evt struct {
				Event string `json:"event"`
			}
			if json.Unmarshal(data, &evt) == nil && evt.Event != "" {
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Event, data)
			} else {
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
			flusher.Flush()
		}
	}
}
