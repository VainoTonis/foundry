package chat

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
)

func TestCreateSessionUsesStableCerberusNameThroughService(t *testing.T) {
	store := newFakeStore()
	store.profiles["dev"] = db.Profile{Name: "dev"}
	svc := newService(store, &fakeCerberus{}, "", nil)

	sess, err := svc.CreateSession(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}

	if sess.ID != 1 || sess.CerberusSession != "foundry-chat-1" {
		t.Fatalf("session = %#v, want ID 1 with stable cerberus name", sess)
	}
	stored := store.mustSession(t, sess.ID)
	if stored.CerberusSession != "foundry-chat-1" || stored.Title != "" || stored.ProfileName != "dev" {
		t.Fatalf("stored session = %#v", stored)
	}
}

func TestCreateSessionRejectsMissingProfileThroughService(t *testing.T) {
	svc := newService(newFakeStore(), &fakeCerberus{}, "", nil)

	_, err := svc.CreateSession(context.Background(), "missing")
	if !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("CreateSession error = %v, want ErrProfileNotFound", err)
	}
}

func TestSendMessageBuildsProjectMountsThroughService(t *testing.T) {
	store := newFakeStore()
	store.sessions[7] = db.ChatSession{ID: 7, CerberusSession: "foundry-chat-7", Status: "active"}
	store.projects[7] = []db.Project{
		{ID: 10, Name: "Core Repo", RepoPath: "/repos/core"},
		{ID: 11, Name: "Side Project!", RepoPath: "/repos/side"},
	}
	cerb := &fakeCerberus{turnOut: cerberus.TurnOutput{Status: "ok", UUID: "uuid-1"}}
	svc := newService(store, cerb, "http://callback", nil)

	if err := svc.SendMessage(context.Background(), 7, "hello"); err != nil {
		t.Fatal(err)
	}
	input := cerb.waitTurn(t)

	if input.Name != "foundry-chat-7" || !input.NoRepo || input.Message != "hello" || input.CallbackURL != "http://callback" {
		t.Fatalf("turn input basics = %#v", input)
	}
	wantMounts := []cerberus.Mount{
		{Host: "/repos/core", Container: "/workspace", ReadOnly: true},
		{Host: "/repos/side", Container: "/workspace/side-project", ReadOnly: true},
	}
	if !reflect.DeepEqual(input.ExtraMounts, wantMounts) {
		t.Fatalf("mounts = %#v, want %#v", input.ExtraMounts, wantMounts)
	}
	wantInstructions := "Attached project dirs (read-only):\n  /workspace\n  /workspace/side-project"
	if input.Instructions != wantInstructions {
		t.Fatalf("instructions = %q, want %q", input.Instructions, wantInstructions)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.sessions[7].CerberusUUID != "uuid-1" || store.sessions[7].Status != "active" {
		t.Fatalf("session after turn = %#v", store.sessions[7])
	}
	if len(store.messages[7]) != 1 || store.messages[7][0].Role != "user" || store.messages[7][0].Content != "hello" {
		t.Fatalf("messages = %#v", store.messages[7])
	}
}

func TestSendMessageReplaysHistoryForSuspendedSessionThroughService(t *testing.T) {
	store := newFakeStore()
	store.sessions[9] = db.ChatSession{ID: 9, CerberusSession: "foundry-chat-9", CerberusUUID: "stale", Status: "suspended"}
	store.messages[9] = []db.ChatMessage{
		{ID: 1, SessionID: 9, Role: "user", Content: "one"},
		{ID: 2, SessionID: 9, Role: "assistant", Content: "two"},
	}
	cerb := &fakeCerberus{turnOut: cerberus.TurnOutput{Status: "ok", UUID: "uuid-2"}}
	svc := newService(store, cerb, "", nil)

	if err := svc.SendMessage(context.Background(), 9, "three"); err != nil {
		t.Fatal(err)
	}
	input := cerb.waitTurn(t)

	if input.UUID != "" {
		t.Fatalf("UUID = %q, want empty for suspended replay", input.UUID)
	}
	if len(input.History) != 3 {
		t.Fatalf("history len = %d, want 3: %#v", len(input.History), input.History)
	}
	if input.History[0].Role != "user" || input.History[1].ParentID != "1" || input.History[2].Content != "three" {
		t.Fatalf("history = %#v", input.History)
	}
}

func TestAttachProjectResetsExistingCerberusSessionThroughService(t *testing.T) {
	store := newFakeStore()
	store.sessions[12] = db.ChatSession{ID: 12, CerberusSession: "foundry-chat-12", CerberusUUID: "uuid-old", Status: "active"}
	cerb := &fakeCerberus{}
	svc := newService(store, cerb, "", nil)

	if err := svc.AttachProject(context.Background(), 12, 34); err != nil {
		t.Fatal(err)
	}

	if !store.attached[[2]int64{12, 34}] {
		t.Fatalf("project was not attached")
	}
	if got := store.mustSession(t, 12).CerberusUUID; got != "" {
		t.Fatalf("cerberus uuid = %q, want cleared", got)
	}
	if !reflect.DeepEqual(cerb.cleaned, []string{"foundry-chat-12"}) {
		t.Fatalf("cleaned = %#v", cerb.cleaned)
	}
}

func TestAttachProjectRejectsStreamingSessionThroughService(t *testing.T) {
	store := newFakeStore()
	store.sessions[12] = db.ChatSession{ID: 12, CerberusSession: "foundry-chat-12", Status: "streaming"}
	svc := newService(store, &fakeCerberus{}, "", nil)

	err := svc.AttachProject(context.Background(), 12, 34)
	if !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("AttachProject error = %v, want ErrSessionBusy", err)
	}
	if store.attached[[2]int64{12, 34}] {
		t.Fatalf("streaming session should not attach project")
	}
}

