package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/discover"
	"github.com/tonis2/foundry/internal/memory"
)

// ---- export ----

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type exportPhase struct {
		db.Phase
		Logs []db.PhaseLog `json:"logs"`
	}
	type exportWorkflow struct {
		db.Workflow
		Phases []exportPhase `json:"phases"`
	}
	type exportSpec struct {
		db.Spec
		Workflows []exportWorkflow `json:"workflows"`
	}
	type exportPayload struct {
		Projects         []db.Project         `json:"projects"`
		Specs            []exportSpec         `json:"specs"`
		MemoryUpdateJobs []db.MemoryUpdateJob `json:"memory_update_jobs"`
		SpecDrafts       []db.SpecDraft       `json:"spec_drafts"`
		Profiles         []db.Profile         `json:"profiles"`
	}

	ctx := r.Context()
	fail := func(err error) bool {
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return true
		}
		return false
	}

	projects, err := db.ListProjects(ctx, s.pool)
	if fail(err) {
		return
	}
	specs, err := db.ListSpecs(ctx, s.pool, db.ListSpecsFilter{})
	if fail(err) {
		return
	}

	exportSpecs := make([]exportSpec, 0, len(specs))
	for _, spec := range specs {
		workflows, err := db.ListWorkflowsBySpec(ctx, s.pool, spec.ID)
		if fail(err) {
			return
		}
		exportWorkflows := make([]exportWorkflow, 0, len(workflows))
		for _, workflow := range workflows {
			phases, err := db.ListPhasesByWorkflow(ctx, s.pool, workflow.ID)
			if fail(err) {
				return
			}
			exportPhases := make([]exportPhase, 0, len(phases))
			for _, phase := range phases {
				logs, err := db.ListPhaseLogs(ctx, s.pool, phase.ID)
				if fail(err) {
					return
				}
				exportPhases = append(exportPhases, exportPhase{Phase: phase, Logs: logs})
			}
			exportWorkflows = append(exportWorkflows, exportWorkflow{Workflow: workflow, Phases: exportPhases})
		}
		exportSpecs = append(exportSpecs, exportSpec{Spec: spec, Workflows: exportWorkflows})
	}

	memoryUpdateJobs, err := db.ListMemoryUpdateJobs(ctx, s.pool)
	if fail(err) {
		return
	}
	specDrafts, err := db.ListSpecDrafts(ctx, s.pool)
	if fail(err) {
		return
	}
	profiles, err := db.ListProfiles(ctx, s.pool)
	if fail(err) {
		return
	}

	jsonOK(w, exportPayload{Projects: projects, Specs: exportSpecs, MemoryUpdateJobs: memoryUpdateJobs, SpecDrafts: specDrafts, Profiles: profiles}, http.StatusOK)
}

