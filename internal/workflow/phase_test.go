package workflow

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBestEffortCloseSessionClosesResolvedSession(t *testing.T) {
	endedAt := time.Now()
	var lookedUp string
	var closedID int64
	var closedEndedAt *time.Time

	bestEffortCloseSession(context.Background(),
		func(ctx context.Context, session string) (int64, error) {
			lookedUp = session
			return 42, nil
		},
		func(ctx context.Context, agentSessionID int64, e *time.Time) error {
			closedID = agentSessionID
			closedEndedAt = e
			return nil
		},
		"foundry-w1-p2", endedAt,
	)

	if lookedUp != "foundry-w1-p2" {
		t.Fatalf("lookedUp = %q, want %q", lookedUp, "foundry-w1-p2")
	}
	if closedID != 42 {
		t.Fatalf("closedID = %d, want 42", closedID)
	}
	if closedEndedAt == nil || !closedEndedAt.Equal(endedAt) {
		t.Fatalf("closedEndedAt = %v, want %v", closedEndedAt, endedAt)
	}
}

func TestBestEffortCloseSessionIgnoresLookupFailure(t *testing.T) {
	closeCalled := false
	bestEffortCloseSession(context.Background(),
		func(ctx context.Context, session string) (int64, error) {
			return 0, errors.New("no such session")
		},
		func(ctx context.Context, agentSessionID int64, e *time.Time) error {
			closeCalled = true
			return nil
		},
		"unknown-session", time.Now(),
	)
	if closeCalled {
		t.Fatal("closeSession was called despite a failed lookup")
	}
}

func TestBestEffortCloseSessionIgnoresCloseFailure(t *testing.T) {
	bestEffortCloseSession(context.Background(),
		func(ctx context.Context, session string) (int64, error) {
			return 1, nil
		},
		func(ctx context.Context, agentSessionID int64, e *time.Time) error {
			return errors.New("close failed")
		},
		"session", time.Now(),
	)
}

func TestBestEffortCloseSessionNoopWithoutSessionOrHelpers(t *testing.T) {
	called := false
	lookup := func(ctx context.Context, session string) (int64, error) {
		called = true
		return 0, nil
	}
	closeFn := func(ctx context.Context, agentSessionID int64, e *time.Time) error {
		called = true
		return nil
	}

	bestEffortCloseSession(context.Background(), lookup, closeFn, "", time.Now())
	bestEffortCloseSession(context.Background(), nil, closeFn, "session", time.Now())
	bestEffortCloseSession(context.Background(), lookup, nil, "session", time.Now())

	if called {
		t.Fatal("lookup/closeSession invoked despite missing session or helper")
	}
}

func TestCloseManagedSessionNoopWithoutPool(t *testing.T) {
	r := &Runner{}
	// Must not panic when no pool is configured (e.g. in unit tests that
	// never reach a real database), regardless of session name.
	r.closeManagedSession("foundry-w1-p1")
}
