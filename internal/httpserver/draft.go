package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/authoring"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/stream"
)

// ---- spec-draft handlers ----

// newAuthoringService creates an authoring Service with Server dependencies.
func (s *Server) newSpecDraftsService() *authoring.Service {
	return authoring.NewService(
		s.pool,
		s.cerb,
		s.callbackURL(),
		s.writeProfileFile,
		removeProfileFile,
	)
}

func (s *Server) handleSpecDraft(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/spec-drafts/")
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

	if suffix == "stream" {
		s.streamDraftEvents(w, r, id)
		return
	}
	s.jsonAPI.HandleSpecDraft(w, r)
}

// ---- cerberus draft event streaming ----

func (s *Server) assembleAndAppend(ctx context.Context, session string, isTurnComplete bool) {
	authoring.AssembleAndAppendMessages(ctx, s.pool, session, isTurnComplete)
}

func (s *Server) streamDraftEvents(w http.ResponseWriter, r *http.Request, draftID int64) {
	draft, err := db.GetSpecDraft(r.Context(), s.pool, draftID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if draft.CerberusSession == "" {
		jsonErr(w, "no session", http.StatusConflict)
		return
	}

	flusher, ok := stream.StartSSE(w)
	if !ok {
		jsonErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	lastIDStr := r.URL.Query().Get("after")
	if lastIDStr == "" {
		lastIDStr = r.Header.Get("Last-Event-ID")
	}
	var lastID int64
	if lastIDStr != "" {
		lastID, _ = strconv.ParseInt(lastIDStr, 10, 64)
	}

	catchUp, _ := db.ListCerberusEvents(r.Context(), s.pool, draft.CerberusSession, lastID)
	for _, e := range catchUp {
		writeSSEvent(w, e)
		lastID = e.ID
	}
	flusher.Flush()

	ch := s.eventHub.Subscribe(draft.CerberusSession)
	defer s.eventHub.Unsubscribe(draft.CerberusSession, ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			var e db.CerberusEvent
			if json.Unmarshal(data, &e) == nil {
				writeSSEvent(w, e)
			} else {
				stream.WriteEvent(w, "message", data)
			}
			flusher.Flush()
		}
	}
}

func writeSSEvent(w http.ResponseWriter, e db.CerberusEvent) {
	stream.WriteIDEvent(w, e.ID, e.EventType, e.Payload)
}
