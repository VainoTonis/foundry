package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonis2/foundry/internal/db"
)

func testContract() Contract {
	return Contract{Version: "v1", Content: "Every plan must state recoverable understanding."}
}

func TestBuildContext_Validation(t *testing.T) {
	snap, err := BuildSnapshot(testPlan(t), nil, nil)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}

	cases := []struct {
		name     string
		snapshot Snapshot
		contract Contract
	}{
		{"empty snapshot", Snapshot{}, testContract()},
		{"empty contract version", snap, Contract{Content: "x"}},
		{"empty contract content", snap, Contract{Version: "v1"}},
	}
	for _, c := range cases {
		if _, err := BuildContext(c.snapshot, c.contract, SessionAttributionSummary{}); err == nil {
			t.Errorf("%s: BuildContext() error = nil, want error", c.name)
		}
	}
}

func TestBuildContext_PromptDeterministicSeparatesPassesAndReportsStrictly(t *testing.T) {
	feedback := testFeedback(MaxFeedbackItems+10, fixedTime())
	snap, err := BuildSnapshot(testPlan(t), testSteps(), feedback)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}

	a, err := BuildContext(snap, testContract(), SessionAttributionSummary{})
	if err != nil {
		t.Fatalf("BuildContext() error = %v", err)
	}
	b, err := BuildContext(snap, testContract(), SessionAttributionSummary{})
	if err != nil {
		t.Fatalf("BuildContext() error = %v", err)
	}
	if a.Prompt != b.Prompt {
		t.Fatalf("BuildContext() prompt not deterministic")
	}
	prompt := a.Prompt

	pass1Idx := strings.Index(prompt, "Pass 1")
	pass2Idx := strings.Index(prompt, "Pass 2")
	if pass1Idx == -1 || pass2Idx == -1 || pass1Idx >= pass2Idx {
		t.Fatalf("BuildContext() prompt does not present Pass 1 before Pass 2")
	}
	if !strings.Contains(prompt, "Do not read any\nrepository file during this pass") {
		t.Fatalf("BuildContext() prompt does not forbid repository reads during Pass 1")
	}
	if !strings.Contains(prompt, "one adjacent hop") {
		t.Fatalf("BuildContext() prompt does not bound Pass 2 grounding to one hop")
	}

	for _, field := range []string{
		ReportFieldVerdict, ReportFieldPass1, ReportFieldPass2,
		ReportFieldEvidence, ReportFieldUncertainty, ReportFieldUnavailable,
	} {
		if !strings.Contains(prompt, `"`+field+`"`) {
			t.Fatalf("BuildContext() prompt missing strict report field %q", field)
		}
	}
	if !strings.Contains(prompt, `"pass"`) || !strings.Contains(prompt, `"revise"`) || !strings.Contains(prompt, `"escalate"`) {
		t.Fatalf("BuildContext() prompt missing one of the three allowed verdicts")
	}

	if strings.Count(prompt, `"id":`) > MaxFeedbackItems {
		t.Fatalf("BuildContext() prompt embeds more than the capped %d feedback items", MaxFeedbackItems)
	}
	if !strings.Contains(prompt, `"feedback_truncated":true`) {
		t.Fatalf("BuildContext() prompt does not disclose feedback truncation")
	}

	if !strings.Contains(prompt, "/workspace/repositories/0-primary") {
		t.Fatalf("BuildContext() prompt missing local repository container path")
	}
	if !strings.Contains(prompt, "UNAVAILABLE") {
		t.Fatalf("BuildContext() prompt does not disclose the remote-only repository as unavailable")
	}
	if strings.Contains(prompt, "/host/checkouts") || strings.Contains(prompt, "example.test") {
		t.Fatalf("BuildContext() prompt leaks a host path or remote URL")
	}
}

func TestBuildContext_ContractFingerprintOfExactBytes(t *testing.T) {
	snap, err := BuildSnapshot(testPlan(t), nil, nil)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	contract := testContract()
	ctx, err := BuildContext(snap, contract, SessionAttributionSummary{})
	if err != nil {
		t.Fatalf("BuildContext() error = %v", err)
	}
	want := sha256Hex([]byte(contract.Content))
	if !strings.Contains(ctx.Prompt, "sha256:"+want) {
		t.Fatalf("BuildContext() prompt missing contract fingerprint sha256:%s", want)
	}

	altered := contract
	altered.Content += " "
	altCtx, err := BuildContext(snap, altered, SessionAttributionSummary{})
	if err != nil {
		t.Fatalf("BuildContext() error = %v", err)
	}
	if strings.Contains(altCtx.Prompt, "sha256:"+want) {
		t.Fatalf("BuildContext() contract fingerprint did not change with altered content")
	}
}

// TestBuildContext_SessionAttributionNeverAffectsSnapshotFingerprint is
// the core invariant this whole feature depends on: session attribution
// is informational only, so two BuildContext calls with the identical
// Snapshot and Contract but different SessionAttributionSummary values
// must render different prompt text while leaving Snapshot.SHA256 (and
// the raw snapshot JSON) byte-identical. If this ever regresses, an
// existing, otherwise-still-valid review could incorrectly be reported
// stale purely because a new session got attributed to the plan.
func TestBuildContext_SessionAttributionNeverAffectsSnapshotFingerprint(t *testing.T) {
	snap, err := BuildSnapshot(testPlan(t), testSteps(), nil)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	contract := testContract()

	empty, err := BuildContext(snap, contract, SessionAttributionSummary{})
	if err != nil {
		t.Fatalf("BuildContext() error = %v", err)
	}
	nonEmpty, err := BuildContext(snap, contract, SessionAttributionSummary{
		Total: 5,
		ByMethod: map[string]int{
			db.SessionPlanLinkMethodSystemDerived: 3,
			db.SessionPlanLinkMethodExplicit:      2,
		},
	})
	if err != nil {
		t.Fatalf("BuildContext() error = %v", err)
	}

	if empty.Snapshot.SHA256 != nonEmpty.Snapshot.SHA256 {
		t.Fatalf("BuildContext() snapshot fingerprint changed with session attribution: %q vs %q",
			empty.Snapshot.SHA256, nonEmpty.Snapshot.SHA256)
	}
	if string(empty.Snapshot.JSON) != string(nonEmpty.Snapshot.JSON) {
		t.Fatalf("BuildContext() snapshot JSON changed with session attribution")
	}
	if empty.Prompt == nonEmpty.Prompt {
		t.Fatalf("BuildContext() prompt did not change with different session attribution")
	}
}

