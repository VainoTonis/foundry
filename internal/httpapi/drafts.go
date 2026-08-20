package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/authoring"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

// draftErrorStatus classifies an error returned by the spec-draft
// authoring service into an HTTP status code. A repository.ErrNoLocalPath
// (a remote-only repository with no local worktree mounted) is a
// conflict: the request is well-formed and the repository exists, but
// the current state of that repository cannot satisfy it, distinct from
// a validation error (400/422) or a missing resource (404).
func draftErrorStatus(err error) int {
	if errors.Is(err, repository.ErrNoLocalPath) {
		return http.StatusConflict
	}
	errMsg := err.Error()
	switch {
	case strings.Contains(errMsg, "required"):
		return http.StatusUnprocessableEntity
	case strings.Contains(errMsg, "not found"):
		return http.StatusNotFound
	case strings.Contains(errMsg, "not configured"):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

func (h *Handler) HandleSpecDrafts(w http.ResponseWriter, r *http.Request) {
	svc := h.specDraftsService()
	switch r.Method {
	case http.MethodGet:
		list, err := svc.ListDrafts(r.Context())
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)

	case http.MethodPost:
		var body struct {
			RepositoryID *int64 `json:"repository_id"`
			Description  string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}

		draft, err := svc.CreateDraftAndStartChat(r.Context(), authoring.CreateDraftAndStartChatParams{
			RepositoryID:      body.RepositoryID,
			Description:       body.Description,
			SpecBuilderPrompt: authoring.SpecBuilderPrompt,
		})
		if err != nil {
			jsonErr(w, err.Error(), draftErrorStatus(err))
			return
		}

		jsonOK(w, draft, http.StatusCreated)

	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) HandleSpecDraft(w http.ResponseWriter, r *http.Request) {
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

	svc := h.specDraftsService()
	switch {
	case suffix == "messages" && r.Method == http.MethodGet:
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

		draft, err := svc.AppendUserMessage(r.Context(), authoring.AppendUserMessageParams{
			DraftID: id,
			Content: body.Content,
		})
		if err != nil {
			jsonErr(w, err.Error(), draftErrorStatus(err))
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

		planID, err := svc.SaveDraft(r.Context(), authoring.SaveDraftParams{
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

		jsonOK(w, map[string]int64{"plan_id": planID}, http.StatusCreated)

	case suffix == "" && r.Method == http.MethodDelete:
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
