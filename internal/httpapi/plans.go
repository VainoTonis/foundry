package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/review"
)

// ReviewRunner is the narrow Steward review execution surface HandlePlan
// needs to create and start exactly one bounded review for a plan.
// *review.Service satisfies this interface. StartStewardReview returns
// the newly created, still-queued review immediately, without waiting
// for it to even start running or for its cerberus turn to resolve, so
// review creation stays prompt; the review's eventual running,
// completed, or failed outcome is later visible via
// GetPlanReview/ListPlanReviews.
type ReviewRunner interface {
	StartStewardReview(ctx context.Context, opts review.RunOptions) (db.PlanReview, error)
}

// planReviewView is a plan review as exposed over the API, with a
// computed Stale flag telling a caller whether the plan has changed
// since this review's exact input snapshot was fingerprinted.
type planReviewView struct {
	db.PlanReview
	Stale bool `json:"stale"`
}

// currentPlanSnapshotHash recomputes the exact snapshot fingerprint
// RunStewardReview would compute for plan right now, from its current
// steps and open feedback, so a stored review's input hash can be
// compared against it to detect staleness.
func (h *Handler) currentPlanSnapshotHash(ctx context.Context, plan db.Plan) (string, error) {
	steps, err := db.ListPlanSteps(ctx, h.pool, plan.ID)
	if err != nil {
		return "", err
	}
	feedback, err := db.ListFeedback(ctx, h.pool)
	if err != nil {
		return "", err
	}
	snap, err := review.BuildSnapshot(plan, steps, feedback)
	if err != nil {
		return "", err
	}
	return snap.SHA256, nil
}

// newPlanReviewView wraps rv with a Stale flag. If currentHash could
// not be computed (hashErr != nil), rv is conservatively reported as
// stale rather than silently claiming it is current.
func newPlanReviewView(rv db.PlanReview, currentHash string, hashErr error) planReviewView {
	stale := true
	if hashErr == nil {
		stale = rv.InputSnapshotSHA256 != currentHash
	}
	return planReviewView{PlanReview: rv, Stale: stale}
}

// planReviewWarning is an advisory notice surfaced alongside plan
// execution when the plan's most recent Steward review is missing,
// stale, unresolved (revise/escalate), failed, or could not fully
// ground its claims. It never blocks execution: runPlan always returns
// it informationally, and a pass verdict on a current, fully grounded
// review simply yields no warnings.
type planReviewWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Advisory warning codes. Each names exactly one review state a caller
// should be told about before running a plan; none of them cause
// execution to fail.
const (
	planReviewWarningMissing    = "review_missing"
	planReviewWarningIncomplete = "review_incomplete"
	planReviewWarningStale      = "review_stale"
	planReviewWarningUnresolved = "review_unresolved"
	planReviewWarningFailed     = "review_failed"
	planReviewWarningUngrounded = "review_grounding_unavailable"
)

// planReviewWarnings inspects reviews (most recent first, as returned
// by db.ListPlanReviews) against currentHash, the plan's exact current
// input snapshot fingerprint, and returns every advisory warning a
// caller should see before running the plan. A hashErr means staleness
// could not be computed, so it is conservatively treated as stale
// rather than silently reported current.
//
// Exactly one of these applies, based on the single most recent
// review's terminal state:
//   - no review exists at all: review_missing
//   - the most recent review has not reached a terminal state yet
//     (still queued or running): review_incomplete
//   - the most recent review failed to complete: review_failed
//   - the most recent review completed: independently, it may be
//     stale against the plan's current content (review_stale), may
//     have returned revise or escalate (review_unresolved), and/or may
//     have been unable to ground its claims against every repository
//     (review_grounding_unavailable). A pass verdict on a current,
//     fully grounded review yields none of these.
func planReviewWarnings(reviews []db.PlanReview, currentHash string, hashErr error) []planReviewWarning {
	if len(reviews) == 0 {
		return []planReviewWarning{{
			Code:    planReviewWarningMissing,
			Message: "no Steward review has ever been run for this plan",
		}}
	}

	latest := reviews[0]
	switch latest.Status {
	case db.PlanReviewStatusFailed:
		msg := "the most recent Steward review failed to complete"
		if latest.Error != nil && strings.TrimSpace(*latest.Error) != "" {
			msg += ": " + strings.TrimSpace(*latest.Error)
		}
		return []planReviewWarning{{Code: planReviewWarningFailed, Message: msg}}
	case db.PlanReviewStatusCompleted:
		var warnings []planReviewWarning
		if hashErr != nil || latest.InputSnapshotSHA256 != currentHash {
			warnings = append(warnings, planReviewWarning{
				Code:    planReviewWarningStale,
				Message: "the most recent Steward review no longer matches the plan's current content",
			})
		}
		if latest.Verdict != nil && (*latest.Verdict == db.PlanReviewVerdictRevise || *latest.Verdict == db.PlanReviewVerdictEscalate) {
			warnings = append(warnings, planReviewWarning{
				Code:    planReviewWarningUnresolved,
				Message: fmt.Sprintf("the most recent Steward review returned verdict %q and has not since been re-reviewed as passing", *latest.Verdict),
			})
		}
		if reportHasUnavailableGrounding(latest.Report) {
			warnings = append(warnings, planReviewWarning{
				Code:    planReviewWarningUngrounded,
				Message: "the most recent Steward review could not ground its claims against every attached repository",
			})
		}
		return warnings
	default: // queued or running: no terminal outcome to evaluate yet.
		return []planReviewWarning{{
			Code:    planReviewWarningIncomplete,
			Message: "the most recent Steward review has not finished running",
		}}
	}
}

