package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

func TestIngest_SemanticLifecycleAndProvenance_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := time.Now().UTC().Format("150405.000000000")
	parent := "semantic-parent-" + suffix
	child := "semantic-child-" + suffix

	var repositoryID, secondaryRepositoryID int64
	if err := pool.QueryRow(ctx, `INSERT INTO repositories (name, local_path) VALUES ($1, $2) RETURNING id`,
		"semantic-"+suffix, "/tmp/foundry-semantic-"+suffix).Scan(&repositoryID); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO repositories (name, local_path) VALUES ($1, $2) RETURNING id`,
		"semantic-secondary-"+suffix, "/tmp/foundry-semantic-secondary-"+suffix).Scan(&secondaryRepositoryID); err != nil {
		t.Fatalf("create secondary repository: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent_sessions WHERE session = ANY($1)`, []string{parent, child})
		_, _ = pool.Exec(context.Background(), `DELETE FROM repositories WHERE id = ANY($1)`, []int64{repositoryID, secondaryRepositoryID})
	})

	parentSource := parent + "-source"
	childSource := child + "-source"
	confidence := 0.9
	parentStartedAt := time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)
	childStartedAt := parentStartedAt.Add(time.Second)
	endedAt := childStartedAt.Add(time.Second)
	activityAt := endedAt.Add(time.Second)
	finalMessageAt := activityAt.Add(time.Second)
	if err := Ingest(ctx, pool, Event{
		Type:      EventSessionStart,
		Session:   Session{Session: parent, SourceSessionID: parentSource, Origin: "test", SchemaVersion: SemanticSchemaVersion},
		Timestamp: parentStartedAt,
	}); err != nil {
		t.Fatalf("ingest parent start: %v", err)
	}
	if err := Ingest(ctx, pool, Event{
		Type:        EventSessionStart,
		Session:     Session{Session: child, SourceSessionID: childSource, Origin: "test", SchemaVersion: SemanticSchemaVersion, ParentSourceSessionID: &parentSource},
		Attribution: Attribution{RepositoryID: &repositoryID, AttributionMethod: "explicit", AttributionConfidence: &confidence},
		Timestamp:   childStartedAt,
	}); err != nil {
		t.Fatalf("ingest child start: %v", err)
	}
	secondaryConfidence := 0.8
	if err := Ingest(ctx, pool, Event{
		Type:        EventSessionStart,
		Session:     Session{Session: child, SourceSessionID: childSource, Origin: "test", SchemaVersion: SemanticSchemaVersion},
		Attribution: Attribution{RepositoryID: &secondaryRepositoryID, AttributionMethod: "tool_path", AttributionConfidence: &secondaryConfidence},
		Timestamp:   childStartedAt.Add(500 * time.Millisecond),
	}); err != nil {
		t.Fatalf("ingest secondary repository attribution: %v", err)
	}

	if err := Ingest(ctx, pool, Event{Type: EventSessionEnd, Session: Session{Session: child, CloseReason: "producer_shutdown"}, Timestamp: endedAt}); err != nil {
		t.Fatalf("ingest child end: %v", err)
	}
	closed, err := db.GetAgentSessionBySession(ctx, pool, child)
	if err != nil {
		t.Fatalf("get closed child: %v", err)
	}
	if closed.EndedAt == nil || !closed.EndedAt.Equal(endedAt) || closed.LifecycleState != "closed" || !closed.LastEventAt.Equal(endedAt) {
		t.Fatalf("child was not closed at producer event time: %+v", closed)
	}

	// A replacement producer reopening the same source session after reload or
	// resume must clear the durable close while preserving its close evidence.
	if err := Ingest(ctx, pool, Event{
		Type:      EventSessionStart,
		Session:   Session{Session: child, SourceSessionID: childSource, Origin: "test", SchemaVersion: SemanticSchemaVersion},
		Timestamp: activityAt,
	}); err != nil {
		t.Fatalf("ingest reload start: %v", err)
	}
	turnIndex := int64(3)
	messageID := "message-" + suffix
	if err := Ingest(ctx, pool, Event{
		Type: EventMessageEnd, Session: Session{Session: child}, Timestamp: activityAt,
		Usage: Usage{InputTokens: 2}, Turn: Turn{
			TurnIndex: &turnIndex, SourceMessageID: &messageID,
			Model: "new-model", Provider: "provider", ThinkingLevel: "high", StopReason: "complete",
		},
	}); err != nil {
		t.Fatalf("ingest reopening turn: %v", err)
	}
	reopened, err := db.GetAgentSessionBySession(ctx, pool, child)
	if err != nil {
		t.Fatalf("get reopened child: %v", err)
	}
	if reopened.EndedAt != nil || reopened.LifecycleState != "active" || !reopened.LastEventAt.Equal(activityAt) {
		t.Fatalf("activity did not reopen child at producer event time: %+v", reopened)
	}

	if err := Ingest(ctx, pool, Event{
		Type: EventFinalMessage, Session: Session{Session: child}, Timestamp: finalMessageAt,
		Message: Message{
			Role: "assistant", Content: "done", SourceMessageID: &messageID, TurnIndex: &turnIndex,
			InputSource: "model", IsFinal: true,
		},
	}); err != nil {
		t.Fatalf("ingest final message: %v", err)
	}

	session, err := db.GetAgentSessionBySession(ctx, pool, child)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if session.SchemaVersion != SemanticSchemaVersion || !session.StartEventSeen || !session.EndEventSeen {
		t.Fatalf("semantic lifecycle flags not persisted: %+v", session)
	}
	if session.EndedAt != nil || session.LifecycleState != "active" || session.CloseReason != "producer_shutdown" {
		t.Fatalf("activity did not reopen child: %+v", session)
	}
	if !session.LastEventAt.Equal(finalMessageAt) {
		t.Fatalf("LastEventAt = %v, want final message time %v", session.LastEventAt, finalMessageAt)
	}
	if session.ParentSession == nil || *session.ParentSession != parent || session.ParentSourceSessionID == nil || *session.ParentSourceSessionID != parentSource {
		t.Fatalf("parent source was not resolved: %+v", session)
	}
	if session.RepositoryID == nil || *session.RepositoryID != repositoryID {
		t.Fatalf("primary repository attribution regressed: %+v", session)
	}
	var repositoryCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_session_repositories WHERE agent_session_id = $1
		AND repository_id = ANY($2)`, session.ID, []int64{repositoryID, secondaryRepositoryID}).Scan(&repositoryCount); err != nil {
		t.Fatalf("read multi-repository attribution: %v", err)
	}
	if repositoryCount != 2 {
		t.Fatalf("repository attribution count = %d, want both realistic repositories", repositoryCount)
	}

	turns, err := db.ListAgentTurnsBySession(ctx, pool, session.ID)
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns = %+v, err = %v", turns, err)
	}
	if turns[0].Model != "new-model" || turns[0].Provider != "provider" || turns[0].ThinkingLevel != "high" || turns[0].StopReason != "complete" ||
		turns[0].TurnIndex == nil || *turns[0].TurnIndex != turnIndex || turns[0].SourceMessageID == nil || *turns[0].SourceMessageID != messageID {
		t.Fatalf("turn provenance = %+v", turns[0])
	}
	messages, err := db.ListAgentMessagesBySession(ctx, pool, session.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages = %+v, err = %v", messages, err)
	}
	if messages[0].SourceMessageID == nil || *messages[0].SourceMessageID != messageID ||
		messages[0].TurnIndex == nil || *messages[0].TurnIndex != turnIndex || messages[0].InputSource != "model" || !messages[0].IsFinal {
		t.Fatalf("message provenance = %+v", messages[0])
	}
}

func TestIngest_PreservesProducerIsFinal_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	sessionName := "is-final-" + time.Now().UTC().Format("150405.000000000")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent_sessions WHERE session = $1`, sessionName)
	})

	for _, message := range []Message{
		{Role: "user", Content: "prompt", IsFinal: false},
		{Role: "assistant", Content: "outcome", IsFinal: true},
	} {
		if err := Ingest(ctx, pool, Event{Type: EventFinalMessage, Session: Session{Session: sessionName}, Message: message}); err != nil {
			t.Fatalf("ingest %s message: %v", message.Role, err)
		}
	}
	session, err := db.GetAgentSessionBySession(ctx, pool, sessionName)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	messages, err := db.ListAgentMessagesBySession(ctx, pool, session.ID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages = %+v, err = %v", messages, err)
	}
	if messages[0].IsFinal || !messages[1].IsFinal {
		t.Fatalf("is_final flags = [%t, %t], want [false, true]", messages[0].IsFinal, messages[1].IsFinal)
	}
}

