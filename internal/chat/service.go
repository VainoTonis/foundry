package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
)

var ErrProfileNotFound = errors.New("profile not found")

type Service struct {
	pool           *pgxpool.Pool
	cerb           *cerberus.Client
	callbackURL    string
	runtimeProfile func() string
}

func NewService(pool *pgxpool.Pool, cerb *cerberus.Client, callbackURL string, runtimeProfile func() string) *Service {
	return &Service{pool: pool, cerb: cerb, callbackURL: callbackURL, runtimeProfile: runtimeProfile}
}

// CreateSession creates a new chat session row. No cerberus call yet — session starts lazily on first message.
func (s *Service) CreateSession(ctx context.Context, profileName string) (db.ChatSession, error) {
	profileName = strings.TrimSpace(profileName)
	if err := s.validateProfile(ctx, profileName); err != nil {
		return db.ChatSession{}, err
	}
	// Use a temp session name; we'll rename after we know the ID.
	// Insert with placeholder and update immediately.
	sess, err := db.CreateChatSession(ctx, s.pool, fmt.Sprintf("foundry-chat-tmp-%d", time.Now().UnixNano()), profileName)
	if err != nil {
		return db.ChatSession{}, fmt.Errorf("create chat session: %w", err)
	}
	realSession := fmt.Sprintf("foundry-chat-%d", sess.ID)
	if err := db.UpdateChatSessionTitle(ctx, s.pool, sess.ID, ""); err != nil {
		return db.ChatSession{}, fmt.Errorf("fix session name: %w", err)
	}
	// Update cerberus_session to the stable name.
	_, err = s.pool.Exec(ctx,
		`UPDATE chat_sessions SET cerberus_session = $1, updated_at = NOW() WHERE id = $2`,
		realSession, sess.ID,
	)
	if err != nil {
		return db.ChatSession{}, fmt.Errorf("update cerberus session name: %w", err)
	}
	sess.CerberusSession = realSession
	return sess, nil
}

func (s *Service) UpdateSessionProfile(ctx context.Context, id int64, profileName string) error {
	profileName = strings.TrimSpace(profileName)
	if _, err := db.GetChatSession(ctx, s.pool, id); err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if err := s.validateProfile(ctx, profileName); err != nil {
		return err
	}
	return db.UpdateChatSessionProfileName(ctx, s.pool, id, profileName)
}

// GetSession returns the session by ID.
func (s *Service) GetSession(ctx context.Context, id int64) (db.ChatSession, error) {
	return db.GetChatSession(ctx, s.pool, id)
}

// ListSessions returns all sessions ordered by most recently updated.
func (s *Service) ListSessions(ctx context.Context) ([]db.ChatSession, error) {
	return db.ListChatSessions(ctx, s.pool)
}

// ListMessages returns all messages for a session.
func (s *Service) ListMessages(ctx context.Context, sessionID int64) ([]db.ChatMessage, error) {
	return db.ListChatMessages(ctx, s.pool, sessionID)
}

