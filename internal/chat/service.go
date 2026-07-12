package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
)

var (
	ErrProfileNotFound = errors.New("profile not found")
	ErrSessionBusy     = errors.New("chat session has an active turn")
)

type Service struct {
	store          store
	cerb           cerberusClient
	callbackURL    string
	runtimeProfile func() string
}

func NewService(pool *pgxpool.Pool, cerb *cerberus.Client, callbackURL string, runtimeProfile func() string) *Service {
	return newService(pgStore{pool: pool}, cerb, callbackURL, runtimeProfile)
}

func newService(store store, cerb cerberusClient, callbackURL string, runtimeProfile func() string) *Service {
	return &Service{store: store, cerb: cerb, callbackURL: callbackURL, runtimeProfile: runtimeProfile}
}

// CreateSession creates a new chat session row. No cerberus call yet — session starts lazily on first message.
func (s *Service) CreateSession(ctx context.Context, profileName string) (db.ChatSession, error) {
	profileName = strings.TrimSpace(profileName)
	if err := s.validateProfile(ctx, profileName); err != nil {
		return db.ChatSession{}, err
	}
	// Use a temp session name; we'll rename after we know the ID.
	// Insert with placeholder and update immediately.
	sess, err := s.store.CreateChatSession(ctx, fmt.Sprintf("foundry-chat-tmp-%d", time.Now().UnixNano()), profileName)
	if err != nil {
		return db.ChatSession{}, fmt.Errorf("create chat session: %w", err)
	}
	realSession := fmt.Sprintf("foundry-chat-%d", sess.ID)
	if err := s.store.UpdateChatSessionTitle(ctx, sess.ID, ""); err != nil {
		return db.ChatSession{}, fmt.Errorf("fix session name: %w", err)
	}
	if err := s.store.UpdateChatSessionCerberusSession(ctx, sess.ID, realSession); err != nil {
		return db.ChatSession{}, fmt.Errorf("update cerberus session name: %w", err)
	}
	sess.CerberusSession = realSession
	return sess, nil
}

func (s *Service) UpdateSessionProfile(ctx context.Context, id int64, profileName string) error {
	profileName = strings.TrimSpace(profileName)
	if _, err := s.store.GetChatSession(ctx, id); err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if err := s.validateProfile(ctx, profileName); err != nil {
		return err
	}
	return s.store.UpdateChatSessionProfileName(ctx, id, profileName)
}

// GetSession returns the session by ID.
func (s *Service) GetSession(ctx context.Context, id int64) (db.ChatSession, error) {
	return s.store.GetChatSession(ctx, id)
}

// ListSessions returns all sessions ordered by most recently updated.
func (s *Service) ListSessions(ctx context.Context) ([]db.ChatSession, error) {
	return s.store.ListChatSessions(ctx)
}

// ListMessages returns all messages for a session.
func (s *Service) ListMessages(ctx context.Context, sessionID int64) ([]db.ChatMessage, error) {
	return s.store.ListChatMessages(ctx, sessionID)
}

// SendMessage persists the user message and launches a cerberus turn in the background.
func (s *Service) SendMessage(ctx context.Context, sessionID int64, content string) error {
	return s.SendMessageWithProfile(ctx, sessionID, content, nil)
}

func (s *Service) SendMessageWithProfile(ctx context.Context, sessionID int64, content string, profileName *string) error {
	sess, err := s.store.GetChatSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sess.Status == "streaming" {
		return ErrSessionBusy
	}
	if profileName != nil {
		nextProfile := strings.TrimSpace(*profileName)
		if err := s.validateProfile(ctx, nextProfile); err != nil {
			return err
		}
		if nextProfile != sess.ProfileName {
			if err := s.store.UpdateChatSessionProfileName(ctx, sessionID, nextProfile); err != nil {
				return fmt.Errorf("update session profile: %w", err)
			}
			sess.ProfileName = nextProfile
		}
	}
	if err := s.store.MarkChatSessionStreaming(ctx, sessionID); err != nil {
		return fmt.Errorf("mark session streaming: %w", err)
	}

	// Set title from first user message if not yet set.
	if sess.Title == "" {
		title := content
		if len(title) > 60 {
			title = strings.TrimSpace(title[:60])
		}
		_ = s.store.UpdateChatSessionTitle(ctx, sessionID, title)
	}

	if _, err := s.store.InsertChatMessage(ctx, sessionID, "user", content); err != nil {
		_ = s.store.MarkChatSessionActive(ctx, sessionID)
		return fmt.Errorf("insert user message: %w", err)
	}

	go s.sendTurn(sess, content)
	return nil
}