// TestBuildContext_SessionAttributionSectionRendering verifies both the
// populated and the empty/zero rendering of the session attribution
// section, and that the section is clearly marked informational and
// distinct from the plan snapshot's own fingerprinted content.
func TestBuildContext_SessionAttributionSectionRendering(t *testing.T) {
	snap, err := BuildSnapshot(testPlan(t), nil, nil)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	contract := testContract()

	populated, err := BuildContext(snap, contract, SessionAttributionSummary{
		Total: 4,
		ByMethod: map[string]int{
			db.SessionPlanLinkMethodSystemDerived: 2,
			db.SessionPlanLinkMethodExplicit:      1,
			db.SessionPlanLinkMethodAPIInferred:   1,
		},
	})
	if err != nil {
		t.Fatalf("BuildContext() error = %v", err)
	}
	if !strings.Contains(populated.Prompt, "## Session attribution (informational; not part of the reviewed content or its fingerprint)") {
		t.Fatalf("BuildContext() prompt missing session attribution section header")
	}
	if !strings.Contains(populated.Prompt, "4 agent session(s)") {
		t.Fatalf("BuildContext() prompt missing session attribution total")
	}
	for _, want := range []string{"system_derived: 2", "explicit: 1", "api_inferred: 1"} {
		if !strings.Contains(populated.Prompt, want) {
			t.Fatalf("BuildContext() prompt missing session attribution breakdown %q", want)
		}
	}
	if strings.Contains(populated.Prompt, "heuristic") {
		t.Fatalf("BuildContext() prompt lists a method with zero linked sessions")
	}

	empty, err := BuildContext(snap, contract, SessionAttributionSummary{})
	if err != nil {
		t.Fatalf("BuildContext() error = %v", err)
	}
	if !strings.Contains(empty.Prompt, "No agent sessions are currently linked to this plan.") {
		t.Fatalf("BuildContext() prompt missing zero-session rendering")
	}
	if strings.Contains(empty.Prompt, "agent session(s) are currently linked") {
		t.Fatalf("BuildContext() zero-session prompt unexpectedly rendered a non-zero breakdown")
	}
}

func TestLoadContract(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "contract.md")
	appendixPath := filepath.Join(dir, "appendix.md")
	writeFile(t, globalPath, "Global contract body.")
	writeFile(t, appendixPath, "Team appendix body.")

	c, err := LoadContract(ContractSource{Version: "v1", GlobalPath: globalPath})
	if err != nil {
		t.Fatalf("LoadContract() global-only error = %v", err)
	}
	if c.Version != "v1" || c.Content != "Global contract body." {
		t.Fatalf("LoadContract() global-only = %+v, want v1 / exact global bytes", c)
	}

	withAppendix, err := LoadContract(ContractSource{Version: "v1", GlobalPath: globalPath, AppendixPath: appendixPath})
	if err != nil {
		t.Fatalf("LoadContract() with appendix error = %v", err)
	}
	want := "Global contract body." + contractAppendixSeparator + "Team appendix body."
	if withAppendix.Content != want {
		t.Fatalf("LoadContract() with appendix content = %q, want %q", withAppendix.Content, want)
	}
	again, err := LoadContract(ContractSource{Version: "v1", GlobalPath: globalPath, AppendixPath: appendixPath})
	if err != nil || again.Content != withAppendix.Content {
		t.Fatalf("LoadContract() not stable across identical loads: err=%v", err)
	}

	blankAppendix, err := LoadContract(ContractSource{Version: "v1", GlobalPath: globalPath, AppendixPath: "   "})
	if err != nil || blankAppendix.Content != "Global contract body." {
		t.Fatalf("LoadContract() blank appendix path not ignored: content=%q err=%v", blankAppendix.Content, err)
	}

	if _, err := LoadContract(ContractSource{GlobalPath: globalPath}); err == nil {
		t.Fatalf("LoadContract() with blank version: want error, got nil")
	}
	if _, err := LoadContract(ContractSource{Version: "v1"}); err == nil {
		t.Fatalf("LoadContract() with blank global path: want error, got nil")
	}
	if _, err := LoadContract(ContractSource{Version: "v1", GlobalPath: filepath.Join(dir, "missing.md")}); err == nil {
		t.Fatalf("LoadContract() with missing global file: want error, got nil")
	}
	if _, err := LoadContract(ContractSource{Version: "v1", GlobalPath: globalPath, AppendixPath: filepath.Join(dir, "missing-appendix.md")}); err == nil {
		t.Fatalf("LoadContract() with missing appendix file: want error, got nil")
	}

	emptyPath := filepath.Join(dir, "empty.md")
	writeFile(t, emptyPath, "   \n")
	if _, err := LoadContract(ContractSource{Version: "v1", GlobalPath: emptyPath}); err == nil {
		t.Fatalf("LoadContract() with blank global contract: want error, got nil")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
