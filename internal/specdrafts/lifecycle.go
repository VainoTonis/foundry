package specdrafts

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/memory"
)

// CreateDraftAndStartChat creates a spec draft and begins a chat session with cerberus.
// It runs the chat asynchronously and returns the draft immediately.
func (svc *Service) CreateDraftAndStartChat(ctx context.Context, params CreateDraftAndStartChatParams) (*db.SpecDraft, error) {
	if params.ProjectID == nil {
		return nil, fmt.Errorf("project_id is required")
	}
	if strings.TrimSpace(svc.memoryRepoPath) == "" {
		return nil, fmt.Errorf("memory repo path is not configured")
	}

	proj, err := db.GetProject(ctx, svc.pool, *params.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}

	projectRepoPath := strings.TrimSpace(proj.RepoPath)
	if projectRepoPath == "" {
		return nil, fmt.Errorf("project repo path is not configured")
	}

	draft, err := db.CreateSpecDraft(ctx, svc.pool, params.ProjectID, "(untitled)")
	if err != nil {
		return nil, fmt.Errorf("create draft: %w", err)
	}

	session := cerberus.DraftSessionName(draft.ID)
	if _, err := db.UpdateSpecDraft(ctx, svc.pool, draft.ID, db.UpdateSpecDraftParams{CerberusSession: &session}); err != nil {
		return nil, fmt.Errorf("update draft session: %w", err)
	}
	draft.CerberusSession = session

	initialPrompt := params.SpecBuilderPrompt
	if params.Description != "" {
		initialPrompt += "\n\nThe user's request:\n" + params.Description
	}
	initialPrompt += "\n\nProject name: " + proj.Name + "\nThe selected project's repository is mounted at /workspace inside your container. Use project memory namespace " + proj.MemoryNamespace + "."

	if mem, err := memory.LoadApproved(svc.memoryRepoPath, proj.MemoryNamespace, nil); err == nil && mem.Markdown != "" {
		initialPrompt = mem.Markdown + "\n\n" + initialPrompt
	} else if err != nil {
		log.Printf("spec-builder draft %d: load memory: %v", draft.ID, err)
	}

	go svc.runChat(context.Background(), draft.ID, session, initialPrompt, projectRepoPath)

	return &draft, nil
}

func (svc *Service) runChat(ctx context.Context, draftID int64, session, prompt, projectRepoPath string) {
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()

	if svc.writeProfileFile != nil {
		profilePath, profileErr := svc.writeProfileFile(ctx, session)
		if profileErr != nil {
			log.Printf("spec-builder: write profile file: %v (proceeding without profile)", profileErr)
		}
		if profilePath != "" {
			svc.cerb.SetProfile(profilePath)
		}
	}

	svc.cerb.SetRepoPath(projectRepoPath)
	if err := svc.cerb.Chat(ctx, session, prompt, svc.callbackURL); err != nil {
		log.Printf("spec-builder chat start error: %v", err)
		errStatus := "error"
		if _, updateErr := db.UpdateSpecDraft(ctx, svc.pool, draftID, db.UpdateSpecDraftParams{Status: &errStatus}); updateErr != nil {
			log.Printf("spec-builder: mark draft %d error: %v", draftID, updateErr)
		}
	}
}

// AppendUserMessage appends a user message to a draft and sends it to cerberus.
func (svc *Service) AppendUserMessage(ctx context.Context, params AppendUserMessageParams) (*db.SpecDraft, error) {
	draft, err := db.GetSpecDraft(ctx, svc.pool, params.DraftID)
	if err != nil {
		return nil, fmt.Errorf("get draft: %w", err)
	}

	messages := AppendMessage(draft.Messages, "user", params.Content)
	draft, err = db.UpdateSpecDraft(ctx, svc.pool, params.DraftID, db.UpdateSpecDraftParams{Messages: messages})
	if err != nil {
		return nil, fmt.Errorf("update draft messages: %w", err)
	}

	if draft.ProjectID == nil {
		return nil, fmt.Errorf("draft has no project")
	}

	proj, err := db.GetProject(ctx, svc.pool, *draft.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}

	projectRepoPath := strings.TrimSpace(proj.RepoPath)
	if projectRepoPath == "" {
		return nil, fmt.Errorf("project repo path is not configured")
	}

	go svc.sendMessage(context.Background(), params.DraftID, draft.CerberusSession, params.Content, projectRepoPath)

	return &draft, nil
}

func (svc *Service) sendMessage(ctx context.Context, draftID int64, session, content, projectRepoPath string) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	svc.cerb.SetRepoPath(projectRepoPath)
	if err := svc.cerb.Message(ctx, session, content, svc.callbackURL); err != nil {
		log.Printf("spec-builder message error: %v", err)
		errStatus := "error"
		if _, updateErr := db.UpdateSpecDraft(ctx, svc.pool, draftID, db.UpdateSpecDraftParams{Status: &errStatus}); updateErr != nil {
			log.Printf("spec-builder: mark draft %d error: %v", draftID, updateErr)
		}
	}
}

