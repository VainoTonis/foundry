package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/stream"
)

func (s *Server) handleChatSessionRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/chat/sessions/")
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
		s.streamChatEvents(w, r, id)
		return
	}
	s.jsonAPI.HandleChatSession(w, r)
}

func (s *Server) streamChatEvents(w http.ResponseWriter, r *http.Request, sessionID int64) {
	sess, err := db.GetChatSession(r.Context(), s.pool, sessionID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
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

	catchUp, _ := db.ListCerberusEvents(r.Context(), s.pool, sess.CerberusSession, lastID)
	for _, e := range catchUp {
		writeSSEvent(w, e)
	}
	flusher.Flush()

	ch := s.eventHub.Subscribe(sess.CerberusSession)
	defer s.eventHub.Unsubscribe(sess.CerberusSession, ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			var e db.CerberusEvent
			if err := json.Unmarshal(data, &e); err == nil {
				writeSSEvent(w, e)
			} else {
				stream.WriteEvent(w, "message", data)
			}
			flusher.Flush()
		}
	}
}
