package chat

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
)

type cerberusClient interface {
	Turn(context.Context, cerberus.TurnInput) (cerberus.TurnOutput, error)
	Clean(context.Context, string) error
}

type store interface {
	CreateChatSession(context.Context, string, string) (db.ChatSession, error)
	UpdateChatSessionCerberusSession(context.Context, int64, string) error
	GetChatSession(context.Context, int64) (db.ChatSession, error)
	GetChatSessionByCerberusSession(context.Context, string) (db.ChatSession, error)
	ListChatSessions(context.Context) ([]db.ChatSession, error)
	UpdateChatSessionProfileName(context.Context, int64, string) error
	UpdateChatSessionUUID(context.Context, int64, string) error
	UpdateChatSessionStatus(context.Context, int64, string) error
	TouchChatSession(context.Context, int64) error
	MarkChatSessionStreaming(context.Context, int64) error
	MarkChatSessionActive(context.Context, int64) error
	MarkChatSessionSuspended(context.Context, int64) error
	ListIdleChatSessions(context.Context, time.Duration) ([]db.ChatSession, error)
	UpdateChatSessionTitle(context.Context, int64, string) error
	DeleteChatSession(context.Context, int64) error
	ClearChatSessionUUID(context.Context, int64) error
	InsertChatMessage(context.Context, int64, string, string) (db.ChatMessage, error)
	ListChatMessages(context.Context, int64) ([]db.ChatMessage, error)
	AttachProjectToSession(context.Context, int64, int64) error
	DetachProjectFromSession(context.Context, int64, int64) error
	ListSessionProjects(context.Context, int64) ([]db.Project, error)
	GetProfileByName(context.Context, string) (db.Profile, error)
	ListCerberusEvents(context.Context, string, int64) ([]db.CerberusEvent, error)
	DeleteCerberusEvents(context.Context, string) error
}

type pgStore struct {
	pool *pgxpool.Pool
}

func (s pgStore) CreateChatSession(ctx context.Context, cerberusSession, profileName string) (db.ChatSession, error) {
	return db.CreateChatSession(ctx, s.pool, cerberusSession, profileName)
}

func (s pgStore) UpdateChatSessionCerberusSession(ctx context.Context, id int64, cerberusSession string) error {
	_, err := s.pool.Exec(ctx, `UPDATE chat_sessions SET cerberus_session = $1, updated_at = NOW() WHERE id = $2`, cerberusSession, id)
	return err
}

func (s pgStore) GetChatSession(ctx context.Context, id int64) (db.ChatSession, error) {
	return db.GetChatSession(ctx, s.pool, id)
}

func (s pgStore) GetChatSessionByCerberusSession(ctx context.Context, cerberusSession string) (db.ChatSession, error) {
	return db.GetChatSessionByCerberusSession(ctx, s.pool, cerberusSession)
}

func (s pgStore) ListChatSessions(ctx context.Context) ([]db.ChatSession, error) {
	return db.ListChatSessions(ctx, s.pool)
}

func (s pgStore) UpdateChatSessionProfileName(ctx context.Context, id int64, profileName string) error {
	return db.UpdateChatSessionProfileName(ctx, s.pool, id, profileName)
}

func (s pgStore) UpdateChatSessionUUID(ctx context.Context, id int64, uuid string) error {
	return db.UpdateChatSessionUUID(ctx, s.pool, id, uuid)
}

func (s pgStore) UpdateChatSessionStatus(ctx context.Context, id int64, status string) error {
	return db.UpdateChatSessionStatus(ctx, s.pool, id, status)
}

func (s pgStore) TouchChatSession(ctx context.Context, id int64) error {
	return db.TouchChatSession(ctx, s.pool, id)
}

func (s pgStore) MarkChatSessionStreaming(ctx context.Context, id int64) error {
	return db.MarkChatSessionStreaming(ctx, s.pool, id)
}

func (s pgStore) MarkChatSessionActive(ctx context.Context, id int64) error {
	return db.MarkChatSessionActive(ctx, s.pool, id)
}

func (s pgStore) MarkChatSessionSuspended(ctx context.Context, id int64) error {
	return db.MarkChatSessionSuspended(ctx, s.pool, id)
}

func (s pgStore) ListIdleChatSessions(ctx context.Context, idleFor time.Duration) ([]db.ChatSession, error) {
	return db.ListIdleChatSessions(ctx, s.pool, idleFor)
}

func (s pgStore) UpdateChatSessionTitle(ctx context.Context, id int64, title string) error {
	return db.UpdateChatSessionTitle(ctx, s.pool, id, title)
}

func (s pgStore) DeleteChatSession(ctx context.Context, id int64) error {
	return db.DeleteChatSession(ctx, s.pool, id)
}

func (s pgStore) ClearChatSessionUUID(ctx context.Context, id int64) error {
	return db.ClearChatSessionUUID(ctx, s.pool, id)
}

func (s pgStore) InsertChatMessage(ctx context.Context, sessionID int64, role, content string) (db.ChatMessage, error) {
	return db.InsertChatMessage(ctx, s.pool, sessionID, role, content)
}

func (s pgStore) ListChatMessages(ctx context.Context, sessionID int64) ([]db.ChatMessage, error) {
	return db.ListChatMessages(ctx, s.pool, sessionID)
}

func (s pgStore) AttachProjectToSession(ctx context.Context, sessionID, projectID int64) error {
	return db.AttachProjectToSession(ctx, s.pool, sessionID, projectID)
}

func (s pgStore) DetachProjectFromSession(ctx context.Context, sessionID, projectID int64) error {
	return db.DetachProjectFromSession(ctx, s.pool, sessionID, projectID)
}

func (s pgStore) ListSessionProjects(ctx context.Context, sessionID int64) ([]db.Project, error) {
	return db.ListSessionProjects(ctx, s.pool, sessionID)
}

func (s pgStore) GetProfileByName(ctx context.Context, name string) (db.Profile, error) {
	return db.GetProfileByName(ctx, s.pool, name)
}

func (s pgStore) ListCerberusEvents(ctx context.Context, session string, afterID int64) ([]db.CerberusEvent, error) {
	return db.ListCerberusEvents(ctx, s.pool, session, afterID)
}

func (s pgStore) DeleteCerberusEvents(ctx context.Context, session string) error {
	db.DeleteCerberusEvents(ctx, s.pool, session)
	return nil
}
