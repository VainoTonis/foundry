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
