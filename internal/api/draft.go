package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/authoring"
	"github.com/tonis2/foundry/internal/db"
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

func (s *Server) handleSpecDrafts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		svc := s.newSpecDraftsService()
		list, err := svc.ListDrafts(r.Context())
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)

	case http.MethodPost:
		var body struct {
			ProjectID   *int64 `json:"project_id"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		svc := s.newSpecDraftsService()
		draft, err := svc.CreateDraftAndStartChat(r.Context(), authoring.CreateDraftAndStartChatParams{
			ProjectID:         body.ProjectID,
			Description:       body.Description,
			SpecBuilderPrompt: authoring.SpecBuilderPrompt,
		})
		if err != nil {
			statusCode := http.StatusInternalServerError
			errMsg := err.Error()
			if strings.Contains(errMsg, "required") {
				statusCode = http.StatusUnprocessableEntity
			} else if strings.Contains(errMsg, "not found") {
				statusCode = http.StatusNotFound
			} else if strings.Contains(errMsg, "not configured") {
				statusCode = http.StatusUnprocessableEntity
			}
			jsonErr(w, errMsg, statusCode)
			return
		}

		jsonOK(w, draft, http.StatusCreated)

	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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

	switch {
	case suffix == "stream":
		s.streamDraftEvents(w, r, id)
		return

	case suffix == "messages" && r.Method == http.MethodGet:
		svc := s.newSpecDraftsService()
		messages, err := svc.GetDraftMessages(r.Context(), id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(messages)

	case suffix == "" && r.Method == http.MethodGet:
		svc := s.newSpecDraftsService()
		draft, err := svc.GetDraft(r.Context(), id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, draft, http.StatusOK)

	case suffix == "message" && r.Method == http.MethodPost:
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		svc := s.newSpecDraftsService()
		draft, err := svc.AppendUserMessage(r.Context(), authoring.AppendUserMessageParams{
			DraftID: id,
			Content: body.Content,
		})
		if err != nil {
			statusCode := http.StatusInternalServerError
			errMsg := err.Error()
			if strings.Contains(errMsg, "not found") {
				statusCode = http.StatusNotFound
			} else if strings.Contains(errMsg, "not configured") {
				statusCode = http.StatusUnprocessableEntity
			}
			jsonErr(w, errMsg, statusCode)
			return
		}

		jsonOK(w, draft, http.StatusOK)

	case suffix == "save" && r.Method == http.MethodPost:
		var saveBody struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&saveBody); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		svc := s.newSpecDraftsService()
		specID, err := svc.SaveDraft(r.Context(), authoring.SaveDraftParams{
			DraftID: id,
			Title:   saveBody.Title,
		})
		if err != nil {
			statusCode := http.StatusInternalServerError
			errMsg := err.Error()
			if strings.Contains(errMsg, "could not extract spec") {
				statusCode = http.StatusUnprocessableEntity
			} else if strings.Contains(errMsg, "not found") {
				statusCode = http.StatusNotFound
			}
			jsonErr(w, errMsg, statusCode)
			return
		}

		jsonOK(w, map[string]int64{"spec_id": specID}, http.StatusCreated)

	case suffix == "" && r.Method == http.MethodDelete:
		svc := s.newSpecDraftsService()
		if err := svc.DeleteDraft(r.Context(), id); err != nil {
			statusCode := http.StatusInternalServerError
			errMsg := err.Error()
			if strings.Contains(errMsg, "not found") {
				statusCode = http.StatusNotFound
			}
			jsonErr(w, errMsg, statusCode)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
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

	flusher, ok := w.(http.Flusher)
	if !ok {
		jsonErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

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
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
			flusher.Flush()
		}
	}
}

func writeSSEvent(w http.ResponseWriter, e db.CerberusEvent) {
	fmt.Fprintf(w, "id: %d\n", e.ID)
	fmt.Fprintf(w, "event: %s\n", e.EventType)
	fmt.Fprintf(w, "data: %s\n\n", e.Payload)
}
