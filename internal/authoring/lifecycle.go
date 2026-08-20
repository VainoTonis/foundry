package authoring

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
	planspec "github.com/tonis2/foundry/internal/spec"
)

// CreateDraftAndStartChat creates a spec draft and begins a chat session with cerberus.
// It runs the chat asynchronously and returns the draft immediately.
func (svc *Service) CreateDraftAndStartChat(ctx context.Context, params CreateDraftAndStartChatParams) (*db.SpecDraft, error) {
	if params.RepositoryID == nil {
		return nil, fmt.Errorf("repository_id is required")
	}
	repo, err := db.GetRepository(ctx, svc.pool, *params.RepositoryID)
	if err != nil {
		return nil, fmt.Errorf("get repository: %w", err)
	}

	repoLocalPath, err := requireLocalPath(repo)
	if err != nil {
		return nil, err
	}

	draft, err := db.CreateSpecDraft(ctx, svc.pool, params.RepositoryID, "(untitled)")
	if err != nil {
		return nil, fmt.Errorf("create draft: %w", err)
	}

	session := draftSessionName(draft.ID)
	if _, err := db.UpdateSpecDraft(ctx, svc.pool, draft.ID, db.UpdateSpecDraftParams{CerberusSession: &session}); err != nil {
		return nil, fmt.Errorf("update draft session: %w", err)
	}
	draft.CerberusSession = session

	initialPrompt := params.SpecBuilderPrompt
	if params.Description != "" {
		initialPrompt += "\n\nThe user's request:\n" + params.Description
	}
	initialPrompt += "\n\nRepository name: " + repo.Name + "\nThe selected repository is mounted at /workspace inside your container."

	go svc.runChat(context.Background(), draft.ID, session, initialPrompt, repoLocalPath)

	return &draft, nil
}

// requireLocalPath returns repo's local worktree path, or a wrapped
// repository.ErrNoLocalPath if repo has no local path configured (for
// example, a remote-only repository selected before it has been
// cloned/mounted anywhere). Selecting a remote-only repository is a
// valid, safe choice; it simply cannot be used for authoring flows that
// require a local mount, so this is reported back to the caller (as a
// wrapped, errors.Is-classifiable error) rather than causing a panic or
// an unclear downstream failure.
func requireLocalPath(repo repository.Repository) (string, error) {
	return repo.RequireLocalPath()
}

func (svc *Service) runChat(ctx context.Context, draftID int64, session, prompt, repoLocalPath string) {
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

	svc.cerb.SetRepoPath(repoLocalPath)
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

	if draft.RepositoryID == nil {
		return nil, fmt.Errorf("draft has no repository")
	}

	repo, err := db.GetRepository(ctx, svc.pool, *draft.RepositoryID)
	if err != nil {
		return nil, fmt.Errorf("get repository: %w", err)
	}

	repoLocalPath, err := requireLocalPath(repo)
	if err != nil {
		return nil, err
	}

	go svc.sendMessage(context.Background(), params.DraftID, draft.CerberusSession, params.Content, repoLocalPath)

	return &draft, nil
}

func (svc *Service) sendMessage(ctx context.Context, draftID int64, session, content, repoLocalPath string) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	svc.cerb.SetRepoPath(repoLocalPath)
	if err := svc.cerb.Message(ctx, session, content, svc.callbackURL); err != nil {
		log.Printf("spec-builder message error: %v", err)
		errStatus := "error"
		if _, updateErr := db.UpdateSpecDraft(ctx, svc.pool, draftID, db.UpdateSpecDraftParams{Status: &errStatus}); updateErr != nil {
			log.Printf("spec-builder: mark draft %d error: %v", draftID, updateErr)
		}
	}
}

// SaveDraft extracts the final specification from a draft and persists it as a canonical plan.
func (svc *Service) SaveDraft(ctx context.Context, params SaveDraftParams) (int64, error) {
	draft, err := db.GetSpecDraft(ctx, svc.pool, params.DraftID)
	if err != nil {
		return 0, fmt.Errorf("get draft: %w", err)
	}

	specContent := ExtractFinalSpec(draft.Messages)
	if specContent == "" {
		return 0, fmt.Errorf("could not extract spec from conversation — ask the agent to update the spec with full spec content")
	}

	var repoID int64
	if draft.RepositoryID != nil {
		repoID = *draft.RepositoryID
		repo, err := db.GetRepository(ctx, svc.pool, repoID)
		if err != nil {
			return 0, fmt.Errorf("get repository: %w", err)
		}
		if repo.LocalPath != nil {
			svc.cerb.SetRepoPath(*repo.LocalPath)
		}
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

	parsed := planspec.Parse(specContent)
	plan, err := db.CreatePlan(ctx, svc.pool, repoID, title, parsed.GlobalContext, specContent)
	if err != nil {
		return 0, fmt.Errorf("create plan: %w", err)
	}
	for _, phase := range parsed.Phases {
		text := phase.Goal
		if phase.Name != "" {
			text = phase.Name + "\n\n" + text
		}
		if _, err := db.CreatePlanStep(ctx, svc.pool, plan.ID, phase.Position, text, nil); err != nil {
			return 0, fmt.Errorf("create plan step: %w", err)
		}
	}

	frozen := db.SpecDraftStatusFrozen
	if _, err := db.UpdateSpecDraft(ctx, svc.pool, params.DraftID, db.UpdateSpecDraftParams{Status: &frozen, Title: &title}); err != nil {
		return 0, fmt.Errorf("mark draft frozen: %w", err)
	}

	return plan.ID, nil
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
		if draft.RepositoryID != nil {
			if repo, err := db.GetRepository(ctx, svc.pool, *draft.RepositoryID); err == nil && repo.LocalPath != nil {
				svc.cerb.SetRepoPath(*repo.LocalPath)
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

func draftSessionName(draftID int64) string {
	return fmt.Sprintf("foundry-draft-%d", draftID)
}
