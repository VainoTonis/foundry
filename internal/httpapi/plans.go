package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

func (h *Handler) HandlePlans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			ProjectID int64  `json:"project_id"`
			Title     string `json:"title"`
			Summary   string `json:"summary"`
			Content   string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.ProjectID == 0 {
			jsonErr(w, "project_id is required", http.StatusBadRequest)
			return
		}
		p, err := db.CreatePlan(r.Context(), h.pool, body.ProjectID, body.Title, body.Summary, body.Content)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "project not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusCreated)
	case http.MethodGet:
		list, err := db.ListPlans(r.Context(), h.pool)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) runPlan(w http.ResponseWriter, r *http.Request, id int64) {
	var body struct {
		MaxCostUSD *float64 `json:"max_cost_usd"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	plan, err := db.GetPlan(r.Context(), h.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "plan not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if plan.ProjectID == nil {
		jsonErr(w, "plan has no project; update project_id before running", http.StatusConflict)
		return
	}
	content := strings.TrimSpace(plan.Content)
	if content == "" {
		content = "# " + plan.Title
		if plan.Summary != "" {
			content += "\n\n" + plan.Summary
		}
		steps, listErr := db.ListPlanSteps(r.Context(), h.pool, id)
		if listErr != nil {
			jsonErr(w, listErr.Error(), http.StatusInternalServerError)
			return
		}
		for i, step := range steps {
			content += "\n\n## Phase " + strconv.Itoa(i+1) + ": Step " + strconv.Itoa(i+1) + "\n\n" + step.Text
		}
	}
	sp, err := db.CreateSpec(r.Context(), h.pool, *plan.ProjectID, plan.Title, content, []byte("[]"))
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	maxCost := body.MaxCostUSD
	if maxCost == nil {
		v := h.defaultBudget
		maxCost = &v
	}
	wf, err := db.CreateWorkflow(r.Context(), h.pool, sp.ID, sp.Track, maxCost)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := db.LinkPlanWorkflow(r.Context(), h.pool, id, wf.ID); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	running := "running"
	_, _ = db.UpdatePlan(r.Context(), h.pool, id, db.UpdatePlanParams{Status: &running})
	_, _ = db.UpdateSpec(r.Context(), h.pool, sp.ID, db.UpdateSpecParams{Status: &running})
	if h.workflowRunner != nil {
		h.workflowRunner.Start(wf.ID)
	}
	jsonOK(w, wf, http.StatusCreated)
}

func (h *Handler) HandlePlan(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/plans/")
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
		p, err := db.GetPlan(r.Context(), h.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case suffix == "" && r.Method == http.MethodPatch:
		var body struct {
			Status    *string `json:"status"`
			ProjectID *int64  `json:"project_id"`
			Title     *string `json:"title"`
			Summary   *string `json:"summary"`
			Content   *string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := db.UpdatePlan(r.Context(), h.pool, id, db.UpdatePlanParams{Status: body.Status, ProjectID: body.ProjectID, Title: body.Title, Summary: body.Summary, Content: body.Content})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case suffix == "run" && r.Method == http.MethodPost:
		h.runPlan(w, r, id)
	case suffix == "steps" && r.Method == http.MethodGet:
		steps, err := db.ListPlanSteps(r.Context(), h.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, steps, http.StatusOK)
	case suffix == "steps" && r.Method == http.MethodPost:
		var body struct {
			Position      int    `json:"position"`
			Text          string `json:"text"`
			ParallelGroup *int   `json:"parallel_group"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		step, err := db.CreatePlanStep(r.Context(), h.pool, id, body.Position, body.Text, body.ParallelGroup)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, step, http.StatusCreated)
	case strings.HasPrefix(suffix, "steps/"):
		stepParts := strings.SplitN(suffix, "/", 2)
		stepID, err := strconv.ParseInt(stepParts[1], 10, 64)
		if err != nil {
			jsonErr(w, "invalid step id", http.StatusBadRequest)
			return
		}

		// Validate step belongs to plan
		step, err := db.GetPlanStepByID(r.Context(), h.pool, id, stepID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}

		switch r.Method {
		case http.MethodGet:
			jsonOK(w, step, http.StatusOK)
		case http.MethodPatch:
			var body struct {
				Status        *string `json:"status"`
				Text          *string `json:"text"`
				ParallelGroup *int    `json:"parallel_group"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonErr(w, err.Error(), http.StatusBadRequest)
				return
			}
			updated, err := db.UpdatePlanStep(r.Context(), h.pool, id, stepID, db.UpdatePlanStepParams{Status: body.Status, Text: body.Text, ParallelGroup: body.ParallelGroup})
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
			jsonOK(w, updated, http.StatusOK)
		default:
			jsonErr(w, "not found", http.StatusNotFound)
		}
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}
