package specdrafts

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
)

// Service encapsulates draft authoring business logic and dependencies.
type Service struct {
	pool              *pgxpool.Pool
	cerb              *cerberus.Client
	callbackURL       string
	memoryRepoPath    string
	writeProfileFile  func(ctx context.Context, session string) (string, error)
	removeProfileFile func(session string)
}

// NewService creates a new Service with the required dependencies.
func NewService(pool *pgxpool.Pool, cerb *cerberus.Client, callbackURL, memoryRepoPath string, writeProfileFile func(ctx context.Context, session string) (string, error), removeProfileFile func(session string)) *Service {
	return &Service{
		pool:              pool,
		cerb:              cerb,
		callbackURL:       callbackURL,
		memoryRepoPath:    memoryRepoPath,
		writeProfileFile:  writeProfileFile,
		removeProfileFile: removeProfileFile,
	}
}

// CreateDraftAndStartChatParams holds parameters for CreateDraftAndStartChat.
type CreateDraftAndStartChatParams struct {
	ProjectID         *int64
	Description       string
	SpecBuilderPrompt string
}

// AppendUserMessageParams holds parameters for AppendUserMessage.
type AppendUserMessageParams struct {
	DraftID int64
	Content string
}

// SaveDraftParams holds parameters for SaveDraft.
type SaveDraftParams struct {
	DraftID int64
	Title   string
}
