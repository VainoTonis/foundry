package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

func TestBuildPhaseFeedbackFromVerdictNotesFilesAndCommit(t *testing.T) {
	raw := buildPhaseFeedback("pass", "cerberus produced changes", []byte(`["internal/db/queries.go"]`), "abc123")
	var got struct {
		Result        string   `json:"result"`
		UsefulContext []string `json:"useful_context"`
		Problems      []string `json:"problems"`
		Confidence    float64  `json:"confidence"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("phase feedback is not valid JSON: %v\n%s", err, raw)
	}
	if got.Result != "pass" || got.Confidence <= 0.8 {
		t.Fatalf("feedback result/confidence = %#v, want pass with high confidence", got)
	}
	for _, want := range []string{"touched internal/db/queries.go", "commit abc123", "cerberus produced changes"} {
		if !contains(got.UsefulContext, want) {
			t.Fatalf("feedback useful_context missing %q: %#v", want, got.UsefulContext)
		}
	}
	if len(got.Problems) != 0 {
		t.Fatalf("pass feedback problems = %#v, want none", got.Problems)
	}
}

func TestBuildPhaseFeedbackRecordsFailedNotesAsProblem(t *testing.T) {
	raw := buildPhaseFeedback("fail", "no diff produced", []byte(`[]`), "")
	var got struct {
		Result   string   `json:"result"`
		Problems []string `json:"problems"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("phase feedback is not valid JSON: %v", err)
	}
	if got.Result != "fail" || !contains(got.Problems, "no diff produced") {
		t.Fatalf("feedback = %#v, want failed result with problem note", got)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestMaxCostExceeded(t *testing.T) {
	cases := []struct {
		name       string
		total      float64
		maxCostUSD *float64
		want       bool
	}{
		{name: "no budget configured never pauses", total: 1000, maxCostUSD: nil, want: false},
		{name: "total below budget does not pause", total: 1, maxCostUSD: floatPtr(2), want: false},
		{name: "total equal to budget pauses", total: 2, maxCostUSD: floatPtr(2), want: true},
		{name: "total above budget pauses", total: 3, maxCostUSD: floatPtr(2), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := maxCostExceeded(tc.total, tc.maxCostUSD); got != tc.want {
				t.Fatalf("maxCostExceeded(%v, %v) = %v, want %v", tc.total, tc.maxCostUSD, got, tc.want)
			}
		})
	}
}

func floatPtr(f float64) *float64 { return &f }

func TestRunPhaseFailsFastForRemoteOnlyRepository(t *testing.T) {
	remote := "https://github.com/foo/bar.git"
	repo := repository.Repository{Name: "remote-only", RemoteURL: &remote}

	r := &Runner{}
	err := r.runPhase(context.Background(), db.Workflow{}, repo, db.Phase{}, "", "", nil)
	if !errors.Is(err, repository.ErrNoLocalPath) {
		t.Fatalf("runPhase() error = %v, want wrapped repository.ErrNoLocalPath", err)
	}
}