// AssembleMessages assembles text_delta cerberus events into a chat_messages row.
// Called from httpserver on turn_complete.
func (s *Service) AssembleMessages(ctx context.Context, cerberusSession string) {
	sess, err := s.store.GetChatSessionByCerberusSession(ctx, cerberusSession)
	if err != nil {
		return
	}
	_ = s.store.TouchChatSession(ctx, sess.ID)

	events, err := s.store.ListCerberusEvents(ctx, cerberusSession, 0)
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
				if _, err := s.store.InsertChatMessage(ctx, sess.ID, "assistant", content); err != nil {
					log.Printf("chat assemble messages: insert: %v", err)
				}
			}
		}
	}
	// Flush any remaining content (turn ended without explicit message_end).
	if buf.Len() > 0 {
		if _, err := s.store.InsertChatMessage(ctx, sess.ID, "assistant", buf.String()); err != nil {
			log.Printf("chat assemble messages: insert trailing: %v", err)
		}
	}

	s.store.DeleteCerberusEvents(ctx, cerberusSession)
	_ = s.store.MarkChatSessionActive(ctx, sess.ID)
}

func (s *Service) SuspendSession(ctx context.Context, id int64) error {
	sess, err := s.store.GetChatSession(ctx, id)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sess.Status == "streaming" {
		return ErrSessionBusy
	}
	if err := s.store.MarkChatSessionSuspended(ctx, id); err != nil {
		if err == db.ErrNotFound {
			return ErrSessionBusy
		}
		return fmt.Errorf("mark session suspended: %w", err)
	}
	_ = s.cerb.Clean(ctx, sess.CerberusSession)
	_ = s.store.DeleteCerberusEvents(ctx, sess.CerberusSession)
	_ = os.Remove(profileFilePath(sess.CerberusSession))
	return nil
}

func (s *Service) AutoSuspendIdleSessions(ctx context.Context, idleFor time.Duration) error {
	sessions, err := s.store.ListIdleChatSessions(ctx, idleFor)
	if err != nil {
		return fmt.Errorf("list idle chat sessions: %w", err)
	}
	for _, sess := range sessions {
		if err := s.SuspendSession(ctx, sess.ID); err != nil && !errors.Is(err, ErrSessionBusy) && !errors.Is(err, db.ErrNotFound) {
			log.Printf("chat idle suspend %d: %v", sess.ID, err)
		}
	}
	return nil
}

// DeleteSession cleans up the cerberus session and removes DB rows.
func (s *Service) DeleteSession(ctx context.Context, id int64) error {
	sess, err := s.store.GetChatSession(ctx, id)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	// Best-effort cerberus clean — session may already be gone.
	_ = s.cerb.Clean(ctx, sess.CerberusSession)
	_ = s.store.DeleteCerberusEvents(ctx, sess.CerberusSession)
	_ = os.Remove(profileFilePath(sess.CerberusSession))
	return s.store.DeleteChatSession(ctx, id)
}

// AttachProject attaches a project to a session and resets the cerberus container.
func (s *Service) AttachProject(ctx context.Context, sessionID, projectID int64) error {
	sess, err := s.store.GetChatSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sess.Status == "streaming" {
		return ErrSessionBusy
	}
	if err := s.store.AttachProjectToSession(ctx, sessionID, projectID); err != nil {
		return fmt.Errorf("attach project: %w", err)
	}
	if sess.CerberusUUID != "" {
		_ = s.cerb.Clean(ctx, sess.CerberusSession)
		_ = s.store.ClearChatSessionUUID(ctx, sessionID)
	}
	return nil
}

// DetachProject detaches a project from a session and resets the cerberus container.
func (s *Service) DetachProject(ctx context.Context, sessionID, projectID int64) error {
	sess, err := s.store.GetChatSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sess.Status == "streaming" {
		return ErrSessionBusy
	}
	if err := s.store.DetachProjectFromSession(ctx, sessionID, projectID); err != nil {
		return fmt.Errorf("detach project: %w", err)
	}
	if sess.CerberusUUID != "" {
		_ = s.cerb.Clean(ctx, sess.CerberusSession)
		_ = s.store.ClearChatSessionUUID(ctx, sessionID)
	}
	return nil
}

