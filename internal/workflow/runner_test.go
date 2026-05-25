package workflow

import (
	"encoding/json"
	"testing"
)

func TestBuildPhaseFeedbackFromVerdictNotesFilesAndCommit(t *testing.T) {
	raw := buildPhaseFeedback("pass", "cerberus produced changes", []byte(`["internal/db/queries.go"]`), "abc123")
	var got struct {
		Result          string   `json:"result"`
		UsefulContext   []string `json:"useful_context"`
		Problems        []string `json:"problems"`
		SuggestedMemory string   `json:"suggested_memory"`
		Confidence      float64  `json:"confidence"`
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
	if got.SuggestedMemory == "" {
		t.Fatalf("feedback did not include suggested_memory: %#v", got)
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
