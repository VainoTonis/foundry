package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/discover"
	"github.com/tonis2/foundry/internal/workflow"
)

// Server holds all handler dependencies.
type Server struct {
	pool          *pgxpool.Pool
	runner        *workflow.Runner
	cerb          *cerberus.Client
	mux           *http.ServeMux
	defaultBudget float64
	gitRoot       string
	cfgPath       string
}

func NewServer(pool *pgxpool.Pool, runner *workflow.Runner, cerb *cerberus.Client, defaultBudget float64, gitRoot string, cfgPath string) *Server {
	s := &Server{pool: pool, runner: runner, cerb: cerb, defaultBudget: defaultBudget, gitRoot: gitRoot, cfgPath: cfgPath}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/projects", s.handleProjects)
	s.mux.HandleFunc("/api/projects/discover", s.handleDiscover)
	s.mux.HandleFunc("/api/projects/", s.handleProject)

	s.mux.HandleFunc("/api/specs", s.handleSpecs)
	s.mux.HandleFunc("/api/specs/", s.handleSpec)

	s.mux.HandleFunc("/api/workflows", s.handleWorkflows)
	s.mux.HandleFunc("/api/workflows/", s.handleWorkflow)

	s.mux.HandleFunc("/api/phases/", s.handlePhase)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/spec-drafts", s.handleSpecDrafts)
	s.mux.HandleFunc("/api/spec-drafts/", s.handleSpecDraft)
}