// reportHasUnavailableGrounding reports whether report's
// review.ReportFieldUnavailable field is present and non-empty,
// meaning Steward itself disclosed at least one repository or claim it
// could not ground against inspected source. A missing, null, empty
// array, empty string, or unparsable report is treated as no
// disclosed gap rather than a false warning.
func reportHasUnavailableGrounding(report json.RawMessage) bool {
	if len(report) == 0 {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(report, &fields); err != nil {
		return false
	}
	raw, ok := fields[review.ReportFieldUnavailable]
	if !ok {
		return false
	}
	switch strings.TrimSpace(string(raw)) {
	case "", "null", "[]", `""`:
		return false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		return len(arr) > 0
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s) != ""
	}
	return true
}

// createPlanReview starts exactly one new Steward review for planID
// and returns 202 Accepted with it as soon as it is persisted queued,
// without waiting for it to start running or for Steward's bounded
// cerberus turn to resolve. It fails with 503 if no ReviewRunner is
// configured, 404 if the plan does not exist, and 502 if the review
// could not even be queued (a review that was queued but later fails
// its turn is instead persisted as a failed review; see foundry plans
// reviews/check to observe it).
func (h *Handler) createPlanReview(w http.ResponseWriter, r *http.Request, planID int64) {
	if h.reviewRunner == nil {
		jsonErr(w, "plan review is not configured", http.StatusServiceUnavailable)
		return
	}
	plan, err := db.GetPlan(r.Context(), h.pool, planID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "plan not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	steps, err := db.ListPlanSteps(r.Context(), h.pool, planID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	feedback, err := db.ListFeedback(r.Context(), h.pool)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result, err := h.reviewRunner.StartStewardReview(r.Context(), review.RunOptions{
		Plan:     plan,
		Steps:    steps,
		Feedback: feedback,
		Contract: h.reviewContract,
		Model:    h.reviewModel,
		Timeout:  h.reviewTimeout,
	})
	if err != nil {
		jsonErr(w, err.Error(), http.StatusBadGateway)
		return
	}

	hash, hashErr := h.currentPlanSnapshotHash(r.Context(), plan)
	jsonOK(w, newPlanReviewView(result, hash, hashErr), http.StatusAccepted)
}