// SendMessage persists the user message and launches a cerberus turn in the background.
func (s *Service) SendMessage(ctx context.Context, sessionID int64, content string) error {
	sess, err := db.GetChatSession(ctx, s.pool, sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	// Set title from first user message if not yet set.
	if sess.Title == "" {
		title := content
		if len(title) > 60 {
			title = strings.TrimSpace(title[:60])
		}
		_ = db.UpdateChatSessionTitle(ctx, s.pool, sessionID, title)
	}

	if _, err := db.InsertChatMessage(ctx, s.pool, sessionID, "user", content); err != nil {
		return fmt.Errorf("insert user message: %w", err)
	}

	go s.sendTurn(sess, content)
	return nil
}

// AssembleMessages assembles text_delta cerberus events into a chat_messages row.
// Called from httpserver on turn_complete.
func (s *Service) AssembleMessages(ctx context.Context, cerberusSession string) {
	sess, err := db.GetChatSessionByCerberusSession(ctx, s.pool, cerberusSession)
	if err != nil {
		return
	}

	events, err := db.ListCerberusEvents(ctx, s.pool, cerberusSession, 0)
	if err != nil {
		log.Printf("chat assemble messages: list events: %v", err)
		return
	}

	var buf strings.Builder
	for _, e := range events {
		if e.EventType == "text_delta" {
			var p struct {
				Content string `json:"content"`
			}
			if err := json.Unmarshal(e.Payload, &p); err == nil && p.Content != "" {
				buf.WriteString(p.Content)
			}
		} else if e.EventType == "message_end" || e.EventType == "turn_complete" {
			if buf.Len() > 0 {
				content := buf.String()
				buf.Reset()
				if _, err := db.InsertChatMessage(ctx, s.pool, sess.ID, "assistant", content); err != nil {
					log.Printf("chat assemble messages: insert: %v", err)
				}
			}
		}
	}
	// Flush any remaining content (turn ended without explicit message_end).
	if buf.Len() > 0 {
		if _, err := db.InsertChatMessage(ctx, s.pool, sess.ID, "assistant", buf.String()); err != nil {
			log.Printf("chat assemble messages: insert trailing: %v", err)
		}
	}

	db.DeleteCerberusEvents(ctx, s.pool, cerberusSession)
}

// DeleteSession cleans up the cerberus session and removes DB rows.
func (s *Service) DeleteSession(ctx context.Context, id int64) error {
	sess, err := db.GetChatSession(ctx, s.pool, id)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	// Best-effort cerberus clean — session may already be gone.
	_ = s.cerb.Clean(ctx, sess.CerberusSession)
	_ = db.DeleteCerberusEvents(ctx, s.pool, sess.CerberusSession)
	_ = os.Remove(profileFilePath(sess.CerberusSession))
	return db.DeleteChatSession(ctx, s.pool, id)
}

func (s *Service) sendTurn(sess db.ChatSession, content string) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	input := cerberus.TurnInput{
		Name:        sess.CerberusSession,
		NoRepo:      true,
		UUID:        sess.CerberusUUID,
		Message:     content,
		CallbackURL: s.callbackURL,
	}
	if profilePath, err := s.writeProfileFile(ctx, effectiveProfileName(sess.ProfileName, s.runtimeProfileName()), sess.CerberusSession); err != nil {
		log.Printf("chat turn %d: write profile file: %v (proceeding without profile)", sess.ID, err)
	} else if profilePath != "" {
		input.ProfileFile = profilePath
	}

	out, err := s.cerb.Turn(ctx, input)
	if err != nil {
		log.Printf("chat turn %d: %v", sess.ID, err)
		_ = db.UpdateChatSessionStatus(ctx, s.pool, sess.ID, "error")
		return
	}

	// Session not found — replay history and retry with a fresh session.
	if out.Status == "error" && out.Error == cerberus.ErrSessionNotFound {
		msgs, dbErr := db.ListChatMessages(ctx, s.pool, sess.ID)
		if dbErr != nil {
			log.Printf("chat turn %d: load history: %v", sess.ID, dbErr)
			_ = db.UpdateChatSessionStatus(ctx, s.pool, sess.ID, "error")
			return
		}
		input.UUID = ""
		input.History = buildHistory(msgs)
		out, err = s.cerb.Turn(ctx, input)
		if err != nil {
			log.Printf("chat turn %d (recovery): %v", sess.ID, err)
			_ = db.UpdateChatSessionStatus(ctx, s.pool, sess.ID, "error")
			return
		}
	}

	if out.Status == "error" {
		log.Printf("chat turn %d: cerberus error: %s", sess.ID, out.Error)
		_ = db.UpdateChatSessionStatus(ctx, s.pool, sess.ID, "error")
		return
	}

	if out.UUID != "" && out.UUID != sess.CerberusUUID {
		_ = db.UpdateChatSessionUUID(ctx, s.pool, sess.ID, out.UUID)
	}
}

func effectiveProfileName(sessionProfile, runtimeProfile string) string {
	if strings.TrimSpace(sessionProfile) != "" {
		return strings.TrimSpace(sessionProfile)
	}
	return strings.TrimSpace(runtimeProfile)
}

func (s *Service) runtimeProfileName() string {
	if s.runtimeProfile == nil {
		return ""
	}
	return s.runtimeProfile()
}

func (s *Service) validateProfile(ctx context.Context, profileName string) error {
	if profileName == "" {
		return nil
	}
	if _, err := db.GetProfileByName(ctx, s.pool, profileName); err != nil {
		if err == db.ErrNotFound {
			return fmt.Errorf("profile %q: %w", profileName, ErrProfileNotFound)
		}
		return fmt.Errorf("lookup profile %q: %w", profileName, err)
	}
	return nil
}

func (s *Service) writeProfileFile(ctx context.Context, profileName, session string) (string, error) {
	if profileName == "" {
		return "", nil
	}
	p, err := db.GetProfileByName(ctx, s.pool, profileName)
	if err == db.ErrNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup profile %q: %w", profileName, err)
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

func profileFilePath(session string) string {
	return "/tmp/foundry-profile-" + session + ".json"
}

// buildHistory converts DB chat messages to cerberus TurnMessage history.
// IDs are stringified DB IDs; parent is the previous message.
func buildHistory(msgs []db.ChatMessage) []cerberus.TurnMessage {
	out := make([]cerberus.TurnMessage, len(msgs))
	for i, m := range msgs {
		tm := cerberus.TurnMessage{
			ID:      fmt.Sprintf("%d", m.ID),
			Role:    m.Role,
			Content: m.Content,
		}
		if i > 0 {
			tm.ParentID = fmt.Sprintf("%d", msgs[i-1].ID)
		}
		out[i] = tm
	}
	return out
}
