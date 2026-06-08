package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/memory"
)

// ---- spec-draft prompt ----

const specBuilderPrompt = `You are Draft Studio for Foundry, a spec-driven development loop that runs AI agents.

Your job: run an exploratory PoC/refinement lane before the user commits to a final Foundry spec. Help the user discover intent, constraints, risks, and phase boundaries. Do not rush to a saved spec; converge toward one through visible thinking and explicit decisions.

## Draft Studio conversation format

In normal chat replies, keep the work visibly organized with these sections:

### Intent
What the user appears to want, including durable product intent and constraints discovered so far.

### Current thinking
Your working interpretation, open assumptions, possible approaches, risks, and tradeoffs. Keep this exploratory and easy to correct.

### Latest preview
A concise draft preview of the likely spec shape or PoC plan. This can be partial and non-executable while still exploring. Do not present this as saved unless you call update_spec.

### Next decision
The single most useful question or choice needed to move forward.

Be concise, collaborative, and iterative. Ask for missing information when needed. If the user is still exploring, keep the preview lightweight rather than forcing a full executable spec.

## Intent context

Before drafting or materially updating a save-ready spec preview, read the key intent files in the project's memory namespace when they exist. Use file path references only; do not inline wiki contents into the prompt or generated spec.

Default intent files to inspect under the configured project memory namespace:
- intent/README.md
- intent/Product Model.md
- intent/Principles.md
- intent/Constraints.md
- intent/Open Questions.md
- relevant linked pages under intent/ when the request or those files point to them

Generated specs should link back to durable intent using Obsidian-style links where relevant, for example:

Related intent: [[Product Model]], [[Principles]], [[Constraints]]

Choose only relevant intent links. Do not invent pages unless the spec truly introduces a durable concept that belongs in intent. If intent files are missing, continue without failing and do not paste placeholder wiki content.

## Save-ready spec format

A saved Foundry spec is markdown with this structure:

# Feature title

Related intent: [[Product Model]], [[Principles]], [[Constraints]]

Global context — background, constraints, anything the agent needs to know.
This is prepended to every phase prompt automatically.

## Phase 1: Name
What this phase should accomplish. This becomes the exact prompt body sent to the agent.
Be specific: what files to create/edit, what the output should be, and how to verify it works.

## Phase 2: Name
...

Rules for executable phases:
- Sections starting with ## Phase N: become executable phases (N must be sequential integers starting at 1)
- Everything before the first phase = global context (shared across all phases)
- Each phase goal should be independently executable by an AI agent in a fresh container
- Phases should be small enough that one agent can complete them in a single session
- Prefer explicit over clever — spell out what files to touch, what functions to write

## Good save-ready example

# User authentication

Stack: Go + pgx + stdlib net/http. No frameworks, no ORMs.
Project already has: users table (id, email, password_hash, created_at).

## Phase 1: Password hashing utilities
Create internal/auth/hash.go with HashPassword(plain string) (string, error) using bcrypt cost 12, and CheckPassword(plain, hash string) bool. Add internal/auth/hash_test.go covering both. No external deps beyond golang.org/x/crypto.

## Phase 2: Login endpoint
Add POST /api/login to internal/api/handlers.go. Accept {email, password} JSON. Return {token} on success, 401 on failure.

## Phase 3: Auth middleware
Add AuthMiddleware(next http.Handler) http.Handler in internal/api/middleware.go. Reads Authorization: Bearer <token>, validates JWT, sets user_id in context.

Use the update_spec tool only when you have a save-ready executable preview: a complete markdown spec with sequential ## Phase N: sections that an agent can run. When you call update_spec, pass the full markdown spec content. Do not call update_spec for exploratory notes, partial previews, unresolved options, or ordinary conversational refinements. Until the preview is save-ready, keep it in the visible Latest preview section instead of using the tool.`