// ---- projects ----

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Name            string `json:"name"`
			RepoPath        string `json:"repo_path"`
			MemoryNamespace string `json:"memory_namespace"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		memoryNamespace := strings.TrimSpace(body.MemoryNamespace)
		if memoryNamespace == "" {
			memoryNamespace = body.Name
		}
		p, err := db.CreateProject(r.Context(), s.pool, body.Name, body.RepoPath, memoryNamespace)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusCreated)
	case http.MethodGet:
		list, err := db.ListProjects(r.Context(), s.pool)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	gitRoot, _, _ := s.runtimeSettings()
	if gitRoot == "" {
		jsonErr(w, "git_root not configured", http.StatusConflict)
		return
	}
	repos, err := discover.FindRepos(gitRoot)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// cross-reference with already-registered projects so UI can mark which are imported
	existing, _ := db.ListProjects(r.Context(), s.pool)
	byPath := make(map[string]db.Project, len(existing))
	for _, p := range existing {
		byPath[p.RepoPath] = p
	}
	type repoItem struct {
		discover.Repo
		Imported        bool   `json:"imported"`
		MemoryNamespace string `json:"memory_namespace"`
	}
	out := make([]repoItem, 0, len(repos))
	for _, repo := range repos {
		p, imported := byPath[repo.Path]
		out = append(out, repoItem{Repo: repo, Imported: imported, MemoryNamespace: p.MemoryNamespace})
	}
	jsonOK(w, out, http.StatusOK)
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r.URL.Path, "/api/projects/")
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		p, err := db.GetProject(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case http.MethodPatch:
		var body struct {
			Name            *string `json:"name"`
			RepoPath        *string `json:"repo_path"`
			MemoryNamespace *string `json:"memory_namespace"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := db.UpdateProject(r.Context(), s.pool, id, db.UpdateProjectParams{
			Name:            body.Name,
			RepoPath:        body.RepoPath,
			MemoryNamespace: body.MemoryNamespace,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case http.MethodDelete:
		if err := db.DeleteProject(r.Context(), s.pool, id); errors.Is(err, db.ErrNotFound) {
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

// ---- specs ----

func (s *Server) handleSpecs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			ProjectID int64           `json:"project_id"`
			Title     string          `json:"title"`
			Content   string          `json:"content"`
			Tags      json.RawMessage `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		tags := []byte("[]")
		if body.Tags != nil {
			tags = body.Tags
		}
		sp, err := db.CreateSpec(r.Context(), s.pool, body.ProjectID, body.Title, body.Content, tags)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusCreated)
	case http.MethodGet:
		f := db.ListSpecsFilter{
			Status: r.URL.Query().Get("status"),
		}
		if pid := r.URL.Query().Get("project_id"); pid != "" {
			f.ProjectID, _ = strconv.ParseInt(pid, 10, 64)
		}
		list, err := db.ListSpecs(r.Context(), s.pool, f)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSpec(w http.ResponseWriter, r *http.Request) {
	// routes under /api/specs/:id and /api/specs/:id/promote
	path := strings.TrimPrefix(r.URL.Path, "/api/specs/")
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
	case suffix == "workflows" && r.Method == http.MethodGet:
		wfs, err := db.ListWorkflowsBySpec(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, wfs, http.StatusOK)
	case suffix == "promote" && r.Method == http.MethodPost:
		sp, err := db.GetSpec(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		track := "polish"
		sp, err = db.UpdateSpec(r.Context(), s.pool, sp.ID, db.UpdateSpecParams{Track: &track})
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusOK)
	case suffix == "" && r.Method == http.MethodGet:
		sp, err := db.GetSpec(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusOK)
	case suffix == "" && r.Method == http.MethodPatch:
		var body struct {
			Title   *string         `json:"title"`
			Content *string         `json:"content"`
			Tags    json.RawMessage `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		var tags []byte
		if body.Tags != nil {
			tags = body.Tags
		}
		sp, err := db.UpdateSpec(r.Context(), s.pool, id, db.UpdateSpecParams{
			Title:   body.Title,
			Content: body.Content,
			Tags:    tags,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, sp, http.StatusOK)
	case suffix == "" && r.Method == http.MethodDelete:
		_, err := db.GetSpec(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		err = db.DeleteSpec(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

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

func (s *Server) handleWorkflowMemoryUpdate(w http.ResponseWriter, r *http.Request, workflowID int64, action string) {
	switch {
	case action == "" && r.Method == http.MethodGet:
		job, err := db.GetLatestMemoryUpdateJobByWorkflow(r.Context(), s.pool, workflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	case action == "" && r.Method == http.MethodPost:
		var body struct {
			Feedback         string `json:"feedback"`
			Comment          string `json:"comment"`
			ProposalMarkdown string `json:"proposal_markdown"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		proposal := strings.TrimSpace(body.ProposalMarkdown)
		comment := strings.TrimSpace(body.Comment)
		if comment == "" {
			comment = strings.TrimSpace(body.Feedback)
		}
		if proposal == "" {
			var err error
			proposal, err = s.generateWorkflowMemoryProposal(r.Context(), workflowID, comment, "")
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		job, err := db.CreateMemoryUpdateJob(r.Context(), s.pool, workflowID, proposal, comment)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusCreated)
	case action == "accept" && r.Method == http.MethodPost:
		job, err := db.GetLatestMemoryUpdateJobByWorkflow(r.Context(), s.pool, workflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _, proj, err := s.workflowProject(r.Context(), workflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, memoryRepoPath, _ := s.runtimeSettings()
		path, err := memory.WriteApprovedUpdate(memoryRepoPath, proj.MemoryNamespace, workflowID, job.ProposalMarkdown)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, acceptMemoryUpdateParams(path))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	case action == "reject" && r.Method == http.MethodPost:
		var body struct {
			Comment string `json:"comment"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		job, err := db.GetLatestMemoryUpdateJobByWorkflow(r.Context(), s.pool, workflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, rejectMemoryUpdateParams(body.Comment))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	case action == "revise" && r.Method == http.MethodPost:
		var body struct {
			Comment          string `json:"comment"`
			ProposalMarkdown string `json:"proposal_markdown"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		job, err := db.GetLatestMemoryUpdateJobByWorkflow(r.Context(), s.pool, workflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		proposal := strings.TrimSpace(body.ProposalMarkdown)
		if proposal == "" {
			proposal, err = s.generateWorkflowMemoryProposal(r.Context(), workflowID, body.Comment, job.ProposalMarkdown)
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, reviseMemoryUpdateParams(body.Comment, proposal))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func (s *Server) handleMemoryUpdate(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/memory-updates/")
	parts := strings.SplitN(path, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	job, err := db.GetMemoryUpdateJob(r.Context(), s.pool, id)
	if errors.Is(err, db.ErrNotFound) {
		jsonErr(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		jsonOK(w, job, http.StatusOK)
	case action == "accept" && r.Method == http.MethodPost:
		_, _, proj, err := s.workflowProject(r.Context(), job.WorkflowID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, memoryRepoPath, _ := s.runtimeSettings()
		path, err := memory.WriteApprovedUpdate(memoryRepoPath, proj.MemoryNamespace, job.WorkflowID, job.ProposalMarkdown)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, acceptMemoryUpdateParams(path))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	case action == "reject" && r.Method == http.MethodPost:
		var body struct {
			Comment string `json:"comment"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, rejectMemoryUpdateParams(body.Comment))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	case action == "revise" && r.Method == http.MethodPost:
		var body struct {
			Comment          string `json:"comment"`
			ProposalMarkdown string `json:"proposal_markdown"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		proposal := strings.TrimSpace(body.ProposalMarkdown)
		if proposal == "" {
			proposal, err = s.generateWorkflowMemoryProposal(r.Context(), job.WorkflowID, body.Comment, job.ProposalMarkdown)
			if errors.Is(err, db.ErrNotFound) {
				jsonErr(w, "not found", http.StatusNotFound)
				return
			}
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		job, err = db.UpdateMemoryUpdateJob(r.Context(), s.pool, job.ID, reviseMemoryUpdateParams(body.Comment, proposal))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, job, http.StatusOK)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func acceptMemoryUpdateParams(path string) db.UpdateMemoryUpdateJobParams {
	accepted := "accepted"
	path = strings.TrimSpace(path)
	return db.UpdateMemoryUpdateJobParams{Status: &accepted, MemoryPath: &path}
}

func rejectMemoryUpdateParams(comment string) db.UpdateMemoryUpdateJobParams {
	rejected := "rejected"
	comment = strings.TrimSpace(comment)
	return db.UpdateMemoryUpdateJobParams{Status: &rejected, ReviewerComment: &comment}
}

func reviseMemoryUpdateParams(comment, proposal string) db.UpdateMemoryUpdateJobParams {
	pending := "pending"
	comment = strings.TrimSpace(comment)
	proposal = strings.TrimSpace(proposal)
	params := db.UpdateMemoryUpdateJobParams{Status: &pending, ReviewerComment: &comment}
	if proposal != "" {
		params.ProposalMarkdown = &proposal
	}
	return params
}

func (s *Server) generateWorkflowMemoryProposal(ctx context.Context, workflowID int64, comment, previousProposal string) (string, error) {
	contextMarkdown, err := s.buildWorkflowMemoryProposal(ctx, workflowID, comment)
	if err != nil {
		return "", err
	}
	return s.generateMemoryProposalMarkdown(ctx, workflowID, contextMarkdown, comment, previousProposal)
}

func (s *Server) generateMemoryProposalMarkdown(ctx context.Context, workflowID int64, contextMarkdown, comment, previousProposal string) (string, error) {
	_, memoryRepoPath, _ := s.runtimeSettings()
	if strings.TrimSpace(memoryRepoPath) == "" {
		return "", fmt.Errorf("memory repo path not configured")
	}
	if s.cerb == nil {
		return "", fmt.Errorf("cerberus client not configured")
	}
	session := fmt.Sprintf("foundry-memory-update-%d-%d", workflowID, time.Now().UnixNano())
	profilePath, profileErr := s.writeProfileFile(ctx, session)
	if profileErr != nil {
		log.Printf("memory update proposal: write profile file: %v (proceeding without profile)", profileErr)
	}
	defer removeProfileFile(session)
	s.cerb.SetProfile(profilePath)
	s.cerb.SetRepoPath(memoryRepoPath)
	out, err := s.cerb.Generate(ctx, session, memoryProposalPrompt(contextMarkdown, comment, previousProposal))
	if cleanErr := s.cerb.Clean(ctx, session); cleanErr != nil {
		log.Printf("memory update proposal: clean session %s: %v", session, cleanErr)
	}
	if err != nil {
		return "", err
	}
	proposal := strings.TrimSpace(out)
	if proposal == "" {
		return "", fmt.Errorf("cerberus returned empty memory proposal")
	}
	return proposal, nil
}

func memoryProposalPrompt(contextMarkdown, comment, previousProposal string) string {
	var b strings.Builder
	b.WriteString("You are updating a private project memory repository. Read the existing memory files in /workspace as needed, but do not create, edit, delete, or commit files.\n\n")
	b.WriteString("Return only the proposed durable memory update as markdown. Include concise facts that should help future work on this project. Exclude transient logs, prompt bodies, and implementation noise.\n")
	if strings.TrimSpace(comment) != "" {
		b.WriteString("\nReviewer instruction:\n")
		b.WriteString(strings.TrimSpace(comment))
		b.WriteString("\n")
	}
	if strings.TrimSpace(previousProposal) != "" {
		b.WriteString("\nCurrent proposal to revise:\n")
		b.WriteString(strings.TrimSpace(previousProposal))
		b.WriteString("\n")
	}
	b.WriteString("\nWorkflow context:\n")
	b.WriteString(strings.TrimSpace(contextMarkdown))
	b.WriteString("\n")
	return b.String()
}

func (s *Server) workflowProject(ctx context.Context, workflowID int64) (db.Workflow, db.Spec, db.Project, error) {
	wf, err := db.GetWorkflow(ctx, s.pool, workflowID)
	if err != nil {
		return wf, db.Spec{}, db.Project{}, err
	}
	sp, err := db.GetSpec(ctx, s.pool, wf.SpecID)
	if err != nil {
		return wf, sp, db.Project{}, err
	}
	proj, err := db.GetProject(ctx, s.pool, sp.ProjectID)
	return wf, sp, proj, err
}

func (s *Server) buildWorkflowMemoryProposal(ctx context.Context, workflowID int64, feedback string) (string, error) {
	wf, sp, proj, err := s.workflowProject(ctx, workflowID)
	if err != nil {
		return "", err
	}
	phases, err := db.ListPhasesByWorkflow(ctx, s.pool, workflowID)
	if err != nil {
		return "", err
	}

	return formatWorkflowMemoryProposal(wf, sp, proj, phases, feedback), nil
}

func formatWorkflowMemoryProposal(wf db.Workflow, sp db.Spec, proj db.Project, phases []db.Phase, feedback string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Workflow %d memory update\n\n", wf.ID))
	b.WriteString(fmt.Sprintf("Project: %s\nSpec: %s\nTrack: %s\nStatus: %s\n", proj.Name, sp.Title, wf.Track, wf.Status))
	if feedback = strings.TrimSpace(feedback); feedback != "" {
		b.WriteString("\n## Reviewer feedback\n\n")
		b.WriteString(feedback)
		b.WriteString("\n")
	}
	b.WriteString("\n## Phase decisions\n")
	for _, ph := range phases {
		b.WriteString(fmt.Sprintf("\n### Phase %d: %s\n\n", ph.Position, ph.Name))
		if ph.DecisionSummary != nil && strings.TrimSpace(*ph.DecisionSummary) != "" {
			b.WriteString("Summary: ")
			b.WriteString(strings.TrimSpace(*ph.DecisionSummary))
			b.WriteString("\n")
		}
		if ph.DecisionRationale != nil && strings.TrimSpace(*ph.DecisionRationale) != "" {
			b.WriteString("Rationale: ")
			b.WriteString(strings.TrimSpace(*ph.DecisionRationale))
			b.WriteString("\n")
		}
		if len(ph.FilesTouched) > 0 && string(ph.FilesTouched) != "[]" {
			b.WriteString("Files touched: `")
			b.WriteString(string(ph.FilesTouched))
			b.WriteString("`\n")
		}
		if feedback := formatPhaseFeedback(ph.PhaseFeedback); feedback != "" {
			b.WriteString("Structured feedback:\n")
			b.WriteString(feedback)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func formatPhaseFeedback(raw []byte) string {
	if len(raw) == 0 || string(raw) == "{}" {
		return ""
	}
	var fb struct {
		Result          string   `json:"result"`
		UsefulContext   []string `json:"useful_context"`
		Problems        []string `json:"problems"`
		SuggestedMemory string   `json:"suggested_memory"`
		Confidence      float64  `json:"confidence"`
	}
	if err := json.Unmarshal(raw, &fb); err != nil {
		return ""
	}
	var lines []string
	if s := strings.TrimSpace(fb.Result); s != "" {
		lines = append(lines, "- Result: "+s)
	}
	for _, s := range fb.UsefulContext {
		if s = strings.TrimSpace(s); s != "" {
			lines = append(lines, "- Useful context: "+s)
		}
	}
	for _, s := range fb.Problems {
		if s = strings.TrimSpace(s); s != "" {
			lines = append(lines, "- Problem: "+s)
		}
	}
	if s := strings.TrimSpace(fb.SuggestedMemory); s != "" {
		lines = append(lines, "- Suggested memory: "+s)
	}
	if fb.Confidence != 0 {
		lines = append(lines, fmt.Sprintf("- Confidence: %.2f", fb.Confidence))
	}
	return strings.Join(lines, "\n")
}

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

// ---- cerberus callback ----

func (s *Server) handleCerberusCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		jsonErr(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.handleCompactCerberusEvent(r.Context(), raw); err != nil {
		code := http.StatusInternalServerError
		if strings.HasPrefix(err.Error(), "invalid json") || strings.Contains(err.Error(), "session and type required") {
			code = http.StatusBadRequest
		}
		jsonErr(w, err.Error(), code)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---- cerberus sessions ----

func (s *Server) handleCerberusSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		views, err := s.knownCerberusSessionViews(r.Context(), true)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, views, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCerberusSession(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/cerberus/sessions/")
	if strings.HasSuffix(path, "/clean") && r.Method == http.MethodPost {
		session := strings.TrimSuffix(path, "/clean")
		force := r.URL.Query().Get("force") == "1" || r.URL.Query().Get("force") == "true"
		var body struct {
			Force bool `json:"force"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Force {
				force = true
			}
		}
		s.cleanKnownCerberusSession(w, r, session, force)
		return
	}
	jsonErr(w, "not found", http.StatusNotFound)
}

func (s *Server) cleanKnownCerberusSession(w http.ResponseWriter, r *http.Request, session string, force bool) {
	known, err := db.ListKnownCerberusSessions(r.Context(), s.pool)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var item *db.KnownCerberusSession
	for i := range known {
		if known[i].Session == session {
			item = &known[i]
			break
		}
	}
	if item == nil {
		jsonErr(w, "unknown Foundry session", http.StatusNotFound)
		return
	}
	if !item.SafeToClean && !force {
		jsonErr(w, "refusing to clean active session: "+item.UnsafeReason, http.StatusConflict)
		return
	}
	if strings.TrimSpace(item.ProjectRepo) != "" {
		s.cerb.SetRepoPath(item.ProjectRepo)
	}
	if err := s.cerb.Clean(r.Context(), item.Session); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	db.DeleteCerberusEvents(r.Context(), s.pool, item.Session)
	removeProfileFile(item.Session)
	jsonOK(w, map[string]string{"status": "cleaned", "session": item.Session}, http.StatusOK)
}

// ---- settings ----

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(s.cfgPath)
		if err != nil {
			jsonErr(w, "cannot read config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		runtimeValues, err := s.loadRuntimeSettings(r.Context())
		if err != nil {
			jsonErr(w, "cannot read runtime settings: "+err.Error(), http.StatusInternalServerError)
			return
		}
		merged := mergeYAMLRuntimeSettings(string(data), runtimeValues)
		w.Header().Set("Content-Type", "application/x-yaml")
		w.Write([]byte(merged))
	case http.MethodPatch:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		runtimePatch := map[string]string{}
		configPatch := map[string]any{}
		for k, v := range body {
			if isRuntimeSetting(k) {
				runtimePatch[k] = strings.TrimSpace(fmt.Sprint(v))
			} else {
				configPatch[k] = v
			}
		}
		if len(configPatch) > 0 {
			data, err := os.ReadFile(s.cfgPath)
			if err != nil {
				jsonErr(w, "cannot read config: "+err.Error(), http.StatusInternalServerError)
				return
			}
			updated := applyYAMLPatch(string(data), configPatch)
			if err := os.WriteFile(s.cfgPath, []byte(updated), 0644); err != nil {
				jsonErr(w, "cannot write config: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		for k, v := range runtimePatch {
			if _, err := db.UpsertAppSetting(r.Context(), s.pool, k, v); err != nil {
				jsonErr(w, "cannot write setting "+k+": "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if len(runtimePatch) > 0 {
			s.updateRuntimeSettings(runtimePatch)
		}
		jsonOK(w, map[string]bool{"success": true}, http.StatusOK)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---- profiles ----

func (s *Server) writeProfileFile(ctx context.Context, session string) (string, error) {
	_, _, cerberusProfile := s.runtimeSettings()
	if cerberusProfile == "" {
		return "", nil
	}
	p, err := db.GetProfileByName(ctx, s.pool, cerberusProfile)
	if err == db.ErrNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup profile %q: %w", cerberusProfile, err)
	}
	payload := map[string]any{}
	if p.DefaultModel != "" {
		payload["default_model"] = p.DefaultModel
	}
	if p.DefaultImage != "" {
		payload["default_image"] = p.DefaultImage
	}
	if p.AWSProfile != "" {
		payload["aws_profile"] = p.AWSProfile
	}
	if p.AWSRegion != "" {
		payload["aws_region"] = p.AWSRegion
	}
	if len(p.ExtraEnv) > 0 {
		payload["extra_env"] = p.ExtraEnv
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal profile: %w", err)
	}
	path := profileFilePath(session)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write profile file: %w", err)
	}
	return path, nil
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		profiles, err := db.ListProfiles(r.Context(), s.pool)
		if err != nil {
			jsonErr(w, "list profiles: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if profiles == nil {
			profiles = []db.Profile{}
		}
		jsonOK(w, profiles, http.StatusOK)

	case http.MethodPost:
		var body struct {
			Name         string            `json:"name"`
			DefaultModel string            `json:"default_model"`
			DefaultImage string            `json:"default_image"`
			AWSProfile   string            `json:"aws_profile"`
			AWSRegion    string            `json:"aws_region"`
			ExtraEnv     map[string]string `json:"extra_env"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Name == "" {
			jsonErr(w, "name is required", http.StatusBadRequest)
			return
		}
		if body.ExtraEnv == nil {
			body.ExtraEnv = map[string]string{}
		}
		p, err := db.CreateProfile(r.Context(), s.pool, body.Name, body.DefaultModel, body.DefaultImage, body.AWSProfile, body.AWSRegion, body.ExtraEnv)
		if err != nil {
			jsonErr(w, "create profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusCreated)

	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	idStr := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/profiles/"), "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		p, err := db.GetProfile(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, "get profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case http.MethodPatch:
		var body struct {
			Name         *string           `json:"name"`
			DefaultModel *string           `json:"default_model"`
			DefaultImage *string           `json:"default_image"`
			AWSProfile   *string           `json:"aws_profile"`
			AWSRegion    *string           `json:"aws_region"`
			ExtraEnv     map[string]string `json:"extra_env"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Name != nil && *body.Name == "" {
			jsonErr(w, "name is required", http.StatusBadRequest)
			return
		}
		p, err := db.UpdateProfile(r.Context(), s.pool, id, db.UpdateProfileParams{
			Name: body.Name, DefaultModel: body.DefaultModel, DefaultImage: body.DefaultImage,
			AWSProfile: body.AWSProfile, AWSRegion: body.AWSRegion, ExtraEnv: body.ExtraEnv,
		})
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, "update profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, p, http.StatusOK)
	case http.MethodDelete:
		if err := db.DeleteProfile(r.Context(), s.pool, id); errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		} else if err != nil {
			jsonErr(w, "delete profile: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
