package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tonis2/foundry/internal/repository"
)

// createTestFeedbackRepo is a small wrapper around createTestRepository
// that gives each repository a unique name derived from a caller-supplied
// suffix, so feedback tests in this file can freely create several
// repositories per subtest without name collisions.
func createTestFeedbackRepo(t *testing.T, pool *pgxpool.Pool, suffix string) repository.Repository {
	t.Helper()
	remote := "https://example.com/foo/feedback-" + suffix + ".git"
	return createTestRepository(t, pool, repository.Repository{
		Name:      "feedback-repo-" + suffix,
		RemoteURL: &remote,
	})
}

func countFeedbackByBody(t *testing.T, pool *pgxpool.Pool, body string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM feedback WHERE body = $1`, body).Scan(&n); err != nil {
		t.Fatalf("count feedback by body %q: %v", body, err)
	}
	return n
}

func countFeedbackRepositoriesForRepository(t *testing.T, pool *pgxpool.Pool, repositoryID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM feedback_repositories WHERE repository_id = $1`, repositoryID).Scan(&n); err != nil {
		t.Fatalf("count feedback_repositories for repository %d: %v", repositoryID, err)
	}
	return n
}

func deleteTestFeedback(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DELETE FROM feedback WHERE id = $1`, id); err != nil {
		t.Errorf("cleanup delete feedback %d: %v", id, err)
	}
}

// TestCreateFeedback_RepositoryIDs_Postgres exercises CreateFeedback's
// repository id validation and transactional rollback behavior: an empty
// repository id list, a duplicate repository id, and an unknown
// repository id must all fail without persisting a feedback row or any
// feedback_repositories rows. A valid list must persist a linked feedback
// row and matching feedback_repositories rows.
func TestCreateFeedback_RepositoryIDs_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	t.Run("empty repository id list errors and persists nothing", func(t *testing.T) {
		body := "feedback-empty-repo-ids"
		_, err := CreateFeedback(ctx, pool, body, "model", "session", nil)
		if err == nil {
			t.Fatal("CreateFeedback() error = nil, want an error for an empty repository id list")
		}
		if got := countFeedbackByBody(t, pool, body); got != 0 {
			t.Fatalf("feedback with body %q after failed CreateFeedback() = %d, want 0", body, got)
		}
	})

	t.Run("duplicate repository id errors and persists nothing", func(t *testing.T) {
		repo := createTestFeedbackRepo(t, pool, "dup")

		body := "feedback-duplicate-repo-id"
		_, err := CreateFeedback(ctx, pool, body, "model", "session", []int64{repo.ID, repo.ID})
		if err == nil {
			t.Fatal("CreateFeedback() error = nil, want an error for a duplicated repository id")
		}
		if got := countFeedbackByBody(t, pool, body); got != 0 {
			t.Fatalf("feedback with body %q after failed CreateFeedback() = %d, want 0", body, got)
		}
		if got := countFeedbackRepositoriesForRepository(t, pool, repo.ID); got != 0 {
			t.Fatalf("feedback_repositories rows for repository %d = %d, want 0", repo.ID, got)
		}
	})

	t.Run("unknown repository id errors and rolls back the whole transaction", func(t *testing.T) {
		repo := createTestFeedbackRepo(t, pool, "unknown-sibling")
		const neverIssuedRepositoryID int64 = 1 << 40

		body := "feedback-unknown-repo-id"
		_, err := CreateFeedback(ctx, pool, body, "model", "session", []int64{repo.ID, neverIssuedRepositoryID})
		if err == nil {
			t.Fatal("CreateFeedback() error = nil, want an error for an unknown repository id")
		}
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("CreateFeedback() error = %v, want it to wrap ErrNotFound", err)
		}
		if got := countFeedbackByBody(t, pool, body); got != 0 {
			t.Fatalf("feedback with body %q after failed CreateFeedback() = %d, want 0", body, got)
		}
		if got := countFeedbackRepositoriesForRepository(t, pool, repo.ID); got != 0 {
			t.Fatalf("feedback_repositories rows for repository %d = %d, want 0 after rollback", repo.ID, got)
		}
	})

	t.Run("valid repository ids persist a linked feedback row and matching feedback_repositories rows", func(t *testing.T) {
		repoA := createTestFeedbackRepo(t, pool, "valid-a")
		repoB := createTestFeedbackRepo(t, pool, "valid-b")

		body := "feedback-valid-repo-ids"
		created, err := CreateFeedback(ctx, pool, body, "model", "session", []int64{repoA.ID, repoB.ID})
		if err != nil {
			t.Fatalf("CreateFeedback() error = %v", err)
		}
		t.Cleanup(func() { deleteTestFeedback(t, pool, created.ID) })

		if created.ScopeStatus != "linked" {
			t.Fatalf("CreateFeedback().ScopeStatus = %q, want %q", created.ScopeStatus, "linked")
		}
		assertFeedbackRepos(t, "CreateFeedback()", created.Repositories, repoA.ID, repoB.ID)

		list, err := ListFeedback(ctx, pool)
		if err != nil {
			t.Fatalf("ListFeedback() error = %v", err)
		}
		var found *Feedback
		for i := range list {
			if list[i].ID == created.ID {
				found = &list[i]
			}
		}
		if found == nil {
			t.Fatalf("ListFeedback() did not include feedback %d", created.ID)
		}
		assertFeedbackRepos(t, "ListFeedback()", found.Repositories, repoA.ID, repoB.ID)
	})
}

// TestCreateStructuredFeedback_RepositoryIDs_Postgres mirrors
// TestCreateFeedback_RepositoryIDs_Postgres for CreateStructuredFeedback.
func TestCreateStructuredFeedback_RepositoryIDs_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	baseInput := func(body string) StructuredFeedbackInput {
		return StructuredFeedbackInput{
			Body:      body,
			Model:     "model",
			SessionID: "session",
			Dimension: "prompt_quality",
			Target:    "orchestrator_prompt",
			Score:     3,
		}
	}

	t.Run("empty repository id list errors and persists nothing", func(t *testing.T) {
		body := "structured-feedback-empty-repo-ids"
		_, err := CreateStructuredFeedback(ctx, pool, baseInput(body), nil)
		if err == nil {
			t.Fatal("CreateStructuredFeedback() error = nil, want an error for an empty repository id list")
		}
		if got := countFeedbackByBody(t, pool, body); got != 0 {
			t.Fatalf("feedback with body %q after failed CreateStructuredFeedback() = %d, want 0", body, got)
		}
	})

	t.Run("duplicate repository id errors and persists nothing", func(t *testing.T) {
		repo := createTestFeedbackRepo(t, pool, "structured-dup")

		body := "structured-feedback-duplicate-repo-id"
		_, err := CreateStructuredFeedback(ctx, pool, baseInput(body), []int64{repo.ID, repo.ID})
		if err == nil {
			t.Fatal("CreateStructuredFeedback() error = nil, want an error for a duplicated repository id")
		}
		if got := countFeedbackByBody(t, pool, body); got != 0 {
			t.Fatalf("feedback with body %q after failed CreateStructuredFeedback() = %d, want 0", body, got)
		}
		if got := countFeedbackRepositoriesForRepository(t, pool, repo.ID); got != 0 {
			t.Fatalf("feedback_repositories rows for repository %d = %d, want 0", repo.ID, got)
		}
	})

	t.Run("unknown repository id errors and rolls back the whole transaction", func(t *testing.T) {
		repo := createTestFeedbackRepo(t, pool, "structured-unknown-sibling")
		const neverIssuedRepositoryID int64 = 1 << 40

		body := "structured-feedback-unknown-repo-id"
		_, err := CreateStructuredFeedback(ctx, pool, baseInput(body), []int64{repo.ID, neverIssuedRepositoryID})
		if err == nil {
			t.Fatal("CreateStructuredFeedback() error = nil, want an error for an unknown repository id")
		}
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("CreateStructuredFeedback() error = %v, want it to wrap ErrNotFound", err)
		}
		if got := countFeedbackByBody(t, pool, body); got != 0 {
			t.Fatalf("feedback with body %q after failed CreateStructuredFeedback() = %d, want 0", body, got)
		}
		if got := countFeedbackRepositoriesForRepository(t, pool, repo.ID); got != 0 {
			t.Fatalf("feedback_repositories rows for repository %d = %d, want 0 after rollback", repo.ID, got)
		}
	})

	t.Run("valid repository ids persist a linked feedback row and matching feedback_repositories rows", func(t *testing.T) {
		repoA := createTestFeedbackRepo(t, pool, "structured-valid-a")
		repoB := createTestFeedbackRepo(t, pool, "structured-valid-b")

		body := "structured-feedback-valid-repo-ids"
		created, err := CreateStructuredFeedback(ctx, pool, baseInput(body), []int64{repoA.ID, repoB.ID})
		if err != nil {
			t.Fatalf("CreateStructuredFeedback() error = %v", err)
		}
		t.Cleanup(func() { deleteTestFeedback(t, pool, created.ID) })

		if created.ScopeStatus != "linked" {
			t.Fatalf("CreateStructuredFeedback().ScopeStatus = %q, want %q", created.ScopeStatus, "linked")
		}
		assertFeedbackRepos(t, "CreateStructuredFeedback()", created.Repositories, repoA.ID, repoB.ID)
	})
}

// assertFeedbackRepos checks that repos is exactly the given repository ids
// (order-independent, since feedback repository membership is unordered).
func assertFeedbackRepos(t *testing.T, label string, repos []FeedbackRepository, want ...int64) {
	t.Helper()
	if len(repos) != len(want) {
		t.Fatalf("%s Repositories = %+v, want %d entries", label, repos, len(want))
	}
	wantSet := make(map[int64]bool, len(want))
	for _, id := range want {
		wantSet[id] = true
	}
	for _, r := range repos {
		if !wantSet[r.RepositoryID] {
			t.Fatalf("%s Repositories contains unexpected repository id %d", label, r.RepositoryID)
		}
		if r.Repository.ID != r.RepositoryID {
			t.Fatalf("%s Repository.ID = %d, want %d", label, r.Repository.ID, r.RepositoryID)
		}
	}
}

// TestListFeedbackFiltered_RepositoryID_Postgres confirms
// ListFeedbackFiltered's repositoryID filter only returns feedback linked
// to that repository, excluding both feedback linked to other
// repositories and legacy_unscoped feedback with no feedback_repositories
// rows at all.
func TestListFeedbackFiltered_RepositoryID_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	repoA := createTestFeedbackRepo(t, pool, "filter-a")
	repoB := createTestFeedbackRepo(t, pool, "filter-b")

	linkedToA, err := CreateFeedback(ctx, pool, "filter-linked-to-a", "model", "session", []int64{repoA.ID})
	if err != nil {
		t.Fatalf("CreateFeedback() error = %v", err)
	}
	t.Cleanup(func() { deleteTestFeedback(t, pool, linkedToA.ID) })

	linkedToB, err := CreateFeedback(ctx, pool, "filter-linked-to-b", "model", "session", []int64{repoB.ID})
	if err != nil {
		t.Fatalf("CreateFeedback() error = %v", err)
	}
	t.Cleanup(func() { deleteTestFeedback(t, pool, linkedToB.ID) })

	// Simulate a pre-existing legacy row: no feedback_repositories rows,
	// scope_status left at its 'legacy_unscoped' column default.
	var legacyID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO feedback (body, model, session_id) VALUES ($1, $2, $3) RETURNING id`,
		"filter-legacy-unscoped", "model", "session",
	).Scan(&legacyID); err != nil {
		t.Fatalf("insert legacy feedback: %v", err)
	}
	t.Cleanup(func() { deleteTestFeedback(t, pool, legacyID) })

	list, err := ListFeedbackFiltered(ctx, pool, "", "", "", repoA.ID)
	if err != nil {
		t.Fatalf("ListFeedbackFiltered() error = %v", err)
	}

	var gotIDs []int64
	for _, f := range list {
		gotIDs = append(gotIDs, f.ID)
	}
	foundLinkedToA := false
	for _, id := range gotIDs {
		if id == linkedToA.ID {
			foundLinkedToA = true
		}
		if id == linkedToB.ID {
			t.Fatalf("ListFeedbackFiltered(repositoryID=%d) included feedback %d linked to a different repository", repoA.ID, linkedToB.ID)
		}
		if id == legacyID {
			t.Fatalf("ListFeedbackFiltered(repositoryID=%d) included legacy_unscoped feedback %d with no repository links", repoA.ID, legacyID)
		}
	}
	if !foundLinkedToA {
		t.Fatalf("ListFeedbackFiltered(repositoryID=%d) did not include feedback %d linked to it", repoA.ID, linkedToA.ID)
	}
}