// ---- spec-draft handlers ----

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
		_, memoryRepoPath, _ := s.runtimeSettings()
		if strings.TrimSpace(memoryRepoPath) == "" {
			jsonErr(w, "memory repo path is not configured", http.StatusUnprocessableEntity)
			return
		}
		var body struct {
			ProjectID   *int64 `json:"project_id"`
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.ProjectID == nil {
			jsonErr(w, "project_id is required", http.StatusUnprocessableEntity)
			return
		}
		proj, err := db.GetProject(r.Context(), s.pool, *body.ProjectID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "project not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		projectRepoPath := strings.TrimSpace(proj.RepoPath)
		if projectRepoPath == "" {
			jsonErr(w, "project repo path is not configured", http.StatusUnprocessableEntity)
			return
		}
		draft, err := db.CreateSpecDraft(r.Context(), s.pool, body.ProjectID, "(untitled)")
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
		if body.Description != "" {
			initialPrompt += "\n\nThe user's request:\n" + body.Description
		}
		initialPrompt += "\n\nProject name: " + proj.Name + "\nThe selected project's repository is mounted at /workspace inside your container. Use project memory namespace " + proj.MemoryNamespace + "."
		if mem, err := memory.LoadApproved(memoryRepoPath, proj.MemoryNamespace, nil); err == nil {
			initialPrompt = memory.Prepend(mem.Markdown, initialPrompt)
		} else {
			log.Printf("spec-builder draft %d: load memory: %v", draft.ID, err)
		}

		pool := s.pool
		cerb := s.cerb
		draftID := draft.ID
		cbURL := s.callbackURL()
		cerberusRepoPath := projectRepoPath
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			profilePath, profileErr := s.writeProfileFile(ctx, session)
			if profileErr != nil {
				log.Printf("spec-builder: write profile file: %v (proceeding without profile)", profileErr)
			}
			if profilePath != "" {
				cerb.SetProfile(profilePath)
			}
			cerb.SetRepoPath(cerberusRepoPath)
			if err := cerb.Chat(ctx, session, initialPrompt, cbURL); err != nil {
				log.Printf("spec-builder chat start error: %v", err)
				errStatus := "error"
				if _, updateErr := db.UpdateSpecDraft(ctx, pool, draftID, db.UpdateSpecDraftParams{Status: &errStatus}); updateErr != nil {
					log.Printf("spec-builder: mark draft %d error: %v", draftID, updateErr)
				}
				return
			}
		}()

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
		draft, err := db.GetSpecDraft(r.Context(), s.pool, id)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(draft.Messages)

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
		draft, err = db.UpdateSpecDraft(r.Context(), s.pool, id, db.UpdateSpecDraftParams{Messages: msgs})
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if draft.ProjectID == nil {
			jsonErr(w, "draft has no project", http.StatusUnprocessableEntity)
			return
		}
		proj, err := db.GetProject(r.Context(), s.pool, *draft.ProjectID)
		if errors.Is(err, db.ErrNotFound) {
			jsonErr(w, "project not found", http.StatusNotFound)
			return
		}
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		projectRepoPath := strings.TrimSpace(proj.RepoPath)
		if projectRepoPath == "" {
			jsonErr(w, "project repo path is not configured", http.StatusUnprocessableEntity)
			return
		}
		cbURL := s.callbackURL()
		cerb := s.cerb
		session := draft.CerberusSession
		pool := s.pool
		draftID := draft.ID
		cerberusRepoPath := projectRepoPath
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			cerb.SetRepoPath(cerberusRepoPath)
			if err := cerb.Message(ctx, session, body.Content, cbURL); err != nil {
				log.Printf("spec-builder message error: %v", err)
				errStatus := "error"
				if _, updateErr := db.UpdateSpecDraft(ctx, pool, draftID, db.UpdateSpecDraftParams{Status: &errStatus}); updateErr != nil {
					log.Printf("spec-builder: mark draft %d error: %v", draftID, updateErr)
				}
			}
		}()
		jsonOK(w, draft, http.StatusOK)

	case suffix == "save" && r.Method == http.MethodPost:
		var saveBody struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&saveBody); err != nil {
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
		specContent := extractFinalSpec(draft.Messages)
		if specContent == "" {
			jsonErr(w, "could not extract spec from conversation — ask the agent to update the spec with full spec content", http.StatusUnprocessableEntity)
			return
		}
		var projID int64
		var proj *db.Project
		if draft.ProjectID != nil {
			projID = *draft.ProjectID
			p, err := db.GetProject(r.Context(), s.pool, projID)
			if err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
			proj = &p
			s.cerb.SetRepoPath(p.RepoPath)
		}
		if err := s.cerb.Close(r.Context(), draft.CerberusSession); err != nil {
			log.Printf("spec-builder close error: %v", err)
		}
		if err := s.cerb.Clean(r.Context(), draft.CerberusSession); err != nil {
			log.Printf("spec-builder clean error: %v", err)
		}
		db.DeleteCerberusEvents(r.Context(), s.pool, draft.CerberusSession)
		removeProfileFile(draft.CerberusSession)
		title := saveBody.Title
		if title == "" {
			title = extractSpecTitle(specContent)
		}
		if title == "" {
			title = draft.Title
		}
		if proj != nil {
			_, memoryRepoPath, _ := s.runtimeSettings()
			if _, err := writeSpecMarkdownToMemory(memoryRepoPath, proj.MemoryNamespace, draft.ID, title, specContent); err != nil {
				jsonErr(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		sp, err := db.CreateSpec(r.Context(), s.pool, projID, title, specContent, []byte("[]"))
		if err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		frozen := db.SpecDraftStatusFrozen
		if _, err := db.UpdateSpecDraft(r.Context(), s.pool, id, db.UpdateSpecDraftParams{Status: &frozen, Title: &title}); err != nil {
			jsonErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
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
			if draft.ProjectID != nil {
				if proj, err := db.GetProject(r.Context(), s.pool, *draft.ProjectID); err == nil {
					s.cerb.SetRepoPath(proj.RepoPath)
				}
			}
			if err := s.cerb.Close(r.Context(), draft.CerberusSession); err != nil {
				log.Printf("spec-builder close on delete: %v", err)
			}
			if err := s.cerb.Clean(r.Context(), draft.CerberusSession); err != nil {
				log.Printf("spec-builder clean on delete: %v", err)
			}
			db.DeleteCerberusEvents(r.Context(), s.pool, draft.CerberusSession)
			removeProfileFile(draft.CerberusSession)
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

// ---- spec extraction helpers ----

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
		if spec := extractSaveReadyMarkdownSpec(msgs[i].Content); spec != "" {
			return spec
		}
	}
	return ""
}

func extractSaveReadyMarkdownSpec(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if isSaveReadySpec(content) {
		return content
	}
	if spec := extractSpecFromMarkdownFence(content); spec != "" {
		return spec
	}
	if idx := strings.Index(content, "FINAL SPEC:"); idx != -1 {
		after := content[idx+len("FINAL SPEC:"):]
		if spec := extractSpecFromMarkdownFence(after); spec != "" {
			return spec
		}
		if spec := extractSpecFromTitle(after); spec != "" {
			return spec
		}
		return strings.TrimSpace(after)
	}
	return extractSpecFromTitle(content)
}

func extractSpecFromMarkdownFence(content string) string {
	remaining := content
	for {
		start := strings.Index(remaining, "```")
		if start == -1 {
			return ""
		}
		afterTicks := remaining[start+3:]
		lineEnd := strings.IndexByte(afterTicks, '\n')
		if lineEnd == -1 {
			return ""
		}
		info := strings.TrimSpace(afterTicks[:lineEnd])
		body := afterTicks[lineEnd+1:]
		end := strings.Index(body, "```")
		if end == -1 {
			return ""
		}
		if info == "" || strings.EqualFold(info, "markdown") || strings.EqualFold(info, "md") {
			candidate := strings.TrimSpace(body[:end])
			if isSaveReadySpec(candidate) {
				return candidate
			}
		}
		remaining = body[end+3:]
	}
}

func extractSpecFromTitle(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			candidate := strings.TrimSpace(strings.Join(lines[i:], "\n"))
			if isSaveReadySpec(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func isSaveReadySpec(content string) bool {
	content = strings.TrimSpace(content)
	return strings.HasPrefix(content, "# ") && strings.Contains(content, "\n## Phase 1:")
}

func extractSpecTitle(specContent string) string {
	for _, line := range strings.Split(specContent, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "## ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}

// ---- spec memory writing ----

func writeSpecMarkdownToMemory(repoPath, namespace string, draftID int64, title, content string) (string, error) {
	repoPath = strings.TrimSpace(repoPath)
	namespace = strings.Trim(strings.TrimSpace(namespace), string(os.PathSeparator)+"/")
	if repoPath == "" {
		return "", fmt.Errorf("memory repo path is not configured")
	}
	if namespace == "" {
		return "", fmt.Errorf("project memory namespace is not configured")
	}

	repoRoot := filepath.Clean(repoPath)
	specDir := filepath.Clean(filepath.Join(repoRoot, filepath.FromSlash(namespace), "specs"))
	if rel, err := filepath.Rel(repoRoot, specDir); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid memory namespace %q", namespace)
	}
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		return "", fmt.Errorf("create memory specs dir: %w", err)
	}

	base := fmt.Sprintf("draft-%d-%s", draftID, slugifySpecFilename(title))
	path := filepath.Join(specDir, base+".md")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write memory spec: %w", err)
	}
	return path, nil
}

func slugifySpecFilename(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "spec"
	}
	return out
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

// ---- cerberus draft event streaming ----

func (s *Server) assembleAndAppend(ctx context.Context, session string, isTurnComplete bool) {
	if !isTurnComplete {
		return
	}

	drafts, _ := db.ListSpecDrafts(ctx, s.pool)
	var draft *db.SpecDraft
	for _, d := range drafts {
		if d.CerberusSession == session {
			draft = &d
			break
		}
	}
	if draft == nil {
		return
	}

	events, err := db.ListCerberusEvents(ctx, s.pool, session, 0)
	if err != nil {
		log.Printf("assemble messages: %v", err)
		return
	}

	var buf strings.Builder
	var assistantMsgs []string
	for _, e := range events {
		switch e.EventType {
		case "text_delta":
			var p struct {
				Content string `json:"content"`
			}
			json.Unmarshal(e.Payload, &p)
			buf.WriteString(p.Content)
		case "message_end":
			if buf.Len() > 0 {
				assistantMsgs = append(assistantMsgs, buf.String())
				buf.Reset()
			}
		case "tool_use":
			var p struct {
				ToolName  string `json:"tool_name"`
				ToolInput string `json:"tool_input"`
			}
			json.Unmarshal(e.Payload, &p)
			if p.ToolName == "update_spec" {
				var toolInput struct {
					Content string `json:"content"`
				}
				if err := json.Unmarshal([]byte(p.ToolInput), &toolInput); err == nil {
					assistantMsgs = append(assistantMsgs, toolInput.Content)
				}
			}
		}
	}
	if buf.Len() > 0 {
		assistantMsgs = append(assistantMsgs, buf.String())
	}

	msgs := draft.Messages
	for _, content := range assistantMsgs {
		msgs = appendMessage(msgs, "assistant", content)
	}
	if len(assistantMsgs) > 0 {
		db.UpdateSpecDraft(ctx, s.pool, draft.ID, db.UpdateSpecDraftParams{Messages: msgs})
	}
	db.DeleteCerberusEvents(ctx, s.pool, session)
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

// ---- orphan draft recovery ----

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
			if _, updateErr := db.UpdateSpecDraft(ctx, pool, d.ID, db.UpdateSpecDraftParams{Status: &errStatus}); updateErr != nil {
				log.Printf("orphan recovery: mark draft %d error: %v", d.ID, updateErr)
			}
			continue
		}
		status, err := cerb.Status(ctx, d.CerberusSession)
		if err != nil || strings.Contains(status, "not found") || strings.Contains(status, "done") || strings.Contains(status, "failed") {
			log.Printf("orphan recovery: marking draft %d as error (status=%q err=%v)", d.ID, status, err)
			if _, updateErr := db.UpdateSpecDraft(ctx, pool, d.ID, db.UpdateSpecDraftParams{Status: &errStatus}); updateErr != nil {
				log.Printf("orphan recovery: mark draft %d error: %v", d.ID, updateErr)
			}
			continue
		}
		// session is alive (waiting) — leave it alone, user can resume from the UI
		if strings.Contains(status, "waiting") {
			log.Printf("orphan recovery: draft %d session %s is alive and waiting — keeping active", d.ID, d.CerberusSession)
		}
	}
}
