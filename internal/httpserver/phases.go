package httpserver

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
	"github.com/tonis2/foundry/internal/stream"
)

// ---- phases ----

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

	if suffix == "logs/stream" {
		s.streamLogs(w, r, id)
		return
	}
	s.jsonAPI.HandlePhase(w, r)
}

func (s *Server) streamLogs(w http.ResponseWriter, r *http.Request, phaseID int64) {
	flusher, ok := stream.StartSSE(w)
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
			stream.WriteIDData(w, l.ID, data)
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
		stream.WriteDone(w)
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
			stream.WriteHeartbeat(w)
			flusher.Flush()
			if isTerminal() {
				stream.WriteDone(w)
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
	stream.WriteEvent(w, "snapshot", data)
	return true
}

func (s *Server) streamWorkflow(w http.ResponseWriter, r *http.Request, workflowID int64) {
	flusher, ok := stream.StartSSE(w)
	if !ok {
		jsonErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

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
			stream.WriteHeartbeat(w)
			flusher.Flush()
		case data, ok := <-ch:
			if !ok {
				return
			}
			var evt struct {
				Event string `json:"event"`
			}
			if json.Unmarshal(data, &evt) == nil && evt.Event != "" {
				stream.WriteEvent(w, evt.Event, data)
			} else {
				stream.WriteEvent(w, "message", data)
			}
			flusher.Flush()
		}
	}
}