// listPlanReviews returns every review of planID, most recent first,
// each with a computed staleness flag against the plan's current input.
func (h *Handler) listPlanReviews(w http.ResponseWriter, r *http.Request, planID int64) {
	plan, err := db.GetPlan(r.Context(), h.pool, planID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "plan not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	reviews, err := db.ListPlanReviews(r.Context(), h.pool, planID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hash, hashErr := h.currentPlanSnapshotHash(r.Context(), plan)
	views := make([]planReviewView, 0, len(reviews))
	for _, rv := range reviews {
		views = append(views, newPlanReviewView(rv, hash, hashErr))
	}
	jsonOK(w, views, http.StatusOK)
}

// getPlanReview returns one review of planID by reviewID, with a
// computed staleness flag. It fails with 404 if the plan does not
// exist, or if reviewID does not name a review of that plan.
func (h *Handler) getPlanReview(w http.ResponseWriter, r *http.Request, planID, reviewID int64) {
	plan, err := db.GetPlan(r.Context(), h.pool, planID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "plan not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rv, err := db.GetPlanReview(r.Context(), h.pool, reviewID)
	if errors.Is(err, db.ErrNotFound) || (err == nil && rv.PlanID != planID) {
		jsonErr(w, "review not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hash, hashErr := h.currentPlanSnapshotHash(r.Context(), plan)
	jsonOK(w, newPlanReviewView(rv, hash, hashErr), http.StatusOK)
}

func (h *Handler) HandlePlans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			RepositoryIDs []int64 `json:"repository_ids"`
			Title         string  `json:"title"`
			Summary       string  `json:"summary"`
			Content       string  `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(body.RepositoryIDs) == 0 {
			jsonErr(w, "repository_ids is required and must contain at least one repository id", http.StatusBadRequest)
			return
		}
		p, err := db.CreatePlan(r.Context(), h.pool, body.RepositoryIDs, body.Title, body.Summary, body.Content)
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
	if len(plan.Repositories) == 0 {
		jsonErr(w, "plan has no repositories", http.StatusConflict)
		return
	}
	if localPath := plan.Repositories[0].Repository.LocalPath; localPath == nil || strings.TrimSpace(*localPath) == "" {
		jsonErr(w, "primary repository has no local checkout; cannot run plan", http.StatusConflict)
		return
	}
	steps, err := db.ListPlanSteps(r.Context(), h.pool, id)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	content := strings.TrimSpace(plan.Content)
	if content == "" {
		content = "# " + plan.Title
		if plan.Summary != "" {
			content += "\n\n" + plan.Summary
		}
		for i, step := range steps {
			content += "\n\n## Phase " + strconv.Itoa(i+1) + ": Step " + strconv.Itoa(i+1) + "\n\n" + step.Text
		}
	}
	sp, err := db.CreateSpec(r.Context(), h.pool, plan.Repositories[0].RepositoryID, plan.Title, content, []byte("[]"))
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

	// Advisory only: a missing, stale, unresolved, failed, or
	// incompletely grounded review is reported alongside the created
	// workflow, but never blocks or alters execution above.
	warnings := h.runPlanReviewWarnings(r.Context(), plan)
	jsonOK(w, runPlanResult{Workflow: wf, ReviewWarnings: warnings}, http.StatusCreated)
}

// runPlanResult is runPlan's response: the created workflow plus any
// advisory Steward review warnings, so a caller sees both without the
// workflow's own JSON shape changing.
type runPlanResult struct {
	db.Workflow
	ReviewWarnings []planReviewWarning `json:"review_warnings"`
}

// runPlanReviewWarnings computes plan's advisory review warnings for
// runPlan's response. It never fails runPlan: if reviews or the
// current snapshot hash cannot be loaded, the review is conservatively
// reported missing/stale rather than surfacing an unrelated 500 from
// an already-started run.
func (h *Handler) runPlanReviewWarnings(ctx context.Context, plan db.Plan) []planReviewWarning {
	reviews, err := db.ListPlanReviews(ctx, h.pool, plan.ID)
	if err != nil {
		reviews = nil
	}
	hash, hashErr := h.currentPlanSnapshotHash(ctx, plan)
	return planReviewWarnings(reviews, hash, hashErr)
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
			Status        *string  `json:"status"`
			Title         *string  `json:"title"`
			Summary       *string  `json:"summary"`
			Content       *string  `json:"content"`
			RepositoryIDs *[]int64 `json:"repository_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.RepositoryIDs != nil && len(*body.RepositoryIDs) == 0 {
			jsonErr(w, "repository_ids must contain at least one repository id", http.StatusBadRequest)
			return
		}
		p, err := db.UpdatePlan(r.Context(), h.pool, id, db.UpdatePlanParams{Status: body.Status, Title: body.Title, Summary: body.Summary, Content: body.Content, RepositoryIDs: body.RepositoryIDs})
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
	case suffix == "reviews" && r.Method == http.MethodGet:
		h.listPlanReviews(w, r, id)
	case suffix == "reviews" && r.Method == http.MethodPost:
		h.createPlanReview(w, r, id)
	case strings.HasPrefix(suffix, "reviews/"):
		if r.Method != http.MethodGet {
			jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		reviewID, err := strconv.ParseInt(strings.TrimPrefix(suffix, "reviews/"), 10, 64)
		if err != nil {
			jsonErr(w, "invalid review id", http.StatusBadRequest)
			return
		}
		h.getPlanReview(w, r, id, reviewID)
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