// ListSessionProjects returns all projects attached to a session.
func (s *Service) ListSessionProjects(ctx context.Context, sessionID int64) ([]db.Project, error) {
	return s.store.ListSessionProjects(ctx, sessionID)
}

var nonAlphaNum = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	s := strings.ToLower(name)
	s = nonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
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
	if sess.Status == "suspended" {
		input.UUID = ""
		if msgs, dbErr := s.store.ListChatMessages(ctx, sess.ID); dbErr == nil {
			input.History = buildHistory(msgs)
		} else {
			log.Printf("chat turn %d: load suspended history: %v", sess.ID, dbErr)
		}
	}

	projects, err := s.store.ListSessionProjects(ctx, sess.ID)
	if err != nil {
		log.Printf("chat turn %d: list session projects: %v", sess.ID, err)
	}
	for i, p := range projects {
		containerPath := "/workspace"
		if i > 0 {
			containerPath = "/workspace/" + slugify(p.Name)
		}
		input.ExtraMounts = append(input.ExtraMounts, cerberus.Mount{
			Host:      p.RepoPath,
			Container: containerPath,
			ReadOnly:  true,
		})
	}
	if len(input.ExtraMounts) > 0 {
		lines := []string{"Attached project dirs (read-only):"}
		for _, m := range input.ExtraMounts {
			lines = append(lines, "  "+m.Container)
		}
		input.Instructions = strings.Join(lines, "\n")
	}

	out, err := s.cerb.Turn(ctx, input)
	if err != nil {
		log.Printf("chat turn %d: %v", sess.ID, err)
		_ = s.store.UpdateChatSessionStatus(ctx, sess.ID, "error")
		return
	}

	// Session not found — replay history and retry with a fresh session.
	if out.Status == "error" && out.Error == cerberus.ErrSessionNotFound {
		msgs, dbErr := s.store.ListChatMessages(ctx, sess.ID)
		if dbErr != nil {
			log.Printf("chat turn %d: load history: %v", sess.ID, dbErr)
			_ = s.store.UpdateChatSessionStatus(ctx, sess.ID, "error")
			return
		}
		input.UUID = ""
		input.History = buildHistory(msgs)
		out, err = s.cerb.Turn(ctx, input)
		if err != nil {
			log.Printf("chat turn %d (recovery): %v", sess.ID, err)
			_ = s.store.UpdateChatSessionStatus(ctx, sess.ID, "error")
			return
		}
	}

	// Stale session name exists in cerberus but our UUID is gone — clean it and retry fresh.
	if out.Status == "error" && strings.Contains(out.Error, cerberus.ErrSessionAlreadyExists) {
		log.Printf("chat turn %d: stale cerberus session %q — cleaning and retrying", sess.ID, sess.CerberusSession)
		_ = s.cerb.Clean(ctx, sess.CerberusSession)
		_ = s.store.ClearChatSessionUUID(ctx, sess.ID)
		msgs, dbErr := s.store.ListChatMessages(ctx, sess.ID)
		if dbErr != nil {
			log.Printf("chat turn %d: load history for stale-session recovery: %v", sess.ID, dbErr)
			_ = s.store.UpdateChatSessionStatus(ctx, sess.ID, "error")
			return
		}
		input.UUID = ""
		input.History = buildHistory(msgs)
		out, err = s.cerb.Turn(ctx, input)
		if err != nil {
			log.Printf("chat turn %d (stale-session recovery): %v", sess.ID, err)
			_ = s.store.UpdateChatSessionStatus(ctx, sess.ID, "error")
			return
		}
	}

	if out.Status == "error" {
		log.Printf("chat turn %d: cerberus error: %s", sess.ID, out.Error)
		_ = s.store.UpdateChatSessionStatus(ctx, sess.ID, "error")
		return
	}

	if out.UUID != "" && out.UUID != sess.CerberusUUID {
		_ = s.store.UpdateChatSessionUUID(ctx, sess.ID, out.UUID)
	}
	_ = s.store.MarkChatSessionActive(ctx, sess.ID)
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
	if _, err := s.store.GetProfileByName(ctx, profileName); err != nil {
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
	p, err := s.store.GetProfileByName(ctx, profileName)
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
