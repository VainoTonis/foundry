package db

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

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

	turn1, err := InsertAgentTurn(ctx, pool, InsertAgentTurnParams{AgentSessionID: session.ID, Seq: nextSeq()})
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
		AgentSessionID: session.ID,
		Seq:            nextSeq(),
		Role:           "assistant",
		Content:        &content,
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
