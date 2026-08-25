package httpapi

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/tonis2/foundry/internal/db"
)

// errBoom is a stand-in for a hash-computation failure, used to assert
// that planReviewWarnings conservatively treats an unresolvable
// staleness check as stale rather than silently reporting current.
var errBoom = errors.New("boom")

func passReview(hash string, unavailable string) db.PlanReview {
	report := `{"verdict":"pass","pass1":"ok","pass2":"ok","evidence":"none","uncertainties":"none","unavailable_repositories":` + unavailable + `}`
	return db.PlanReview{
		Status:              db.PlanReviewStatusCompleted,
		Verdict:             strPtr(db.PlanReviewVerdictPass),
		InputSnapshotSHA256: hash,
		Report:              json.RawMessage(report),
	}
}

// TestPlanReviewWarningsCoversEveryState covers every advisory review
// state step 7 requires a warning for: missing, incomplete (queued or
// running), stale, revise, escalate, failed, incompletely grounded, and
// the passing/current/fully-grounded case that must suppress every
// warning.
func TestPlanReviewWarningsCoversEveryState(t *testing.T) {
	cases := []struct {
		name        string
		reviews     []db.PlanReview
		currentHash string
		hashErr     error
		wantCodes   []string
	}{
		{
			name:      "missing: no review exists",
			reviews:   nil,
			wantCodes: []string{planReviewWarningMissing},
		},
		{
			name: "incomplete: latest review still queued",
			reviews: []db.PlanReview{
				{Status: db.PlanReviewStatusQueued},
			},
			wantCodes: []string{planReviewWarningIncomplete},
		},
		{
			name: "incomplete: latest review still running",
			reviews: []db.PlanReview{
				{Status: db.PlanReviewStatusRunning},
			},
			wantCodes: []string{planReviewWarningIncomplete},
		},
		{
			name: "failed: latest review failed",
			reviews: []db.PlanReview{
				{Status: db.PlanReviewStatusFailed, Error: strPtr("cerberus turn: boom")},
			},
			wantCodes: []string{planReviewWarningFailed},
		},
		{
			name: "stale: completed review hash no longer matches",
			reviews: []db.PlanReview{
				passReview("old-hash", "[]"),
			},
			currentHash: "new-hash",
			wantCodes:   []string{planReviewWarningStale},
		},
		{
			name: "stale: hash could not be computed",
			reviews: []db.PlanReview{
				passReview("hash", "[]"),
			},
			currentHash: "hash",
			hashErr:     errBoom,
			wantCodes:   []string{planReviewWarningStale},
		},
		{
			name: "revise: unresolved verdict",
			reviews: []db.PlanReview{
				{
					Status:              db.PlanReviewStatusCompleted,
					Verdict:             strPtr(db.PlanReviewVerdictRevise),
					InputSnapshotSHA256: "hash",
					Report:              json.RawMessage(`{"unavailable_repositories":[]}`),
				},
			},
			currentHash: "hash",
			wantCodes:   []string{planReviewWarningUnresolved},
		},
		{
			name: "escalate: unresolved verdict",
			reviews: []db.PlanReview{
				{
					Status:              db.PlanReviewStatusCompleted,
					Verdict:             strPtr(db.PlanReviewVerdictEscalate),
					InputSnapshotSHA256: "hash",
					Report:              json.RawMessage(`{"unavailable_repositories":[]}`),
				},
			},
			currentHash: "hash",
			wantCodes:   []string{planReviewWarningUnresolved},
		},
		{
			name: "ungrounded: completed pass review discloses unavailable repositories",
			reviews: []db.PlanReview{
				passReview("hash", `["cerberus"]`),
			},
			currentHash: "hash",
			wantCodes:   []string{planReviewWarningUngrounded},
		},
		{
			name: "ungrounded: disclosed as a non-empty string instead of an array",
			reviews: []db.PlanReview{
				passReview("hash", `"cerberus repo unreachable"`),
			},
			currentHash: "hash",
			wantCodes:   []string{planReviewWarningUngrounded},
		},
		{
			name: "stale and ungrounded combine",
			reviews: []db.PlanReview{
				passReview("old-hash", `["cerberus"]`),
			},
			currentHash: "new-hash",
			wantCodes:   []string{planReviewWarningStale, planReviewWarningUngrounded},
		},
		{
			name: "pass: current and fully grounded suppresses every warning",
			reviews: []db.PlanReview{
				passReview("hash", "[]"),
			},
			currentHash: "hash",
			wantCodes:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planReviewWarnings(tc.reviews, tc.currentHash, tc.hashErr)
			if len(got) != len(tc.wantCodes) {
				t.Fatalf("planReviewWarnings() = %+v, want codes %v", got, tc.wantCodes)
			}
			for i, code := range tc.wantCodes {
				if got[i].Code != code {
					t.Fatalf("planReviewWarnings()[%d].Code = %q, want %q (full: %+v)", i, got[i].Code, code, got)
				}
				if got[i].Message == "" {
					t.Fatalf("planReviewWarnings()[%d].Message is empty, want a human-readable message", i)
				}
			}
		})
	}
}

// TestReportHasUnavailableGrounding covers every report shape
// reportHasUnavailableGrounding must treat as either a disclosed
// grounding gap or none, including malformed and absent input that
// must never be mistaken for a disclosed gap.
func TestReportHasUnavailableGrounding(t *testing.T) {
	cases := []struct {
		name   string
		report json.RawMessage
		want   bool
	}{
		{"nil report", nil, false},
		{"empty report", json.RawMessage(``), false},
		{"malformed report", json.RawMessage(`not json`), false},
		{"field absent", json.RawMessage(`{}`), false},
		{"null", json.RawMessage(`{"unavailable_repositories":null}`), false},
		{"empty array", json.RawMessage(`{"unavailable_repositories":[]}`), false},
		{"empty string", json.RawMessage(`{"unavailable_repositories":""}`), false},
		{"non-empty array", json.RawMessage(`{"unavailable_repositories":["cerberus"]}`), true},
		{"non-empty string", json.RawMessage(`{"unavailable_repositories":"cerberus repo unreachable"}`), true},
		{"whitespace-only string", json.RawMessage(`{"unavailable_repositories":"   "}`), false},
		{"unexpected object shape is conservatively treated as disclosed", json.RawMessage(`{"unavailable_repositories":{"repo":"cerberus"}}`), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reportHasUnavailableGrounding(tc.report); got != tc.want {
				t.Fatalf("reportHasUnavailableGrounding(%s) = %v, want %v", tc.report, got, tc.want)
			}
		})
	}
}
