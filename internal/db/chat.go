package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ChatSession struct {
	ID              int64     `json:"id"`
	Title           string    `json:"title"`
	CerberusSession string    `json:"cerberus_session"`
	CerberusUUID    string    `json:"cerberus_uuid"`
	ProfileName     string    `json:"profile_name"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ChatMessage struct {
	ID        int64     `json:"id"`
	SessionID int64     `json:"session_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func CreateChatSession(ctx context.Context, pool *pgxpool.Pool, cerberusSession, profileName string) (ChatSession, error) {
	var s ChatSession
	err := pool.QueryRow(ctx,
		`INSERT INTO chat_sessions (cerberus_session, profile_name) VALUES ($1, $2)
		 RETURNING id, title, cerberus_session, cerberus_uuid, profile_name, status, created_at, updated_at`,
		cerberusSession, profileName,
	).Scan(&s.ID, &s.Title, &s.CerberusSession, &s.CerberusUUID, &s.ProfileName, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func GetChatSession(ctx context.Context, pool *pgxpool.Pool, id int64) (ChatSession, error) {
	var s ChatSession
	err := pool.QueryRow(ctx,
		`SELECT id, title, cerberus_session, cerberus_uuid, profile_name, status, created_at, updated_at
		 FROM chat_sessions WHERE id = $1`,
		id,
	).Scan(&s.ID, &s.Title, &s.CerberusSession, &s.CerberusUUID, &s.ProfileName, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	if err == pgx.ErrNoRows {
		return ChatSession{}, ErrNotFound
	}
	return s, err
}

func GetChatSessionByCerberusSession(ctx context.Context, pool *pgxpool.Pool, cerberusSession string) (ChatSession, error) {
	var s ChatSession
	err := pool.QueryRow(ctx,
		`SELECT id, title, cerberus_session, cerberus_uuid, profile_name, status, created_at, updated_at
		 FROM chat_sessions WHERE cerberus_session = $1`,
		cerberusSession,
	).Scan(&s.ID, &s.Title, &s.CerberusSession, &s.CerberusUUID, &s.ProfileName, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	if err == pgx.ErrNoRows {
		return ChatSession{}, ErrNotFound
	}
	return s, err
}

func ListChatSessions(ctx context.Context, pool *pgxpool.Pool) ([]ChatSession, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, title, cerberus_session, cerberus_uuid, profile_name, status, created_at, updated_at
		 FROM chat_sessions ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatSession
	for rows.Next() {
		var s ChatSession
		if err := rows.Scan(&s.ID, &s.Title, &s.CerberusSession, &s.CerberusUUID, &s.ProfileName, &s.Status, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func UpdateChatSessionProfileName(ctx context.Context, pool *pgxpool.Pool, id int64, profileName string) error {
	tag, err := pool.Exec(ctx,
		`UPDATE chat_sessions SET profile_name = $1, updated_at = NOW() WHERE id = $2`,
		profileName, id,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func UpdateChatSessionUUID(ctx context.Context, pool *pgxpool.Pool, id int64, uuid string) error {
	_, err := pool.Exec(ctx,
		`UPDATE chat_sessions SET cerberus_uuid = $1, updated_at = NOW() WHERE id = $2`,
		uuid, id,
	)
	return err
}

func UpdateChatSessionStatus(ctx context.Context, pool *pgxpool.Pool, id int64, status string) error {
	_, err := pool.Exec(ctx,
		`UPDATE chat_sessions SET status = $1, updated_at = NOW() WHERE id = $2`,
		status, id,
	)
	return err
}

func UpdateChatSessionTitle(ctx context.Context, pool *pgxpool.Pool, id int64, title string) error {
	_, err := pool.Exec(ctx,
		`UPDATE chat_sessions SET title = $1, updated_at = NOW() WHERE id = $2`,
		title, id,
	)
	return err
}

func DeleteChatSession(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	_, err := pool.Exec(ctx, `DELETE FROM chat_sessions WHERE id = $1`, id)
	return err
}

func InsertChatMessage(ctx context.Context, pool *pgxpool.Pool, sessionID int64, role, content string) (ChatMessage, error) {
	var m ChatMessage
	err := pool.QueryRow(ctx,
		`INSERT INTO chat_messages (session_id, role, content)
		 VALUES ($1, $2, $3)
		 RETURNING id, session_id, role, content, created_at`,
		sessionID, role, content,
	).Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.CreatedAt)
	return m, err
}

func ListChatMessages(ctx context.Context, pool *pgxpool.Pool, sessionID int64) ([]ChatMessage, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, session_id, role, content, created_at
		 FROM chat_messages WHERE session_id = $1 ORDER BY id`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
