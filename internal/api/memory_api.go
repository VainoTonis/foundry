package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/memory"
)

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