// ---- projects ----

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var body struct {
			Name     string `json:"name"`
			RepoPath string `json:"repo_path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		p, err := db.CreateProject(r.Context(), s.pool, body.Name, body.RepoPath)
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
	if s.gitRoot == "" {
		jsonErr(w, "git_root not configured", http.StatusConflict)
		return
	}
	repos, err := discover.FindRepos(s.gitRoot)
	if err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// cross-reference with already-registered projects so UI can mark which are imported
	existing, _ := db.ListProjects(r.Context(), s.pool)
	byPath := make(map[string]bool, len(existing))
	for _, p := range existing {
		byPath[p.RepoPath] = true
	}
	type repoItem struct {
		discover.Repo
		Imported bool `json:"imported"`
	}
	out := make([]repoItem, 0, len(repos))
	for _, repo := range repos {
		out = append(out, repoItem{Repo: repo, Imported: byPath[repo.Path]})
	}
	jsonOK(w, out, http.StatusOK)
}

func (s *Server) handleProject(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r.URL.Path, "/api/projects/")
	if err != nil {
		jsonErr(w, "invalid id", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	case suffix == "phases" && r.Method == http.MethodGet:
		phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, phases, http.StatusOK)
	case suffix == "resume" && r.Method == http.MethodPost:
		// reset failed phase and restart runner
		phases, err := db.ListPhasesByWorkflow(r.Context(), s.pool, id)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, ph := range phases {
			if ph.Status == "failed" {
				pending := "pending"
				zero := 0
				_, _ = db.UpdatePhase(r.Context(), s.pool, ph.ID, db.UpdatePhaseParams{
					Status:     &pending,
					RetryCount: &zero,
				})
				break
			}
		}
		_ = db.UpdateWorkflowStatus(r.Context(), s.pool, id, "running")
		s.runner.Start(id)
		wf, _ := db.GetWorkflow(r.Context(), s.pool, id)
		jsonOK(w, wf, http.StatusOK)
	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

// ---- phases ----

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
		done := "done"
		pass := "pass"
		now := time.Now()
		_, err := db.UpdatePhase(r.Context(), s.pool, id, db.UpdatePhaseParams{
			Status:        &done,
			ReviewVerdict: &pass,
			FinishedAt:    &now,
		})
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
		failed := "failed"
		fail := "fail"
		now := time.Now()
		_, err := db.UpdatePhase(r.Context(), s.pool, id, db.UpdatePhaseParams{
			Status:        &failed,
			ReviewVerdict: &fail,
			FinishedAt:    &now,
		})
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
			if err := s.cerb.Clean(r.Context(), *ph.CerberusSession); err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
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
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var lastID int64
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			logs, err := db.StreamPhaseLogs(r.Context(), s.pool, phaseID, lastID)
			if err != nil {
				return
			}
			for _, l := range logs {
				data, _ := json.Marshal(l)
				fmt.Fprintf(w, "data: %s\n\n", data)
				lastID = l.ID
			}
			flusher.Flush()
			// stop streaming if phase is terminal
			ph, err := db.GetPhase(r.Context(), s.pool, phaseID)
			if err != nil {
				return
			}
			if ph.Status == "done" || ph.Status == "failed" {
				fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
		}
	}
}

// helpers

func jsonOK(w http.ResponseWriter, v any, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func pathID(path, prefix string) (int64, error) {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, "/")
	return strconv.ParseInt(s, 10, 64)
}

// ---- spec-drafts ----

const specBuilderPrompt = `You are a spec writer for Foundry, a spec-driven development loop that runs AI agents.

Your job: help the user write a Foundry spec — a markdown document that defines what should be built and how it should be broken into phases for an AI agent to execute.

## Spec format

A spec is markdown with this structure:

# Feature title

Global context — background, constraints, anything the agent needs to know.
This is prepended to every phase prompt automatically.

## Phase 1: Name
What this phase should accomplish. This becomes the exact prompt body sent to the agent.
Be specific: what files to create/edit, what the output should be, how to verify it works.

## Phase 2: Name
...

Rules:
- Sections starting with ## Phase N: become executable phases (N must be sequential integers starting at 1)
- Everything before the first phase = global context (shared across all phases)
- Each phase goal should be independently executable by an AI agent in a fresh container
- Phases should be small enough that one agent can complete them in a single session
- Prefer explicit over clever — spell out what files to touch, what functions to write

## Good example

# User authentication

Stack: Go + pgx + stdlib net/http. No frameworks, no ORMs.
Project already has: users table (id, email, password_hash, created_at).

## Phase 1: Password hashing utilities
Create internal/auth/hash.go with HashPassword(plain string) (string, error) using bcrypt cost 12, and CheckPassword(plain, hash string) bool. Add internal/auth/hash_test.go covering both. No external deps beyond golang.org/x/crypto.

## Phase 2: Login endpoint
Add POST /api/login to internal/api/handlers.go. Accept {email, password} JSON. Return {token} on success, 401 on failure.

## Phase 3: Auth middleware
Add AuthMiddleware(next http.Handler) http.Handler in internal/api/middleware.go. Reads Authorization: Bearer <token>, validates JWT, sets user_id in context.

When ready to output the final spec, put it in a markdown code block preceded by the exact line: FINAL SPEC:`

func (s *Server) handleSpecDrafts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := db.ListSpecDrafts(r.Context(), s.pool)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, list, http.StatusOK)
	case http.MethodPost:
		var body struct {
			ProjectID *int64 `json:"project_id"`
			Title     string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		draft, err := db.CreateSpecDraft(r.Context(), s.pool, body.ProjectID, body.Title)
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		session := cerberus.DraftSessionName(draft.ID)
		if _, err := db.UpdateSpecDraft(r.Context(), s.pool, draft.ID, db.UpdateSpecDraftParams{CerberusSession: &session}); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		draft.CerberusSession = session

		initialPrompt := specBuilderPrompt
		if body.Title != "" {
			initialPrompt += "\n\nThe user wants to build: " + body.Title
		}

		ctx120, cancel := context.WithTimeout(r.Context(), 120*time.Second)
		defer cancel()
		if err := s.cerb.Chat(ctx120, session, initialPrompt); err != nil {
			log.Printf("spec-builder chat start error: %v", err)
		}

		ctx90, cancel2 := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel2()
		agentMsg, err := s.cerb.Message(ctx90, session, "Hello! What would you like to build? Tell me about the feature or system you have in mind.")
		if err != nil {
			log.Printf("spec-builder first message error: %v", err)
			agentMsg = "Hello! Tell me what you'd like to build."
		}

		msgs := appendMessage(nil, "assistant", agentMsg)
		draft, _ = db.UpdateSpecDraft(r.Context(), s.pool, draft.ID, db.UpdateSpecDraftParams{Messages: msgs})
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
	case suffix == "" && r.Method == http.MethodGet:
		draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
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
		draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		msgs := appendMessage(draft.Messages, "user", body.Content)
		ctx90, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		agentResp, err := s.cerb.Message(ctx90, draft.CerberusSession, body.Content)
		if err != nil {
			jsonErr(w, "agent error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		msgs = appendMessage(msgs, "assistant", agentResp)
		draft, err = db.UpdateSpecDraft(r.Context(), s.pool, id, db.UpdateSpecDraftParams{Messages: msgs})
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, draft, http.StatusOK)

	case suffix == "save" && r.Method == http.MethodPost:
		draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		specContent := extractFinalSpec(draft.Messages)
		if specContent == "" {
			ctx60, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			defer cancel()
			agentResp, err := s.cerb.Message(ctx60, draft.CerberusSession, "Please output the final spec now. Write exactly the line 'FINAL SPEC:' followed by a markdown code block containing the complete spec.")
			if err != nil {
				jsonErr(w, "could not get final spec: "+err.Error(), http.StatusInternalServerError)
				return
			}
			msgs := appendMessage(draft.Messages, "assistant", agentResp)
			draft, _ = db.UpdateSpecDraft(r.Context(), s.pool, id, db.UpdateSpecDraftParams{Messages: msgs})
			specContent = extractFinalSpec(draft.Messages)
		}
		if specContent == "" {
			jsonErr(w, "could not extract spec from conversation", http.StatusUnprocessableEntity)
			return
		}
		if err := s.cerb.Close(r.Context(), draft.CerberusSession); err != nil {
			log.Printf("spec-builder close error: %v", err)
		}
		if err := s.cerb.Clean(r.Context(), draft.CerberusSession); err != nil {
			log.Printf("spec-builder clean error: %v", err)
		}
		var projID int64
		if draft.ProjectID != nil {
			projID = *draft.ProjectID
		}
		sp, err := db.CreateSpec(r.Context(), s.pool, projID, draft.Title, specContent, []byte("[]"))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		saved := "saved"
		db.UpdateSpecDraft(r.Context(), s.pool, id, db.UpdateSpecDraftParams{Status: &saved})
		jsonOK(w, map[string]int64{"spec_id": sp.ID}, http.StatusCreated)

	case suffix == "" && r.Method == http.MethodDelete:
		draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if draft.CerberusSession != "" {
			if err := s.cerb.Close(r.Context(), draft.CerberusSession); err != nil {
				log.Printf("spec-builder close on delete: %v", err)
			}
			if err := s.cerb.Clean(r.Context(), draft.CerberusSession); err != nil {
				log.Printf("spec-builder clean on delete: %v", err)
			}
		}
		if err := db.DeleteSpecDraft(r.Context(), s.pool, id); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		jsonErr(w, "not found", http.StatusNotFound)
	}
}

func extractFinalSpec(messages []byte) string {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	var msgs []msg
	if err := json.Unmarshal(messages, &msgs); err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "assistant" {
			continue
		}
		content := msgs[i].Content
		idx := strings.Index(content, "FINAL SPEC:")
		if idx == -1 {
			continue
		}
		after := content[idx+len("FINAL SPEC:"):]
		start := strings.Index(after, "```")
		if start == -1 {
			return strings.TrimSpace(after)
		}
		after = after[start+3:]
		if strings.HasPrefix(after, "markdown") {
			after = after[8:]
		}
		end := strings.Index(after, "```")
		if end == -1 {
			return strings.TrimSpace(after)
		}
		return strings.TrimSpace(after[:end])
	}
	return ""
}