func TestIngest_RepositoryEvidenceDoesNotRegress_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "-")
	sessionName := "semantic-evidence-" + suffix
	var repositoryID int64
	if err := pool.QueryRow(ctx, `INSERT INTO repositories (name, local_path) VALUES ($1, $2) RETURNING id`,
		sessionName, "/tmp/"+sessionName).Scan(&repositoryID); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent_sessions WHERE session = $1`, sessionName)
		_, _ = pool.Exec(context.Background(), `DELETE FROM repositories WHERE id = $1`, repositoryID)
	})

	high, low := 0.95, 0.2
	for _, evidence := range []struct {
		method string
		value  *float64
	}{{"explicit", &high}, {"path_guess", &low}} {
		if err := Ingest(ctx, pool, Event{
			Type:        EventSessionStart,
			Session:     Session{Session: sessionName, SourceSessionID: sessionName + "-source", Origin: "test", SchemaVersion: SemanticSchemaVersion},
			Attribution: Attribution{RepositoryID: &repositoryID, AttributionMethod: evidence.method, AttributionConfidence: evidence.value},
		}); err != nil {
			t.Fatalf("ingest %s evidence: %v", evidence.method, err)
		}
	}
	var method string
	var gotConfidence float64
	if err := pool.QueryRow(ctx, `SELECT attribution_method, attribution_confidence FROM agent_session_repositories WHERE repository_id = $1`, repositoryID).Scan(&method, &gotConfidence); err != nil {
		t.Fatalf("read repository evidence: %v", err)
	}
	if method != "explicit" || gotConfidence != high {
		t.Fatalf("repository evidence = (%q, %v), want explicit/%v", method, gotConfidence, high)
	}
}

func TestIngest_CrashStaleSessionReopensOnResume_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "-")
	sessionName := "semantic-crash-stale-" + suffix
	var repositoryID int64
	if err := pool.QueryRow(ctx, `INSERT INTO repositories (name, local_path) VALUES ($1, $2) RETURNING id`,
		sessionName, "/tmp/"+sessionName).Scan(&repositoryID); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent_sessions WHERE session = $1`, sessionName)
		_, _ = pool.Exec(context.Background(), `DELETE FROM repositories WHERE id = $1`, repositoryID)
	})

	staleAt := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	start := Event{Type: EventSessionStart,
		Session:     Session{Session: sessionName, SourceSessionID: sessionName + "-source", Origin: "pi", SchemaVersion: SemanticSchemaVersion},
		Attribution: Attribution{RepositoryID: &repositoryID, AttributionMethod: "explicit"}, Timestamp: staleAt}
	if err := Ingest(ctx, pool, start); err != nil {
		t.Fatalf("ingest crash-stale start: %v", err)
	}
	stale, err := db.ListAgentSessionsPage(ctx, pool, db.AgentSessionPageParams{Limit: 10, Lifecycle: "stale", RepositoryID: &repositoryID})
	if err != nil || len(stale) != 1 || stale[0].Session != sessionName || stale[0].EndEventSeen {
		t.Fatalf("crash-stale lifecycle = %+v, err = %v", stale, err)
	}

	start.Timestamp = time.Now().UTC()
	if err := Ingest(ctx, pool, start); err != nil {
		t.Fatalf("ingest resumed start: %v", err)
	}
	active, err := db.ListAgentSessionsPage(ctx, pool, db.AgentSessionPageParams{Limit: 10, Lifecycle: "active", RepositoryID: &repositoryID})
	if err != nil || len(active) != 1 || active[0].Session != sessionName || active[0].LifecycleState != "active" || active[0].EndedAt != nil {
		t.Fatalf("resumed lifecycle = %+v, err = %v", active, err)
	}
}

func TestIngest_UnsupportedSemanticSchemaVersion_Postgres(t *testing.T) {
	pool := testPool(t)
	err := Ingest(context.Background(), pool, Event{
		Type:    EventSessionStart,
		Session: Session{Session: "unsupported-schema", SourceSessionID: "unsupported-schema-source", Origin: "test", SchemaVersion: "999"},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Fatalf("Ingest() error = %v, want unsupported schema_version", err)
	}
}
