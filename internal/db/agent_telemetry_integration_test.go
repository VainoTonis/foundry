package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func createTestAgentSession(t *testing.T, pool *pgxpool.Pool, p EnsureAgentSessionParams) AgentSession {
	t.Helper()
	s, err := EnsureAgentSession(context.Background(), pool, p)
	if err != nil {
		t.Fatalf("EnsureAgentSession(%+v): %v", p, err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM agent_sessions WHERE id = $1`, s.ID); err != nil {
			t.Errorf("cleanup delete agent_sessions %d: %v", s.ID, err)
		}
	})
	return s
}

func TestSemanticTelemetrySchema_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	type columnExpectation struct {
		table      string
		column     string
		nullable   string
		hasDefault bool
	}
	expected := []columnExpectation{
		{"agent_sessions", "schema_version", "NO", true},
		{"agent_sessions", "last_event_at", "NO", true},
		{"agent_sessions", "close_reason", "NO", true},
		{"agent_sessions", "lifecycle_state", "NO", true},
		{"agent_sessions", "start_event_seen", "NO", true},
		{"agent_sessions", "end_event_seen", "NO", true},
		{"agent_sessions", "parent_source_session_id", "YES", false},
		{"agent_turns", "model", "NO", true},
		{"agent_turns", "provider", "NO", true},
		{"agent_turns", "thinking_level", "NO", true},
		{"agent_turns", "stop_reason", "NO", true},
		{"agent_turns", "turn_index", "YES", false},
		{"agent_turns", "source_message_id", "YES", false},
		{"agent_messages", "source_message_id", "YES", false},
		{"agent_messages", "turn_index", "YES", false},
		{"agent_messages", "input_source", "NO", true},
		{"agent_messages", "is_final", "NO", true},
		{"agent_session_repositories", "agent_session_id", "NO", false},
		{"agent_session_repositories", "repository_id", "NO", false},
		{"agent_session_repositories", "attribution_method", "NO", true},
		{"agent_session_repositories", "attribution_confidence", "YES", false},
	}
	for _, want := range expected {
		var nullable string
		var defaultValue *string
		err := pool.QueryRow(ctx, `
			SELECT is_nullable, column_default
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = $1 AND column_name = $2`,
			want.table, want.column,
		).Scan(&nullable, &defaultValue)
		if err != nil {
			t.Fatalf("schema column %s.%s: %v", want.table, want.column, err)
		}
		if nullable != want.nullable {
			t.Errorf("%s.%s nullable = %s, want %s", want.table, want.column, nullable, want.nullable)
		}
		if (defaultValue != nil) != want.hasDefault {
			t.Errorf("%s.%s default = %v, want hasDefault=%t", want.table, want.column, defaultValue, want.hasDefault)
		}
	}

	for _, index := range []string{
		"agent_sessions_recent_idx",
		"agent_session_repositories_repository_session_idx",
		"agent_messages_session_source_message_id_key",
		"agent_turns_session_turn_index_idx",
		"agent_messages_session_turn_index_idx",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, index).Scan(&exists); err != nil {
			t.Fatalf("look up index %s: %v", index, err)
		}
		if !exists {
			t.Errorf("index %s does not exist", index)
		}
	}
}

func TestEnsureAgentSession_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := "agent-telemetry-ensure-dup"

	repoID := int64(12345)
	model := "claude-x"
	created := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         session,
		SourceSessionID: session + "-source",
		Origin:          "cli",
		Kind:            "coding",
		Model:           &model,
	})
	if created.Kind != "coding" {
		t.Fatalf("Kind = %q, want %q", created.Kind, "coding")
	}
	if created.Origin != "cli" {
		t.Fatalf("Origin = %q, want %q", created.Origin, "cli")
	}
	if created.Model == nil || *created.Model != model {
		t.Fatalf("Model = %v, want %q", created.Model, model)
	}
	if created.RepositoryID != nil {
		t.Fatalf("RepositoryID = %v, want nil", created.RepositoryID)
	}

	otherModel := "should-not-overwrite"
	updated, err := EnsureAgentSession(ctx, pool, EnsureAgentSessionParams{
		Session:         session,
		SourceSessionID: session + "-source",
		Origin:          "should-not-overwrite-origin",
		Kind:            "should-not-overwrite-kind",
		Model:           &otherModel,
		RepositoryID:    &repoID,
	})
	if err != nil {
		t.Fatalf("EnsureAgentSession() duplicate call error = %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("duplicate EnsureAgentSession() returned a different row: ID = %d, want %d", updated.ID, created.ID)
	}
	if updated.Origin != "cli" {
		t.Fatalf("Origin = %q, want unchanged %q (first write wins)", updated.Origin, "cli")
	}
	if updated.Kind != "coding" {
		t.Fatalf("Kind = %q, want unchanged %q", updated.Kind, "coding")
	}
	if updated.Model == nil || *updated.Model != model {
		t.Fatalf("Model = %v, want unchanged %q (already populated, must not overwrite)", updated.Model, model)
	}
	if updated.RepositoryID == nil || *updated.RepositoryID != repoID {
		t.Fatalf("RepositoryID = %v, want %d (was absent, must be filled)", updated.RepositoryID, repoID)
	}

	got, err := GetAgentSessionBySession(ctx, pool, session)
	if err != nil {
		t.Fatalf("GetAgentSessionBySession() error = %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("GetAgentSessionBySession().ID = %d, want %d", got.ID, created.ID)
	}
	if got.RepositoryID == nil || *got.RepositoryID != repoID {
		t.Fatalf("persisted RepositoryID = %v, want %d", got.RepositoryID, repoID)
	}

	if _, err := GetAgentSessionBySession(ctx, pool, "agent-telemetry-does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAgentSessionBySession() on unknown session error = %v, want %v", err, ErrNotFound)
	}
}

// TestEnsureAgentSession_UpgradesPlaceholderAttribution_Postgres verifies
// that a fallback row created with placeholder attribution (source_session_id
// equal to the session identifier, origin "unknown") can later be upgraded
// by a call carrying real attribution, but that real attribution, once set,
// is never overwritten by a subsequent placeholder value.
func TestEnsureAgentSession_UpgradesPlaceholderAttribution_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := "agent-telemetry-ensure-placeholder-upgrade"

	fallback := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         session,
		SourceSessionID: session,
		Origin:          "unknown",
	})
	if fallback.SourceSessionID != session {
		t.Fatalf("SourceSessionID = %q, want placeholder %q", fallback.SourceSessionID, session)
	}
	if fallback.Origin != "unknown" {
		t.Fatalf("Origin = %q, want placeholder %q", fallback.Origin, "unknown")
	}

	upgraded, err := EnsureAgentSession(ctx, pool, EnsureAgentSessionParams{
		Session:         session,
		SourceSessionID: session + "-real-source",
		Origin:          "claude-code",
	})
	if err != nil {
		t.Fatalf("EnsureAgentSession() upgrade error = %v", err)
	}
	if upgraded.ID != fallback.ID {
		t.Fatalf("upgraded.ID = %d, want unchanged %d", upgraded.ID, fallback.ID)
	}
	if upgraded.SourceSessionID != session+"-real-source" {
		t.Fatalf("SourceSessionID = %q, want upgraded to %q", upgraded.SourceSessionID, session+"-real-source")
	}
	if upgraded.Origin != "claude-code" {
		t.Fatalf("Origin = %q, want upgraded to %q", upgraded.Origin, "claude-code")
	}

	// A later call carrying placeholder-shaped values must not overwrite
	// the now-real attribution.
	regressed, err := EnsureAgentSession(ctx, pool, EnsureAgentSessionParams{
		Session:         session,
		SourceSessionID: session,
		Origin:          "unknown",
	})
	if err != nil {
		t.Fatalf("EnsureAgentSession() post-upgrade error = %v", err)
	}
	if regressed.SourceSessionID != session+"-real-source" {
		t.Fatalf("SourceSessionID = %q, want unchanged %q (must not regress to placeholder)", regressed.SourceSessionID, session+"-real-source")
	}
	if regressed.Origin != "claude-code" {
		t.Fatalf("Origin = %q, want unchanged %q (must not regress to placeholder)", regressed.Origin, "claude-code")
	}
}

func TestEnsureAgentSession_MultiRepositoryAndResumeLifecycle_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	fixture := fmt.Sprintf("agent-semantic-attribution-%d", time.Now().UnixNano())
	repoA := createTestPlanRepo(t, pool, fixture+"-a")
	repoB := createTestPlanRepo(t, pool, fixture+"-b")
	started := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	high, medium := 0.95, 0.75

	session := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session: fixture, SourceSessionID: fixture + "-source", Origin: "pi",
		RepositoryID: &repoA.ID, AttributionMethod: "explicit", AttributionConfidence: &high,
		LifecycleState: "active", StartEventSeen: true, EventAt: &started, StartedAt: &started,
	})
	if _, err := EnsureAgentSession(ctx, pool, EnsureAgentSessionParams{
		Session: fixture, SourceSessionID: fixture + "-source", Origin: "pi",
		RepositoryID: &repoB.ID, AttributionMethod: "tool_path", AttributionConfidence: &medium,
		LifecycleState: "active", EventAt: &started,
	}); err != nil {
		t.Fatalf("attach secondary repository: %v", err)
	}
	rows, err := pool.Query(ctx, `SELECT repository_id, attribution_method FROM agent_session_repositories
		WHERE agent_session_id = $1 ORDER BY repository_id`, session.ID)
	if err != nil {
		t.Fatalf("list repository attribution: %v", err)
	}
	defer rows.Close()
	got := map[int64]string{}
	for rows.Next() {
		var id int64
		var method string
		if err := rows.Scan(&id, &method); err != nil {
			t.Fatalf("scan repository attribution: %v", err)
		}
		got[id] = method
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("repository attribution rows: %v", err)
	}
	if got[repoA.ID] != "explicit" || got[repoB.ID] != "tool_path" {
		t.Fatalf("multi-repository attribution = %+v", got)
	}

	closedAt := started.Add(time.Minute)
	closed, err := CloseAgentSession(ctx, pool, session.ID, &closedAt, "reload")
	if err != nil || closed.LifecycleState != "closed" || !closed.EndEventSeen || closed.CloseReason != "reload" {
		t.Fatalf("closed lifecycle = %+v, err = %v", closed, err)
	}
	resumedAt := closedAt.Add(time.Second)
	resumed, err := EnsureAgentSession(ctx, pool, EnsureAgentSessionParams{
		Session: fixture, SourceSessionID: fixture + "-source", Origin: "pi",
		LifecycleState: "active", StartEventSeen: true, EventAt: &resumedAt,
	})
	if err != nil || resumed.LifecycleState != "active" || resumed.EndedAt != nil ||
		!resumed.EndEventSeen || resumed.CloseReason != "reload" || !resumed.LastEventAt.Equal(resumedAt) {
		t.Fatalf("resumed lifecycle/provenance = %+v, err = %v", resumed, err)
	}
	if resumed.RepositoryID == nil || *resumed.RepositoryID != repoA.ID {
		t.Fatalf("resume changed primary repository: %+v", resumed)
	}
}

func TestAllocateAgentSessionSeq_ConcurrentAllocation_Postgres(t *testing.T) {
	pool := testPool(t)

	session := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "agent-telemetry-concurrent-seq",
		SourceSessionID: "agent-telemetry-concurrent-seq-source",
		Origin:          "cli",
	})

	const n = 25
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seqs []int64
		errs []error
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seq, err := AllocateAgentSessionSeq(context.Background(), pool, session.ID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			seqs = append(seqs, seq)
		}()
	}
	wg.Wait()

	for _, err := range errs {
		t.Fatalf("AllocateAgentSessionSeq() error = %v", err)
	}
	if len(seqs) != n {
		t.Fatalf("got %d allocated seqs, want %d", len(seqs), n)
	}

	seen := make(map[int64]bool, n)
	for _, s := range seqs {
		if seen[s] {
			t.Fatalf("AllocateAgentSessionSeq() allocated duplicate seq %d across concurrent callers: %v", s, seqs)
		}
		seen[s] = true
	}

	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, s := range seqs {
		if s != int64(i) {
			t.Fatalf("allocated seqs = %v, want a contiguous 0..%d run with no concurrent allocation failures", seqs, n-1)
		}
	}
}

func TestAttachAgentToolResult_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "agent-telemetry-attach-result",
		SourceSessionID: "agent-telemetry-attach-result-source",
		Origin:          "cli",
	})

	nextSeq := func() int64 {
		seq, err := AllocateAgentSessionSeq(ctx, pool, session.ID)
		if err != nil {
			t.Fatalf("AllocateAgentSessionSeq() error = %v", err)
		}
		return seq
	}

	t.Run("exact tool_call_id match is preferred", func(t *testing.T) {
		callID := "call-exact-1"
		call, err := InsertAgentToolCall(ctx, pool, InsertAgentToolCallParams{
			AgentSessionID: session.ID,
			Seq:            nextSeq(),
			ToolCallID:     &callID,
			ToolName:       "bash",
		})
		if err != nil {
			t.Fatalf("InsertAgentToolCall() error = %v", err)
		}

		result := "ok"
		attached, err := AttachAgentToolResult(ctx, pool, AttachAgentToolResultParams{
			AgentSessionID: session.ID,
			ToolCallID:     &callID,
			ToolName:       "bash",
			ResultSeq:      nextSeq(),
			Result:         &result,
		})
		if err != nil {
			t.Fatalf("AttachAgentToolResult() error = %v", err)
		}
		if attached.ID != call.ID {
			t.Fatalf("attached.ID = %d, want %d", attached.ID, call.ID)
		}
		if attached.ToolResult == nil || *attached.ToolResult != result {
			t.Fatalf("attached.ToolResult = %v, want %q", attached.ToolResult, result)
		}
		if attached.FinishedAt == nil {
			t.Fatal("attached.FinishedAt = nil, want set")
		}
		if attached.ResultSeq == nil {
			t.Fatal("attached.ResultSeq = nil, want set")
		}
	})

	t.Run("exact tool_call_id match does not re-attach to an already-finished call", func(t *testing.T) {
		callID := "call-exact-finished"
		if _, err := InsertAgentToolCall(ctx, pool, InsertAgentToolCallParams{
			AgentSessionID: session.ID,
			Seq:            nextSeq(),
			ToolCallID:     &callID,
			ToolName:       "bash",
		}); err != nil {
			t.Fatalf("InsertAgentToolCall() error = %v", err)
		}

		firstResult := "first"
		if _, err := AttachAgentToolResult(ctx, pool, AttachAgentToolResultParams{
			AgentSessionID: session.ID,
			ToolCallID:     &callID,
			ToolName:       "bash",
			ResultSeq:      nextSeq(),
			Result:         &firstResult,
		}); err != nil {
			t.Fatalf("AttachAgentToolResult() first attach error = %v", err)
		}

		secondResult := "second"
		_, err := AttachAgentToolResult(ctx, pool, AttachAgentToolResultParams{
			AgentSessionID: session.ID,
			ToolCallID:     &callID,
			ToolName:       "bash",
			ResultSeq:      nextSeq(),
			Result:         &secondResult,
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("AttachAgentToolResult() re-attach to finished call error = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("no matching tool_call_id is not found", func(t *testing.T) {
		missing := "call-does-not-exist"
		result := "irrelevant"
		_, err := AttachAgentToolResult(ctx, pool, AttachAgentToolResultParams{
			AgentSessionID: session.ID,
			ToolCallID:     &missing,
			ToolName:       "bash",
			ResultSeq:      nextSeq(),
			Result:         &result,
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("AttachAgentToolResult() error = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("fallback picks the newest unfinished same-name call deterministically", func(t *testing.T) {
		toolName := fmt.Sprintf("read-%d", session.ID)

		older, err := InsertAgentToolCall(ctx, pool, InsertAgentToolCallParams{
			AgentSessionID: session.ID,
			Seq:            nextSeq(),
			ToolName:       toolName,
		})
		if err != nil {
			t.Fatalf("InsertAgentToolCall() older error = %v", err)
		}
		newer, err := InsertAgentToolCall(ctx, pool, InsertAgentToolCallParams{
			AgentSessionID: session.ID,
			Seq:            nextSeq(),
			ToolName:       toolName,
		})
		if err != nil {
			t.Fatalf("InsertAgentToolCall() newer error = %v", err)
		}

		firstResult := "first"
		firstAttached, err := AttachAgentToolResult(ctx, pool, AttachAgentToolResultParams{
			AgentSessionID: session.ID,
			ToolName:       toolName,
			ResultSeq:      nextSeq(),
			Result:         &firstResult,
		})
		if err != nil {
			t.Fatalf("AttachAgentToolResult() first fallback error = %v", err)
		}
		if firstAttached.ID != newer.ID {
			t.Fatalf("first fallback attached.ID = %d, want newest call %d (older = %d)", firstAttached.ID, newer.ID, older.ID)
		}

		secondResult := "second"
		secondAttached, err := AttachAgentToolResult(ctx, pool, AttachAgentToolResultParams{
			AgentSessionID: session.ID,
			ToolName:       toolName,
			ResultSeq:      nextSeq(),
			Result:         &secondResult,
		})
		if err != nil {
			t.Fatalf("AttachAgentToolResult() second fallback error = %v", err)
		}
		if secondAttached.ID != older.ID {
			t.Fatalf("second fallback attached.ID = %d, want remaining unfinished call %d (newer = %d)", secondAttached.ID, older.ID, newer.ID)
		}

		thirdResult := "third"
		_, err = AttachAgentToolResult(ctx, pool, AttachAgentToolResultParams{
			AgentSessionID: session.ID,
			ToolName:       toolName,
			ResultSeq:      nextSeq(),
			Result:         &thirdResult,
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("third fallback (no unfinished calls left) error = %v, want %v", err, ErrNotFound)
		}
	})

	t.Run("fallback is concurrency-safe: concurrent results never attach to the same call", func(t *testing.T) {
		toolName := fmt.Sprintf("concurrent-read-%d", session.ID)

		const n = 8
		callIDs := make([]int64, n)
		for i := 0; i < n; i++ {
			call, err := InsertAgentToolCall(ctx, pool, InsertAgentToolCallParams{
				AgentSessionID: session.ID,
				Seq:            nextSeq(),
				ToolName:       toolName,
			})
			if err != nil {
				t.Fatalf("InsertAgentToolCall() error = %v", err)
			}
			callIDs[i] = call.ID
		}

		var (
			wg        sync.WaitGroup
			mu        sync.Mutex
			attached  []int64
			notFounds int
		)
		for i := 0; i < n; i++ {
			wg.Add(1)
			resultSeq := nextSeq()
			go func() {
				defer wg.Done()
				result := "ok"
				c, err := AttachAgentToolResult(context.Background(), pool, AttachAgentToolResultParams{
					AgentSessionID: session.ID,
					ToolName:       toolName,
					ResultSeq:      resultSeq,
					Result:         &result,
				})
				mu.Lock()
				defer mu.Unlock()
				if errors.Is(err, ErrNotFound) {
					notFounds++
					return
				}
				if err != nil {
					t.Errorf("AttachAgentToolResult() error = %v", err)
					return
				}
				attached = append(attached, c.ID)
			}()
		}
		wg.Wait()

		if notFounds != 0 {
			t.Fatalf("got %d ErrNotFound results, want 0 (exactly one unfinished call per concurrent attach)", notFounds)
		}
		if len(attached) != n {
			t.Fatalf("got %d successful attaches, want %d", len(attached), n)
		}
		seen := make(map[int64]bool, n)
		for _, id := range attached {
			if seen[id] {
				t.Fatalf("AttachAgentToolResult() attached two concurrent results to the same call %d: %v", id, attached)
			}
			seen[id] = true
		}
	})
}

func TestAgentSessionUsageAndClose_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "agent-telemetry-usage-close",
		SourceSessionID: "agent-telemetry-usage-close-source",
		Origin:          "cli",
	})

	updated, err := AddAgentSessionUsage(ctx, pool, session.ID, AgentSessionUsageDelta{
		InputTokens:   10,
		OutputTokens:  20,
		CostUSD:       1.5,
		ToolCallCount: 1,
		TurnCount:     1,
	})
	if err != nil {
		t.Fatalf("AddAgentSessionUsage() error = %v", err)
	}
	if updated.InputTokens != 10 || updated.OutputTokens != 20 || updated.ToolCallCount != 1 || updated.TurnCount != 1 {
		t.Fatalf("AddAgentSessionUsage() first call = %+v, want tokens/counts set from zero", updated)
	}

	updated, err = AddAgentSessionUsage(ctx, pool, session.ID, AgentSessionUsageDelta{
		InputTokens:   5,
		ToolCallCount: 2,
	})
	if err != nil {
		t.Fatalf("AddAgentSessionUsage() second call error = %v", err)
	}
	if updated.InputTokens != 15 {
		t.Fatalf("InputTokens = %d, want 15 (accumulated)", updated.InputTokens)
	}
	if updated.OutputTokens != 20 {
		t.Fatalf("OutputTokens = %d, want unchanged 20", updated.OutputTokens)
	}
	if updated.ToolCallCount != 3 {
		t.Fatalf("ToolCallCount = %d, want 3 (accumulated)", updated.ToolCallCount)
	}

	if _, err := AddAgentSessionUsage(ctx, pool, int64(1)<<40, AgentSessionUsageDelta{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddAgentSessionUsage() on unknown id error = %v, want %v", err, ErrNotFound)
	}

	closed, err := CloseAgentSession(ctx, pool, session.ID, nil)
	if err != nil {
		t.Fatalf("CloseAgentSession() error = %v", err)
	}
	if closed.EndedAt == nil {
		t.Fatal("CloseAgentSession() EndedAt = nil, want set")
	}
	firstEndedAt := *closed.EndedAt

	closedAgain, err := CloseAgentSession(ctx, pool, session.ID, nil)
	if err != nil {
		t.Fatalf("CloseAgentSession() second call error = %v", err)
	}
	if closedAgain.EndedAt == nil || !closedAgain.EndedAt.Equal(firstEndedAt) {
		t.Fatalf("CloseAgentSession() second call EndedAt = %v, want unchanged %v", closedAgain.EndedAt, firstEndedAt)
	}

	if _, err := CloseAgentSession(ctx, pool, int64(1)<<40, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CloseAgentSession() on unknown id error = %v, want %v", err, ErrNotFound)
	}
}

// createTestPhaseForTelemetry creates a repo/spec/workflow/phase chain for
// use by phase-scoped telemetry read tests, cleaning up the repository and
// spec (which cascade to the workflow and phase) on test completion.
func createTestPhaseForTelemetry(t *testing.T, pool *pgxpool.Pool, suffix string) Phase {
	t.Helper()
	ctx := context.Background()

	repo := createTestPlanRepo(t, pool, suffix)

	spec, err := CreateSpec(ctx, pool, repo.ID, "spec for "+suffix, "content", []byte(`[]`))
	if err != nil {
		t.Fatalf("CreateSpec() error = %v", err)
	}
	t.Cleanup(func() { _ = DeleteSpec(context.Background(), pool, spec.ID) })

	wf, err := CreateWorkflow(ctx, pool, spec.ID, "poc", nil)
	if err != nil {
		t.Fatalf("CreateWorkflow() error = %v", err)
	}
	t.Cleanup(func() { _ = DeleteWorkflow(context.Background(), pool, wf.ID) })

	phase, err := CreatePhase(ctx, pool, wf.ID, 0, "phase-"+suffix, "goal-"+suffix, 60)
	if err != nil {
		t.Fatalf("CreatePhase() error = %v", err)
	}
	return phase
}

func TestListAgentSessionsByPhase_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	phase := createTestPhaseForTelemetry(t, pool, "telemetry-sessions")

	// An empty phase (no sessions yet) must return an empty, non-nil slice
	// rather than an error.
	empty, err := ListAgentSessionsByPhase(ctx, pool, phase.ID)
	if err != nil {
		t.Fatalf("ListAgentSessionsByPhase() on empty phase error = %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("ListAgentSessionsByPhase() on empty phase = %v, want empty", empty)
	}

	phaseID := phase.ID
	sessA := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "agent-telemetry-phase-sessions-a",
		SourceSessionID: "agent-telemetry-phase-sessions-a-source",
		Origin:          "cli",
		PhaseID:         &phaseID,
	})
	sessB := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "agent-telemetry-phase-sessions-b",
		SourceSessionID: "agent-telemetry-phase-sessions-b-source",
		Origin:          "cli",
		PhaseID:         &phaseID,
	})
	// A session belonging to a different (unset) phase must not be included.
	_ = createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "agent-telemetry-phase-sessions-other",
		SourceSessionID: "agent-telemetry-phase-sessions-other-source",
		Origin:          "cli",
	})

	got, err := ListAgentSessionsByPhase(ctx, pool, phase.ID)
	if err != nil {
		t.Fatalf("ListAgentSessionsByPhase() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListAgentSessionsByPhase() returned %d sessions, want 2: %+v", len(got), got)
	}
	if got[0].ID != sessA.ID || got[1].ID != sessB.ID {
		t.Fatalf("ListAgentSessionsByPhase() order = [%d, %d], want [%d, %d] (started_at, id)", got[0].ID, got[1].ID, sessA.ID, sessB.ID)
	}
	for _, s := range got {
		if s.PhaseID == nil || *s.PhaseID != phase.ID {
			t.Fatalf("session %d PhaseID = %v, want %d", s.ID, s.PhaseID, phase.ID)
		}
	}
}

func TestListAgentSessions_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	a := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "agent-telemetry-global-list-a",
		SourceSessionID: "agent-telemetry-global-list-a-source",
		Origin:          "cli",
	})
	b := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "agent-telemetry-global-list-b",
		SourceSessionID: "agent-telemetry-global-list-b-source",
		Origin:          "cli",
	})

	got, err := ListAgentSessions(ctx, pool)
	if err != nil {
		t.Fatalf("ListAgentSessions() error = %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("ListAgentSessions() = empty, want at least the two sessions just created")
	}

	var sawA, sawB bool
	for _, s := range got {
		switch s.ID {
		case a.ID:
			sawA = true
		case b.ID:
			sawB = true
		}
	}
	if !sawA || !sawB {
		t.Fatalf("ListAgentSessions() did not include both created sessions (sawA=%v sawB=%v): %+v", sawA, sawB, got)
	}
}

func TestListAgentSessionsPage_BoundedLifecycleAndCursor_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	fixture := fmt.Sprintf("agent-page-bounded-%d", time.Now().UnixNano())
	repo := createTestPlanRepo(t, pool, fixture)
	repoID := repo.ID
	base := time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)
	active := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session: fixture + "-active", SourceSessionID: fixture + "-active-source", Origin: "test",
		RepositoryID: &repoID, LifecycleState: "active", EventAt: &base,
	})
	later := base.Add(time.Minute)
	closed := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session: fixture + "-closed", SourceSessionID: fixture + "-closed-source", Origin: "test",
		RepositoryID: &repoID, LifecycleState: "active", EventAt: &later,
	})
	if _, err := CloseAgentSession(ctx, pool, closed.ID, &later); err != nil {
		t.Fatalf("CloseAgentSession() error = %v", err)
	}

	got, err := ListAgentSessionsPage(ctx, pool, AgentSessionPageParams{
		Limit: 1, Lifecycle: "closed", RepositoryID: &repoID,
	})
	if err != nil {
		t.Fatalf("ListAgentSessionsPage() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != closed.ID {
		t.Fatalf("closed page = %+v, want fixture row %d", got, closed.ID)
	}
	beforeID := closed.ID
	got, err = ListAgentSessionsPage(ctx, pool, AgentSessionPageParams{
		Limit: 10, BeforeAt: &later, BeforeID: &beforeID, RepositoryID: &repoID,
	})
	if err != nil {
		t.Fatalf("ListAgentSessionsPage(cursor) error = %v", err)
	}
	if len(got) != 1 || got[0].ID != active.ID {
		t.Fatalf("cursor page = %+v, want fixture row %d", got, active.ID)
	}
	if got[0].LastEventAt.After(later) || (got[0].LastEventAt.Equal(later) && got[0].ID >= beforeID) {
		t.Fatalf("cursor leaked newer session: %+v", got[0])
	}
}

func TestListAgentSessionsPage_LifecycleAppliedBeforeLimit_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	fixture := fmt.Sprintf("agent-page-lifecycle-%d", time.Now().UnixNano())
	repo := createTestPlanRepo(t, pool, fixture)
	repoID := repo.ID
	closedAt := time.Date(2090, 1, 1, 0, 0, 0, 0, time.UTC)
	closed := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session: fixture + "-old-closed", SourceSessionID: fixture + "-old-closed-source",
		Origin: "test", RepositoryID: &repoID, LifecycleState: "active", EventAt: &closedAt,
	})
	if _, err := CloseAgentSession(ctx, pool, closed.ID, &closedAt); err != nil {
		t.Fatalf("CloseAgentSession() error = %v", err)
	}

	// More than one historical UI fetch worth of newer, non-matching rows
	// must not hide the closed row: lifecycle belongs in SQL before LIMIT.
	for i := 0; i < 52; i++ {
		at := closedAt.Add(time.Duration(i+1) * time.Minute)
		createTestAgentSession(t, pool, EnsureAgentSessionParams{
			Session:         fmt.Sprintf("%s-new-active-%02d", fixture, i),
			SourceSessionID: fmt.Sprintf("%s-new-active-%02d-source", fixture, i),
			Origin:          "test", RepositoryID: &repoID, LifecycleState: "active", EventAt: &at,
		})
	}

	got, err := ListAgentSessionsPage(ctx, pool, AgentSessionPageParams{
		Limit: 1, Lifecycle: "closed", RepositoryID: &repoID,
	})
	if err != nil {
		t.Fatalf("ListAgentSessionsPage() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != closed.ID {
		t.Fatalf("closed page = %+v, want older matching row %d despite 52 newer active rows", got, closed.ID)
	}
}

func TestListAgentTurnsToolCallsMessagesBySession_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	session := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "agent-telemetry-phase-detail",
		SourceSessionID: "agent-telemetry-phase-detail-source",
		Origin:          "cli",
	})

	nextSeq := func() int64 {
		seq, err := AllocateAgentSessionSeq(ctx, pool, session.ID)
		if err != nil {
			t.Fatalf("AllocateAgentSessionSeq() error = %v", err)
		}
		return seq
	}

	// Before any writes, all three list functions must return empty, non-nil
	// slices rather than errors.
	if turns, err := ListAgentTurnsBySession(ctx, pool, session.ID); err != nil || len(turns) != 0 {
		t.Fatalf("ListAgentTurnsBySession() on empty session = (%v, %v), want (empty, nil)", turns, err)
	}
	if calls, err := ListAgentToolCallsBySession(ctx, pool, session.ID); err != nil || len(calls) != 0 {
		t.Fatalf("ListAgentToolCallsBySession() on empty session = (%v, %v), want (empty, nil)", calls, err)
	}
	if msgs, err := ListAgentMessagesBySession(ctx, pool, session.ID); err != nil || len(msgs) != 0 {
		t.Fatalf("ListAgentMessagesBySession() on empty session = (%v, %v), want (empty, nil)", msgs, err)
	}

	turnIndex := int64(12)
	sourceMessageID := "assistant-correlation-12"
	turn1, err := InsertAgentTurn(ctx, pool, InsertAgentTurnParams{
		AgentSessionID: session.ID, Seq: nextSeq(), TurnIndex: &turnIndex, SourceMessageID: &sourceMessageID,
	})
	if err != nil {
		t.Fatalf("InsertAgentTurn() error = %v", err)
	}
	turn2, err := InsertAgentTurn(ctx, pool, InsertAgentTurnParams{AgentSessionID: session.ID, Seq: nextSeq()})
	if err != nil {
		t.Fatalf("InsertAgentTurn() error = %v", err)
	}

	// Tool call with all nullable metadata left unset (no tool_call_id, no
	// tool_input, and never attached with a result).
	bareCall, err := InsertAgentToolCall(ctx, pool, InsertAgentToolCallParams{
		AgentSessionID: session.ID,
		Seq:            nextSeq(),
		ToolName:       "bash",
	})
	if err != nil {
		t.Fatalf("InsertAgentToolCall() error = %v", err)
	}

	// Tool call with metadata populated and a result attached.
	callID := "call-phase-detail"
	input := `{"cmd":"ls"}`
	fullCall, err := InsertAgentToolCall(ctx, pool, InsertAgentToolCallParams{
		AgentSessionID: session.ID,
		Seq:            nextSeq(),
		ToolCallID:     &callID,
		ToolName:       "bash",
		ToolInput:      &input,
	})
	if err != nil {
		t.Fatalf("InsertAgentToolCall() error = %v", err)
	}
	result := "ok"
	if _, err := AttachAgentToolResult(ctx, pool, AttachAgentToolResultParams{
		AgentSessionID: session.ID,
		ToolCallID:     &callID,
		ToolName:       "bash",
		ResultSeq:      nextSeq(),
		Result:         &result,
	}); err != nil {
		t.Fatalf("AttachAgentToolResult() error = %v", err)
	}

	// Message with nullable content left unset.
	bareMsg, err := InsertAgentMessage(ctx, pool, InsertAgentMessageParams{
		AgentSessionID: session.ID,
		Seq:            nextSeq(),
		Role:           "system",
	})
	if err != nil {
		t.Fatalf("InsertAgentMessage() error = %v", err)
	}
	content := "hello"
	fullMsg, err := InsertAgentMessage(ctx, pool, InsertAgentMessageParams{
		AgentSessionID:  session.ID,
		Seq:             nextSeq(),
		Role:            "assistant",
		SourceMessageID: &sourceMessageID,
		TurnIndex:       &turnIndex,
		Content:         &content,
	})
	if err != nil {
		t.Fatalf("InsertAgentMessage() error = %v", err)
	}

	turns, err := ListAgentTurnsBySession(ctx, pool, session.ID)
	if err != nil {
		t.Fatalf("ListAgentTurnsBySession() error = %v", err)
	}
	if len(turns) != 2 || turns[0].ID != turn1.ID || turns[1].ID != turn2.ID {
		t.Fatalf("ListAgentTurnsBySession() = %+v, want [%d, %d] in seq order", turns, turn1.ID, turn2.ID)
	}
	if turns[0].TurnIndex == nil || *turns[0].TurnIndex != turnIndex || turns[0].SourceMessageID == nil || *turns[0].SourceMessageID != sourceMessageID {
		t.Fatalf("turn correlation = %+v, want turn %d/message %q", turns[0], turnIndex, sourceMessageID)
	}

	calls, err := ListAgentToolCallsBySession(ctx, pool, session.ID)
	if err != nil {
		t.Fatalf("ListAgentToolCallsBySession() error = %v", err)
	}
	if len(calls) != 2 || calls[0].ID != bareCall.ID || calls[1].ID != fullCall.ID {
		t.Fatalf("ListAgentToolCallsBySession() = %+v, want [%d, %d] in seq order", calls, bareCall.ID, fullCall.ID)
	}
	if calls[0].ToolCallID != nil || calls[0].ToolInput != nil || calls[0].ToolResult != nil || calls[0].FinishedAt != nil {
		t.Fatalf("calls[0] (bare) = %+v, want all nullable metadata unset", calls[0])
	}
	if calls[1].ToolCallID == nil || *calls[1].ToolCallID != callID {
		t.Fatalf("calls[1].ToolCallID = %v, want %q", calls[1].ToolCallID, callID)
	}
	if calls[1].ToolResult == nil || *calls[1].ToolResult != result {
		t.Fatalf("calls[1].ToolResult = %v, want %q", calls[1].ToolResult, result)
	}
	if calls[1].FinishedAt == nil {
		t.Fatal("calls[1].FinishedAt = nil, want set")
	}

	msgs, err := ListAgentMessagesBySession(ctx, pool, session.ID)
	if err != nil {
		t.Fatalf("ListAgentMessagesBySession() error = %v", err)
	}
	if len(msgs) != 2 || msgs[0].ID != bareMsg.ID || msgs[1].ID != fullMsg.ID {
		t.Fatalf("ListAgentMessagesBySession() = %+v, want [%d, %d] in seq order", msgs, bareMsg.ID, fullMsg.ID)
	}
	if msgs[0].Content != nil {
		t.Fatalf("msgs[0].Content = %v, want nil", msgs[0].Content)
	}
	if msgs[1].Content == nil || *msgs[1].Content != content {
		t.Fatalf("msgs[1].Content = %v, want %q", msgs[1].Content, content)
	}
	if msgs[1].TurnIndex == nil || *msgs[1].TurnIndex != turnIndex || msgs[1].SourceMessageID == nil || *msgs[1].SourceMessageID != sourceMessageID {
		t.Fatalf("message correlation = %+v, want turn %d/message %q", msgs[1], turnIndex, sourceMessageID)
	}

	// Turns, tool calls, and messages belonging to a different session must
	// not leak into this session's results.
	other := createTestAgentSession(t, pool, EnsureAgentSessionParams{
		Session:         "agent-telemetry-phase-detail-other",
		SourceSessionID: "agent-telemetry-phase-detail-other-source",
		Origin:          "cli",
	})
	otherSeq, err := AllocateAgentSessionSeq(ctx, pool, other.ID)
	if err != nil {
		t.Fatalf("AllocateAgentSessionSeq() error = %v", err)
	}
	if _, err := InsertAgentTurn(ctx, pool, InsertAgentTurnParams{AgentSessionID: other.ID, Seq: otherSeq}); err != nil {
		t.Fatalf("InsertAgentTurn() error = %v", err)
	}

	turns, err = ListAgentTurnsBySession(ctx, pool, session.ID)
	if err != nil {
		t.Fatalf("ListAgentTurnsBySession() error = %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("ListAgentTurnsBySession() leaked cross-session rows: got %d, want 2", len(turns))
	}
}
