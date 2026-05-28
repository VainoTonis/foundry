package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/db"
)

func TestSettingsPatchSeparatesRuntimeKeys(t *testing.T) {
	if !isRuntimeSetting("git_root") || !isRuntimeSetting("memory_repo_path") || !isRuntimeSetting("cerberus_profile") {
		t.Fatalf("runtime settings keys not recognized")
	}
	if isRuntimeSetting("server_port") {
		t.Fatalf("server_port should remain config-backed")
	}
}

func TestMergeYAMLRuntimeSettingsOverridesAndAppendsDBValues(t *testing.T) {
	got := mergeYAMLRuntimeSettings("db_url: old\ngit_root: /old\n", map[string]string{"git_root": "/db/git", "memory_repo_path": "/db/mem", "cerberus_profile": "prof"})
	for _, want := range []string{"git_root: \"/db/git\"", "memory_repo_path: \"/db/mem\"", "cerberus_profile: \"prof\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("merged settings missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "/old") {
		t.Fatalf("old runtime setting leaked into merged yaml:\n%s", got)
	}
}

func TestPhaseStateTransitionHelpers(t *testing.T) {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)

	approve := approvePhaseUpdate(now)
	if approve.Status == nil || *approve.Status != "done" || approve.ReviewVerdict == nil || *approve.ReviewVerdict != "pass" || approve.FinishedAt == nil || !approve.FinishedAt.Equal(now) {
		t.Fatalf("approvePhaseUpdate = %#v, want done/pass/finished_at", approve)
	}

	reject := rejectPhaseUpdate(now)
	if reject.Status == nil || *reject.Status != "failed" || reject.ReviewVerdict == nil || *reject.ReviewVerdict != "fail" || reject.FinishedAt == nil || !reject.FinishedAt.Equal(now) {
		t.Fatalf("rejectPhaseUpdate = %#v, want failed/fail/finished_at", reject)
	}

	resume := resumeFailedPhaseUpdate()
	if resume.Status == nil || *resume.Status != "pending" || resume.RetryCount == nil || *resume.RetryCount != 0 {
		t.Fatalf("resumeFailedPhaseUpdate = %#v, want pending with retry count reset", resume)
	}
}

func TestBuildFollowUpSpecContentInjectsFailureContextBeforePhases(t *testing.T) {
	review := " needs tests "
	summary := "failed on migration"
	rationale := "constraint violated"
	prompt := strings.Repeat("p", 2100)
	sp := db.Spec{Content: "# Original\n\nGlobal context.\n\n## Phase 1: Build\n\nDo it."}
	wf := db.Workflow{ID: 77}
	failed := []db.Phase{{ID: 9, Position: 2, Name: "Verify", Status: "failed", RetryCount: 3, ReviewVerdict: strPtr("fail"), ReviewNotes: &review, DecisionSummary: &summary, DecisionRationale: &rationale, PromptSent: &prompt}}

	content := buildFollowUpSpecContentWithContext(sp, buildFollowUpFailureContext(context.Background(), wf, failed, nil))

	followIdx := strings.Index(content, "## Follow-up run context")
	phaseIdx := strings.Index(content, "## Phase 1: Build")
	if followIdx == -1 || phaseIdx == -1 || followIdx > phaseIdx {
		t.Fatalf("follow-up context was not injected before phases:\n%s", content)
	}
	for _, want := range []string{"failed workflow #77", "### Failed phase 2: Verify", "- Phase ID: 9", "- Retry count: 3", "> needs tests", "> failed on migration", "Prompt sent excerpt:", "... truncated ..."} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
}

func TestBuildFollowUpSpecContentAppendsWhenSpecHasNoPhases(t *testing.T) {
	sp := db.Spec{Content: "# Original\n\nNo executable phases yet."}
	got := buildFollowUpSpecContentWithContext(sp, "## Follow-up run context\n\nDetails")
	if !strings.HasSuffix(got, "## Follow-up run context\n\nDetails") {
		t.Fatalf("context was not appended to phase-less spec:\n%s", got)
	}
}