// SaveDraft extracts the final spec from a draft's messages and persists it.
func (svc *Service) SaveDraft(ctx context.Context, params SaveDraftParams) (int64, error) {
	draft, err := db.GetSpecDraft(ctx, svc.pool, params.DraftID)
	if err != nil {
		return 0, fmt.Errorf("get draft: %w", err)
	}

	specContent := ExtractFinalSpec(draft.Messages)
	if specContent == "" {
		return 0, fmt.Errorf("could not extract spec from conversation — ask the agent to update the spec with full spec content")
	}

	var projID int64
	var proj *db.Project
	if draft.ProjectID != nil {
		projID = *draft.ProjectID
		p, err := db.GetProject(ctx, svc.pool, projID)
		if err != nil {
			return 0, fmt.Errorf("get project: %w", err)
		}
		proj = &p
		svc.cerb.SetRepoPath(p.RepoPath)
	}

	if err := svc.cerb.Close(ctx, draft.CerberusSession); err != nil {
		log.Printf("spec-builder close error: %v", err)
	}

	if err := svc.cerb.Clean(ctx, draft.CerberusSession); err != nil {
		log.Printf("spec-builder clean error: %v", err)
	}
	db.DeleteCerberusEvents(ctx, svc.pool, draft.CerberusSession)

	if svc.removeProfileFile != nil {
		svc.removeProfileFile(draft.CerberusSession)
	}

	title := params.Title
	if title == "" {
		title = ExtractSpecTitle(specContent)
	}
	if title == "" {
		title = draft.Title
	}

	if proj != nil {
		if _, err := memory.WriteDraftSpecMarkdown(svc.memoryRepoPath, proj.MemoryNamespace, draft.ID, title, specContent); err != nil {
			return 0, fmt.Errorf("write spec to memory: %w", err)
		}
	}

	sp, err := db.CreateSpec(ctx, svc.pool, projID, title, specContent, []byte("[]"))
	if err != nil {
		return 0, fmt.Errorf("create spec: %w", err)
	}

	frozen := db.SpecDraftStatusFrozen
	if _, err := db.UpdateSpecDraft(ctx, svc.pool, params.DraftID, db.UpdateSpecDraftParams{Status: &frozen, Title: &title}); err != nil {
		return 0, fmt.Errorf("mark draft frozen: %w", err)
	}

	return sp.ID, nil
}

// GetDraft retrieves a single draft by ID.
func (svc *Service) GetDraft(ctx context.Context, draftID int64) (*db.SpecDraft, error) {
	draft, err := db.GetSpecDraft(ctx, svc.pool, draftID)
	if err != nil {
		return nil, err
	}
	return &draft, nil
}

// ListDrafts retrieves all spec drafts.
func (svc *Service) ListDrafts(ctx context.Context) ([]db.SpecDraft, error) {
	drafts, err := db.ListSpecDrafts(ctx, svc.pool)
	if err != nil {
		return nil, err
	}
	return drafts, nil
}

// GetDraftMessages retrieves the message history for a draft.
func (svc *Service) GetDraftMessages(ctx context.Context, draftID int64) ([]byte, error) {
	draft, err := db.GetSpecDraft(ctx, svc.pool, draftID)
	if err != nil {
		return nil, err
	}
	return draft.Messages, nil
}

// DeleteDraft cleans up a draft including its cerberus session.
func (svc *Service) DeleteDraft(ctx context.Context, draftID int64) error {
	draft, err := db.GetSpecDraft(ctx, svc.pool, draftID)
	if err != nil {
		return fmt.Errorf("get draft: %w", err)
	}

	if draft.CerberusSession != "" {
		if draft.ProjectID != nil {
			if proj, err := db.GetProject(ctx, svc.pool, *draft.ProjectID); err == nil {
				svc.cerb.SetRepoPath(proj.RepoPath)
			}
		}
		if err := svc.cerb.Close(ctx, draft.CerberusSession); err != nil {
			log.Printf("spec-builder close on delete: %v", err)
		}
		if err := svc.cerb.Clean(ctx, draft.CerberusSession); err != nil {
			log.Printf("spec-builder clean on delete: %v", err)
		}
		db.DeleteCerberusEvents(ctx, svc.pool, draft.CerberusSession)
		if svc.removeProfileFile != nil {
			svc.removeProfileFile(draft.CerberusSession)
		}
	}

	if err := db.DeleteSpecDraft(ctx, svc.pool, draftID); err != nil {
		return fmt.Errorf("delete draft: %w", err)
	}

	return nil
}