type fakeCerberus struct {
	mu      sync.Mutex
	turnCh  chan cerberus.TurnInput
	turnOut cerberus.TurnOutput
	turnErr error
	cleaned []string
}

func (f *fakeCerberus) Turn(_ context.Context, input cerberus.TurnInput) (cerberus.TurnOutput, error) {
	f.mu.Lock()
	if f.turnCh == nil {
		f.turnCh = make(chan cerberus.TurnInput, 10)
	}
	out := f.turnOut
	err := f.turnErr
	f.mu.Unlock()
	f.turnCh <- input
	return out, err
}

func (f *fakeCerberus) Clean(_ context.Context, session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleaned = append(f.cleaned, session)
	return nil
}

func (f *fakeCerberus) waitTurn(t *testing.T) cerberus.TurnInput {
	t.Helper()
	f.mu.Lock()
	if f.turnCh == nil {
		f.turnCh = make(chan cerberus.TurnInput, 10)
	}
	ch := f.turnCh
	f.mu.Unlock()
	select {
	case input := <-ch:
		return input
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Cerberus turn")
		return cerberus.TurnInput{}
	}
}

type fakeStore struct {
	mu       sync.Mutex
	nextID   int64
	nextMsg  int64
	sessions map[int64]db.ChatSession
	messages map[int64][]db.ChatMessage
	projects map[int64][]db.Project
	profiles map[string]db.Profile
	attached map[[2]int64]bool
	events   map[string][]db.CerberusEvent
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		nextID:   1,
		nextMsg:  1,
		sessions: map[int64]db.ChatSession{},
		messages: map[int64][]db.ChatMessage{},
		projects: map[int64][]db.Project{},
		profiles: map[string]db.Profile{},
		attached: map[[2]int64]bool{},
		events:   map[string][]db.CerberusEvent{},
	}
}

func (f *fakeStore) mustSession(t *testing.T, id int64) db.ChatSession {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		t.Fatalf("missing session %d", id)
	}
	return s
}

func (f *fakeStore) CreateChatSession(_ context.Context, cerberusSession, profileName string) (db.ChatSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := db.ChatSession{ID: f.nextID, CerberusSession: cerberusSession, ProfileName: profileName, Status: "active"}
	f.nextID++
	f.sessions[s.ID] = s
	return s, nil
}

func (f *fakeStore) UpdateChatSessionCerberusSession(_ context.Context, id int64, cerberusSession string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return db.ErrNotFound
	}
	s.CerberusSession = cerberusSession
	f.sessions[id] = s
	return nil
}