func TestMemoryReviewStateHelpers(t *testing.T) {
	accept := acceptMemoryUpdateParams(" /memory/project/workflow-updates/workflow-9.md ")
	if accept.Status == nil || *accept.Status != "accepted" || accept.MemoryPath == nil || *accept.MemoryPath != "/memory/project/workflow-updates/workflow-9.md" || accept.ProposalMarkdown != nil || accept.ReviewerComment != nil {
		t.Fatalf("acceptMemoryUpdateParams = %#v, want accepted path-only update", accept)
	}

	reject := rejectMemoryUpdateParams(" no durable signal ")
	if reject.Status == nil || *reject.Status != "rejected" || reject.ReviewerComment == nil || *reject.ReviewerComment != "no durable signal" || reject.ProposalMarkdown != nil || reject.MemoryPath != nil {
		t.Fatalf("rejectMemoryUpdateParams = %#v, want rejected/comment-only update", reject)
	}

	revise := reviseMemoryUpdateParams(" tighten scope ", " revised proposal ")
	if revise.Status == nil || *revise.Status != "pending" || revise.ReviewerComment == nil || *revise.ReviewerComment != "tighten scope" || revise.ProposalMarkdown == nil || *revise.ProposalMarkdown != "revised proposal" || revise.MemoryPath != nil {
		t.Fatalf("reviseMemoryUpdateParams = %#v, want pending with trimmed comment/proposal", revise)
	}

	reviseNoProposal := reviseMemoryUpdateParams(" keep existing proposal ", "  ")
	if reviseNoProposal.ProposalMarkdown != nil || reviseNoProposal.ReviewerComment == nil || *reviseNoProposal.ReviewerComment != "keep existing proposal" {
		t.Fatalf("reviseMemoryUpdateParams with blank proposal = %#v, want existing proposal preserved", reviseNoProposal)
	}
}

func TestFormatWorkflowMemoryProposalBuildsBoundedContext(t *testing.T) {
	feedback := " remember the durable bit "
	summary := "Use bounded writes"
	rationale := "Avoid touching target repo"
	prompt := "do not include prompt bodies"
	adjusted := "do not include adjusted prompts"
	notes := "do not include review notes"
	phaseFeedback := []byte(`{"result":"pass","useful_context":["touched internal/memory/memory.go"],"problems":[],"suggested_memory":"remember storage writes","confidence":0.85}`)
	proposal := formatWorkflowMemoryProposal(
		db.Workflow{ID: 42, Track: "impl", Status: "done"},
		db.Spec{Title: "Storage"},
		db.Project{Name: "Foundry"},
		[]db.Phase{{Position: 1, Name: "Persist", PromptSent: &prompt, AdjustedPrompt: &adjusted, ReviewNotes: &notes, DecisionSummary: &summary, DecisionRationale: &rationale, FilesTouched: []byte(`["internal/memory/memory.go"]`), PhaseFeedback: phaseFeedback}},
		feedback,
	)

	for _, want := range []string{"# Workflow 42 memory update", "Project: Foundry", "Spec: Storage", "## Reviewer feedback", strings.TrimSpace(feedback), "### Phase 1: Persist", "Summary: Use bounded writes", "Rationale: Avoid touching target repo", "Files touched: `", "Structured feedback:", "- Result: pass", "- Useful context: touched internal/memory/memory.go", "- Suggested memory: remember storage writes", "- Confidence: 0.85"} {
		if !strings.Contains(proposal, want) {
			t.Fatalf("proposal missing %q:\n%s", want, proposal)
		}
	}
	for _, notWant := range []string{prompt, adjusted, notes, "Prompt sent", "Adjusted prompt", "Review notes:"} {
		if strings.Contains(proposal, notWant) {
			t.Fatalf("proposal crossed intended boundary with %q:\n%s", notWant, proposal)
		}
	}
	if strings.HasSuffix(proposal, "\n") {
		t.Fatalf("proposal was not trimmed:\n%s", proposal)
	}
}

func TestMemoryProposalPromptIncludesRevisionInputs(t *testing.T) {
	prompt := memoryProposalPrompt("# Workflow 9 memory update\n\nContext", " tighten this ", "# Old proposal")
	for _, want := range []string{"Return only the proposed durable memory update as markdown", "do not create, edit, delete, or commit files", "Reviewer instruction:\ntighten this", "Current proposal to revise:\n# Old proposal", "Workflow context:\n# Workflow 9 memory update"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildFollowUpContextIncludesRecentLogs(t *testing.T) {
	ph := db.Phase{ID: 1, Position: 1, Name: "Test", Status: "failed"}
	got := buildFollowUpFailureContext(context.Background(), db.Workflow{ID: 10}, []db.Phase{ph}, func(context.Context, int64, int) ([]db.PhaseLog, error) {
		return []db.PhaseLog{{Line: " last useful log "}, {Line: ""}}, nil
	})
	if !strings.Contains(got, "Recent log summary (tail):") || !strings.Contains(got, "> last useful log") {
		t.Fatalf("recent logs were not included:\n%s", got)
	}
}

func strPtr(s string) *string { return &s }