func appendMessage(existing []byte, role, content string) []byte {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
		Ts      string `json:"ts"`
	}
	var msgs []msg
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &msgs)
	}
	msgs = append(msgs, msg{Role: role, Content: content, Ts: time.Now().Format(time.RFC3339)})
	b, _ := json.Marshal(msgs)
	return b
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
		w.Header().Set("Content-Type", "application/x-yaml")
		w.Write(data)
	case http.MethodPatch:
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		// read current yaml as text, update matching key: value lines
		data, err := os.ReadFile(s.cfgPath)
		if err != nil {
			jsonErr(w, "cannot read config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		updated := applyYAMLPatch(string(data), body)
		if err := os.WriteFile(s.cfgPath, []byte(updated), 0644); err != nil {
			jsonErr(w, "cannot write config: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-yaml")
		w.Write([]byte(updated))
	default:
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// applyYAMLPatch does a naive line-by-line replacement of top-level YAML keys.
func applyYAMLPatch(yaml string, patch map[string]any) string {
	lines := strings.Split(yaml, "\n")
	replaced := map[string]bool{}
	for i, line := range lines {
		for k, v := range patch {
			prefix := k + ":"
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				lines[i] = fmt.Sprintf("%s: %s", k, yamlValue(v))
				replaced[k] = true
			}
		}
	}
	for k, v := range patch {
		if !replaced[k] {
			lines = append(lines, fmt.Sprintf("%s: %s", k, yamlValue(v)))
		}
	}
	return strings.Join(lines, "\n")
}

// yamlValue formats a value for a YAML line.
// Strings get quoted; numbers and booleans are written bare.
func yamlValue(v any) string {
	s := fmt.Sprint(v)
	// if it parses as a number or bool, write bare
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	if _, err := strconv.ParseBool(s); err == nil {
		return s
	}
	return fmt.Sprintf("%q", s)
}

func RecoverOrphanDrafts(ctx context.Context, pool *pgxpool.Pool, cerb *cerberus.Client) {
	drafts, err := db.ListSpecDrafts(ctx, pool)
	if err != nil {
		log.Printf("orphan recovery: list drafts: %v", err)
		return
	}
	errStatus := "error"
	for _, d := range drafts {
		if d.Status != "active" {
			continue
		}
		if d.CerberusSession == "" {
			db.UpdateSpecDraft(ctx, pool, d.ID, db.UpdateSpecDraftParams{Status: &errStatus})
			continue
		}
		status, err := cerb.Status(ctx, d.CerberusSession)
		if err != nil || strings.Contains(status, "not found") || strings.Contains(status, "done") || strings.Contains(status, "failed") {
			log.Printf("orphan recovery: marking draft %d as error (status=%q err=%v)", d.ID, status, err)
			db.UpdateSpecDraft(ctx, pool, d.ID, db.UpdateSpecDraftParams{Status: &errStatus})
		}
	}
}
