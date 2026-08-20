package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

// ---- repositories ----

// HandleRepositories serves the /api/repositories collection endpoint:
// creating a new canonical Repository (POST) or listing all of them (GET).
func (h *Handler) HandleRepositories(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Name      string  `json:"name"`
			LocalPath *string `json:"local_path"`
			RemoteURL *string `json:"remote_url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		repo, err := db.CreateRepository(r.Context(), h.pool, repository.Repository{
			Name:      body.Name,
			LocalPath: body.LocalPath,
			RemoteURL: body.RemoteURL,
		})
		if errors.Is(err, repository.ErrNoLocator) || errors.Is(err, repository.ErrInvalidLocator) {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, repo, http.StatusCreated)
	case http.MethodGet:
		list, err := db.ListRepositories(r.Context(), h.pool)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// repositoryUpdateBody decodes a PATCH request body for a single
// Repository, distinguishing an omitted locator field from one explicitly
// set to null so callers can clear a locator (subject to the
// at-least-one-locator invariant enforced by db.UpdateRepository).
type repositoryUpdateBody struct {
	Name      *string
	LocalPath db.LocatorField
	RemoteURL db.LocatorField
}

// decodeRepositoryUpdateBody decodes a PATCH request body into a
// repositoryUpdateBody, distinguishing a key that is entirely absent from
// the JSON object (field left unchanged) from a key that is explicitly
// present with a JSON null value (field cleared). This distinction cannot
// be made by decoding straight into typed struct fields, since
// encoding/json collapses "absent" and "null" to the same zero value for
// pointer fields; decoding into a map of raw messages first preserves key
// presence so it can be inspected explicitly.
func decodeRepositoryUpdateBody(data []byte) (repositoryUpdateBody, error) {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return repositoryUpdateBody{}, err
	}

	var body repositoryUpdateBody

	if nameRaw, ok := raw["name"]; ok {
		var name *string
		if err := json.Unmarshal(nameRaw, &name); err != nil {
			return repositoryUpdateBody{}, err
		}
		body.Name = name
	}

	if localPathRaw, ok := raw["local_path"]; ok {
		var v *string
		if err := json.Unmarshal(localPathRaw, &v); err != nil {
			return repositoryUpdateBody{}, err
		}
		body.LocalPath = db.SetLocator(v)
	}

	if remoteURLRaw, ok := raw["remote_url"]; ok {
		var v *string
		if err := json.Unmarshal(remoteURLRaw, &v); err != nil {
			return repositoryUpdateBody{}, err
		}
		body.RemoteURL = db.SetLocator(v)
	}

	return body, nil
}

// HandleRepository serves the /api/repositories/{id} item endpoint:
// fetching, updating, or deleting a single canonical Repository.
func (h *Handler) HandleRepository(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r.URL.Path, "/api/repositories/")
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		repo, err := db.GetRepository(r.Context(), h.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, repo, http.StatusOK)
	case http.MethodPatch:
		data, err := io.ReadAll(r.Body)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, err := decodeRepositoryUpdateBody(data)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		repo, err := db.UpdateRepository(r.Context(), h.pool, id, db.UpdateRepositoryParams{
			Name:      body.Name,
			LocalPath: body.LocalPath,
			RemoteURL: body.RemoteURL,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, repository.ErrNoLocator) || errors.Is(err, repository.ErrInvalidLocator) {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, repo, http.StatusOK)
	case http.MethodDelete:
		if err := db.DeleteRepository(r.Context(), h.pool, id); errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		} else if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