// TestCreateFeedback_AgentSessionID_Postgres verifies that CreateFeedback
// and CreateStructuredFeedback resolve agent_session_id from sessionID by
// looking up agent_sessions.source_session_id, and that a missing or
// non-matching sessionID leaves agent_session_id NULL without ever
// failing feedback creation.
func TestCreateFeedback_AgentSessionID_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	getFeedbackAgentSessionID := func(t *testing.T, id int64) *int64 {
		t.Helper()
		var agentSessionID *int64
		if err := pool.QueryRow(ctx, `SELECT agent_session_id FROM feedback WHERE id = $1`, id).Scan(&agentSessionID); err != nil {
			t.Fatalf("select agent_session_id for feedback %d: %v", id, err)
		}
		return agentSessionID
	}

	t.Run("CreateFeedback with matching sessionID sets agent_session_id", func(t *testing.T) {
		repo := createTestFeedbackRepo(t, pool, "agent-session-match")
		sourceSessionID := "feedback-agent-session-match-src"
		session := createTestAgentSession(t, pool, EnsureAgentSessionParams{
			Session:         "feedback-agent-session-match-sess",
			SourceSessionID: sourceSessionID,
			Origin:          "test",
		})

		body := "feedback-agent-session-match"
		created, err := CreateFeedback(ctx, pool, body, "model", sourceSessionID, []int64{repo.ID})
		if err != nil {
			t.Fatalf("CreateFeedback() error = %v", err)
		}
		t.Cleanup(func() { deleteTestFeedback(t, pool, created.ID) })

		if created.AgentSessionID == nil || *created.AgentSessionID != session.ID {
			t.Fatalf("CreateFeedback().AgentSessionID = %v, want %d", created.AgentSessionID, session.ID)
		}
		if got := getFeedbackAgentSessionID(t, created.ID); got == nil || *got != session.ID {
			t.Fatalf("feedback.agent_session_id in db = %v, want %d", got, session.ID)
		}
	})

	t.Run("CreateFeedback with empty sessionID succeeds with NULL agent_session_id", func(t *testing.T) {
		repo := createTestFeedbackRepo(t, pool, "agent-session-empty")

		body := "feedback-agent-session-empty"
		created, err := CreateFeedback(ctx, pool, body, "model", "", []int64{repo.ID})
		if err != nil {
			t.Fatalf("CreateFeedback() error = %v", err)
		}
		t.Cleanup(func() { deleteTestFeedback(t, pool, created.ID) })

		if created.AgentSessionID != nil {
			t.Fatalf("CreateFeedback().AgentSessionID = %v, want nil", created.AgentSessionID)
		}
		if got := getFeedbackAgentSessionID(t, created.ID); got != nil {
			t.Fatalf("feedback.agent_session_id in db = %v, want NULL", got)
		}
	})

	t.Run("CreateFeedback with non-matching sessionID succeeds with NULL agent_session_id", func(t *testing.T) {
		repo := createTestFeedbackRepo(t, pool, "agent-session-no-match")

		body := "feedback-agent-session-no-match"
		created, err := CreateFeedback(ctx, pool, body, "model", "no-such-source-session-id", []int64{repo.ID})
		if err != nil {
			t.Fatalf("CreateFeedback() error = %v", err)
		}
		t.Cleanup(func() { deleteTestFeedback(t, pool, created.ID) })

		if created.AgentSessionID != nil {
			t.Fatalf("CreateFeedback().AgentSessionID = %v, want nil", created.AgentSessionID)
		}
		if got := getFeedbackAgentSessionID(t, created.ID); got != nil {
			t.Fatalf("feedback.agent_session_id in db = %v, want NULL", got)
		}
	})

	t.Run("CreateStructuredFeedback with matching sessionID sets agent_session_id", func(t *testing.T) {
		repo := createTestFeedbackRepo(t, pool, "agent-session-structured-match")
		sourceSessionID := "feedback-agent-session-structured-match-src"
		session := createTestAgentSession(t, pool, EnsureAgentSessionParams{
			Session:         "feedback-agent-session-structured-match-sess",
			SourceSessionID: sourceSessionID,
			Origin:          "test",
		})

		body := "structured-feedback-agent-session-match"
		in := StructuredFeedbackInput{
			Body:      body,
			Model:     "model",
			SessionID: sourceSessionID,
			Dimension: "prompt_quality",
			Target:    "orchestrator_prompt",
			Score:     3,
		}
		created, err := CreateStructuredFeedback(ctx, pool, in, []int64{repo.ID})
		if err != nil {
			t.Fatalf("CreateStructuredFeedback() error = %v", err)
		}
		t.Cleanup(func() { deleteTestFeedback(t, pool, created.ID) })

		if created.AgentSessionID == nil || *created.AgentSessionID != session.ID {
			t.Fatalf("CreateStructuredFeedback().AgentSessionID = %v, want %d", created.AgentSessionID, session.ID)
		}
		if got := getFeedbackAgentSessionID(t, created.ID); got == nil || *got != session.ID {
			t.Fatalf("feedback.agent_session_id in db = %v, want %d", got, session.ID)
		}
	})

	t.Run("CreateStructuredFeedback with empty sessionID succeeds with NULL agent_session_id", func(t *testing.T) {
		repo := createTestFeedbackRepo(t, pool, "agent-session-structured-empty")

		body := "structured-feedback-agent-session-empty"
		in := StructuredFeedbackInput{
			Body:      body,
			Model:     "model",
			SessionID: "",
			Dimension: "prompt_quality",
			Target:    "orchestrator_prompt",
			Score:     3,
		}
		created, err := CreateStructuredFeedback(ctx, pool, in, []int64{repo.ID})
		if err != nil {
			t.Fatalf("CreateStructuredFeedback() error = %v", err)
		}
		t.Cleanup(func() { deleteTestFeedback(t, pool, created.ID) })

		if created.AgentSessionID != nil {
			t.Fatalf("CreateStructuredFeedback().AgentSessionID = %v, want nil", created.AgentSessionID)
		}
		if got := getFeedbackAgentSessionID(t, created.ID); got != nil {
			t.Fatalf("feedback.agent_session_id in db = %v, want NULL", got)
		}
	})

	t.Run("CreateStructuredFeedback with non-matching sessionID succeeds with NULL agent_session_id", func(t *testing.T) {
		repo := createTestFeedbackRepo(t, pool, "agent-session-structured-no-match")

		body := "structured-feedback-agent-session-no-match"
		in := StructuredFeedbackInput{
			Body:      body,
			Model:     "model",
			SessionID: "no-such-source-session-id-structured",
			Dimension: "prompt_quality",
			Target:    "orchestrator_prompt",
			Score:     3,
		}
		created, err := CreateStructuredFeedback(ctx, pool, in, []int64{repo.ID})
		if err != nil {
			t.Fatalf("CreateStructuredFeedback() error = %v", err)
		}
		t.Cleanup(func() { deleteTestFeedback(t, pool, created.ID) })

		if created.AgentSessionID != nil {
			t.Fatalf("CreateStructuredFeedback().AgentSessionID = %v, want nil", created.AgentSessionID)
		}
		if got := getFeedbackAgentSessionID(t, created.ID); got != nil {
			t.Fatalf("feedback.agent_session_id in db = %v, want NULL", got)
		}
	})
}
