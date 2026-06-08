package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

// ---- workflows ----

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SpecID     int64    `json:"spec_id"`
		MaxCostUSD *float64 `json:"max_cost_usd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	sp, err := db.GetSpec(r.Context(), s.pool, body.SpecID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "spec not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	maxCost := body.MaxCostUSD
	if maxCost == nil {
		def := s.defaultBudget
		maxCost = &def
	}
	wf, err := db.CreateWorkflow(r.Context(), s.pool, sp.ID, sp.Track, maxCost)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// mark spec running
	runStatus := "running"
	_, _ = db.UpdateSpec(r.Context(), s.pool, sp.ID, db.UpdateSpecParams{Status: &runStatus})

	s.runner.Start(wf.ID)
	jsonOK(w, wf, http.StatusCreated)
}

func (s *Server) handleWorkflow(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/workflows/")
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
		wf, err := db.GetWorkflow(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, wf, http.StatusOK)
	case suffix == "" && r.Method == http.MethodDelete:
		s.runner.Stop(id)
		if err := db.DeleteWorkflow(r.Context(), s.pool, id); errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		} else if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case suffix == "phases" && r.Method == http.MethodGet:
		phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, phases, http.StatusOK)
	case suffix == "resume" && r.Method == http.MethodPost:
		phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, ph := range phases {
			if ph.Status == "failed" {
				_, _ = db.UpdatePhase(r.Context(), s.pool, ph.ID, resumeFailedPhaseUpdate())
				break
			}
		}
		_ = db.UpdateWorkflowStatus(r.Context(), s.pool, id, "running")
		s.runner.Start(id)
		wf, _ := db.GetWorkflow(r.Context(), s.pool, id)
		jsonOK(w, wf, http.StatusOK)
	case suffix == "stop" && r.Method == http.MethodPost:
		s.runner.Stop(id)
		jsonOK(w, map[string]string{"status": "stopping"}, http.StatusOK)
	case suffix == "follow-up" && r.Method == http.MethodPost:
		s.handleWorkflowFollowUp(w, r, id)
	case strings.HasPrefix(suffix, "memory-update"):
		s.handleWorkflowMemoryUpdate(w, r, id, strings.Trim(strings.TrimPrefix(suffix, "memory-update"), "/"))
	case suffix == "stream":
		s.streamWorkflow(w, r, id)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) handleWorkflowFollowUp(w http.ResponseWriter, r *http.Request, workflowID int64) {
	wf, sp, _, err := s.workflowProject(r.Context(), workflowID)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wf.Status != "failed" {
		jsonErr(w, "follow-up runs can only be created for failed workflows", http.StatusConflict)
		return
	}
	phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, workflowID)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	failed := make([]db.Phase, 0)
	for _, ph := range phases {
		if ph.Status == "failed" {
			failed = append(failed, ph)
		}
	}
	if len(failed) == 0 {
		jsonErr(w, "workflow has no failed phases", http.StatusConflict)
		return
	}

	content := s.buildFollowUpSpecContent(r.Context(), sp, wf, failed)
	newTitle := "Follow-up: " + sp.Title
	newSpec, err := db.CreateSpec(r.Context(), s.pool, sp.ProjectID, newTitle, content, sp.Tags)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	track := sp.Track
	running := "running"
	newSpec, err = db.UpdateSpec(r.Context(), s.pool, newSpec.ID, db.UpdateSpecParams{Track: &track, Status: &running})
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	newWorkflow, err := db.CreateWorkflow(r.Context(), s.pool, newSpec.ID, newSpec.Track, wf.MaxCostUSD)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.runner.Start(newWorkflow.ID)
	jsonOK(w, newWorkflow, http.StatusCreated)
}

func (s *Server) buildFollowUpSpecContent(ctx context.Context, sp db.Spec, wf db.Workflow, failed []db.Phase) string {
	return buildFollowUpSpecContentWithContext(sp, s.buildFollowUpContext(ctx, wf, failed))
}

func buildFollowUpSpecContentWithContext(sp db.Spec, context string) string {
	content := strings.TrimSpace(sp.Content)
	idx := strings.Index(content, "\n## Phase ")
	if idx == -1 {
		return strings.TrimSpace(content + "\n\n" + context)
	}
	return strings.TrimSpace(content[:idx] + "\n\n" + context + "\n" + content[idx+1:])
}

func (s *Server) buildFollowUpContext(ctx context.Context, wf db.Workflow, failed []db.Phase) string {
	return buildFollowUpFailureContext(ctx, wf, failed, func(ctx context.Context, phaseID int64, limit int) ([]db.PhaseLog, error) {
		return db.ListRecentPhaseLogs(ctx, s.pool, phaseID, limit)
	})
}

func buildFollowUpFailureContext(ctx context.Context, wf db.Workflow, failed []db.Phase, recentLogs func(context.Context, int64, int) ([]db.PhaseLog, error)) string {
	var b strings.Builder
	b.WriteString("## Follow-up run context\n\n")
	b.WriteString(fmt.Sprintf("This spec was generated as a follow-up to failed workflow #%d. Use the failure context below to avoid repeating the same mistakes and to complete the original phases.\n", wf.ID))
	for _, ph := range failed {
		b.WriteString(fmt.Sprintf("\n### Failed phase %d: %s\n\n", ph.Position, ph.Name))
		b.WriteString(fmt.Sprintf("- Phase ID: %d\n- Status: %s\n- Retry count: %d\n", ph.ID, ph.Status, ph.RetryCount))
		if ph.ReviewVerdict != nil && strings.TrimSpace(*ph.ReviewVerdict) != "" {
			b.WriteString("- Review verdict: ")
			b.WriteString(strings.TrimSpace(*ph.ReviewVerdict))
			b.WriteString("\n")
		}
		if ph.ReviewNotes != nil && strings.TrimSpace(*ph.ReviewNotes) != "" {
			b.WriteString("\nReview notes:\n")
			b.WriteString(indentBlock(strings.TrimSpace(*ph.ReviewNotes)))
			b.WriteString("\n")
		}
		if ph.DecisionSummary != nil && strings.TrimSpace(*ph.DecisionSummary) != "" {
			b.WriteString("\nDecision summary:\n")
			b.WriteString(indentBlock(strings.TrimSpace(*ph.DecisionSummary)))
			b.WriteString("\n")
		}
		if ph.DecisionRationale != nil && strings.TrimSpace(*ph.DecisionRationale) != "" {
			b.WriteString("\nDecision rationale:\n")
			b.WriteString(indentBlock(strings.TrimSpace(*ph.DecisionRationale)))
			b.WriteString("\n")
		}
		if ph.PromptSent != nil && strings.TrimSpace(*ph.PromptSent) != "" {
			b.WriteString("\nPrompt sent excerpt:\n")
			b.WriteString(indentBlock(truncateString(strings.TrimSpace(*ph.PromptSent), 2000)))
			b.WriteString("\n")
		}
		var logs []db.PhaseLog
		if recentLogs != nil {
			var err error
			logs, err = recentLogs(ctx, ph.ID, 80)
			if err != nil {
				b.WriteString("\nLog summary: unavailable: ")
				b.WriteString(err.Error())
				b.WriteString("\n")
				continue
			}
		}
		if len(logs) > 0 {
			b.WriteString("\nRecent log summary (tail):\n")
			var lines []string
			for _, l := range logs {
				line := strings.TrimSpace(l.Line)
				if line != "" {
					lines = append(lines, line)
				}
			}
			b.WriteString(indentBlock(truncateString(strings.Join(lines, "\n"), 4000)))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}