func (f *fakeStore) GetChatSession(_ context.Context, id int64) (db.ChatSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return db.ChatSession{}, db.ErrNotFound
	}
	return s, nil
}

func (f *fakeStore) GetChatSessionByCerberusSession(_ context.Context, cerberusSession string) (db.ChatSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if s.CerberusSession == cerberusSession {
			return s, nil
		}
	}
	return db.ChatSession{}, db.ErrNotFound
}

func (f *fakeStore) ListChatSessions(context.Context) ([]db.ChatSession, error) { return nil, nil }

func (f *fakeStore) UpdateChatSessionProfileName(_ context.Context, id int64, profileName string) error {
	return f.updateSession(id, func(s *db.ChatSession) { s.ProfileName = profileName })
}

func (f *fakeStore) UpdateChatSessionUUID(_ context.Context, id int64, uuid string) error {
	return f.updateSession(id, func(s *db.ChatSession) { s.CerberusUUID = uuid })
}

func (f *fakeStore) UpdateChatSessionStatus(_ context.Context, id int64, status string) error {
	return f.updateSession(id, func(s *db.ChatSession) { s.Status = status })
}

func (f *fakeStore) TouchChatSession(context.Context, int64) error { return nil }

func (f *fakeStore) MarkChatSessionStreaming(_ context.Context, id int64) error {
	return f.updateSession(id, func(s *db.ChatSession) { s.Status = "streaming" })
}

func (f *fakeStore) MarkChatSessionActive(_ context.Context, id int64) error {
	return f.updateSession(id, func(s *db.ChatSession) { s.Status = "active" })
}

func (f *fakeStore) MarkChatSessionSuspended(_ context.Context, id int64) error {
	return f.updateSession(id, func(s *db.ChatSession) { s.Status = "suspended" })
}

func (f *fakeStore) ListIdleChatSessions(context.Context, time.Duration) ([]db.ChatSession, error) {
	return nil, nil
}

func (f *fakeStore) UpdateChatSessionTitle(_ context.Context, id int64, title string) error {
	return f.updateSession(id, func(s *db.ChatSession) { s.Title = title })
}

func (f *fakeStore) DeleteChatSession(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, id)
	return nil
}

func (f *fakeStore) ClearChatSessionUUID(_ context.Context, id int64) error {
	return f.updateSession(id, func(s *db.ChatSession) { s.CerberusUUID = "" })
}

func (f *fakeStore) InsertChatMessage(_ context.Context, sessionID int64, role, content string) (db.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := db.ChatMessage{ID: f.nextMsg, SessionID: sessionID, Role: role, Content: content}
	f.nextMsg++
	f.messages[sessionID] = append(f.messages[sessionID], m)
	return m, nil
}

func (f *fakeStore) ListChatMessages(_ context.Context, sessionID int64) ([]db.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]db.ChatMessage(nil), f.messages[sessionID]...), nil
}

func (f *fakeStore) AttachProjectToSession(_ context.Context, sessionID, projectID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attached[[2]int64{sessionID, projectID}] = true
	return nil
}

func (f *fakeStore) DetachProjectFromSession(_ context.Context, sessionID, projectID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.attached, [2]int64{sessionID, projectID})
	return nil
}

func (f *fakeStore) ListSessionProjects(_ context.Context, sessionID int64) ([]db.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]db.Project(nil), f.projects[sessionID]...), nil
}

func (f *fakeStore) GetProfileByName(_ context.Context, name string) (db.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.profiles[name]
	if !ok {
		return db.Profile{}, db.ErrNotFound
	}
	return p, nil
}

func (f *fakeStore) ListCerberusEvents(_ context.Context, session string, _ int64) ([]db.CerberusEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]db.CerberusEvent(nil), f.events[session]...), nil
}

func (f *fakeStore) DeleteCerberusEvents(_ context.Context, session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.events, session)
	return nil
}

func (f *fakeStore) updateSession(id int64, update func(*db.ChatSession)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return db.ErrNotFound
	}
	update(&s)
	f.sessions[id] = s
	return nil
}
