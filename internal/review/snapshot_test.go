package review

import (
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func fixedTime() time.Time { return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) }

func testPlan(t *testing.T) db.Plan {
	t.Helper()
	local := "/host/checkouts/primary"
	remote := "https://example.test/secondary.git"
	return db.Plan{
		ID:      42,
		Title:   "  Add widget  ",
		Summary: "  summary  ",
		Content: "  content  ",
		Repositories: []db.PlanRepository{
			{Position: 1, RepositoryID: 2, Repository: repository.Repository{ID: 2, Name: "secondary", RemoteURL: &remote}},
			{Position: 0, RepositoryID: 1, Repository: repository.Repository{ID: 1, Name: "primary", LocalPath: &local}},
		},
	}
}

func testSteps() []db.PlanStep {
	return []db.PlanStep{
		{PlanID: 42, Position: 1, Text: "second"},
		{PlanID: 42, Position: 0, Text: "first"},
	}
}

func testFeedback(n int, base time.Time) []db.Feedback {
	out := make([]db.Feedback, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, db.Feedback{
			ID:        int64(i + 1),
			Body:      "body",
			Dimension: strPtr("quality"),
			Target:    strPtr("plan"),
			Score:     intPtr(i),
			Tags:      []string{"b", "a"},
			Status:    "open",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	return out
}

func TestBuildSnapshot_Validation(t *testing.T) {
	if _, err := BuildSnapshot(db.Plan{}, nil, nil); err == nil {
		t.Fatalf("zero plan id: want error, got nil")
	}
	if _, err := BuildSnapshot(db.Plan{ID: 1}, nil, nil); err == nil {
		t.Fatalf("no repositories: want error, got nil")
	}
}

func TestBuildSnapshot_DeterministicFingerprint(t *testing.T) {
	plan := testPlan(t)
	steps := testSteps()
	feedback := testFeedback(3, fixedTime())

	a, err := BuildSnapshot(plan, steps, feedback)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	b, err := BuildSnapshot(plan, steps, feedback)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if a.SHA256 != b.SHA256 || string(a.JSON) != string(b.JSON) {
		t.Fatalf("BuildSnapshot() not stable across identical calls")
	}

	// Reordering caller-supplied steps/feedback must not change the fingerprint.
	reorderedSteps := []db.PlanStep{steps[1], steps[0]}
	reorderedFeedback := []db.Feedback{feedback[2], feedback[0], feedback[1]}
	c, err := BuildSnapshot(plan, reorderedSteps, reorderedFeedback)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if a.SHA256 != c.SHA256 {
		t.Fatalf("BuildSnapshot() fingerprint changed with reordered input: %q != %q", a.SHA256, c.SHA256)
	}

	plan.Content += " changed"
	d, err := BuildSnapshot(plan, steps, feedback)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if a.SHA256 == d.SHA256 {
		t.Fatalf("BuildSnapshot() fingerprint unchanged after content edit")
	}
}

func TestBuildSnapshot_StepsOrderedByPosition(t *testing.T) {
	snap, err := BuildSnapshot(testPlan(t), testSteps(), nil)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if len(snap.Plan.Steps) != 2 || snap.Plan.Steps[0].Text != "first" || snap.Plan.Steps[1].Text != "second" {
		t.Fatalf("BuildSnapshot() steps = %+v, want ordered [first, second]", snap.Plan.Steps)
	}
}

func TestBuildSnapshot_RepositoriesOrderedAndHostPathsHidden(t *testing.T) {
	snap, err := BuildSnapshot(testPlan(t), nil, nil)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	repos := snap.Plan.Repositories
	if len(repos) != 2 {
		t.Fatalf("BuildSnapshot() repositories = %d, want 2", len(repos))
	}

	primary := repos[0]
	if primary.Name != "primary" || !primary.Primary || !primary.Available || primary.RepositoryID != 1 {
		t.Fatalf("BuildSnapshot() primary repo = %+v, want available primary named 'primary'", primary)
	}
	if primary.ContainerPath != "/workspace/repositories/0-primary" {
		t.Fatalf("BuildSnapshot() primary container path = %q, want /workspace/repositories/0-primary", primary.ContainerPath)
	}

	secondary := repos[1]
	if secondary.Available || secondary.UnavailableReason == "" {
		t.Fatalf("BuildSnapshot() secondary repo = %+v, want unavailable with a reason", secondary)
	}

	if strings.Contains(string(snap.JSON), "/host/checkouts/primary") || strings.Contains(string(snap.JSON), "example.test") {
		t.Fatalf("BuildSnapshot() JSON leaks host path or remote URL: %s", snap.JSON)
	}
}

func TestBuildSnapshot_FeedbackOpenOnlyCappedAndTruncated(t *testing.T) {
	base := fixedTime()
	feedback := testFeedback(MaxFeedbackItems+5, base)
	feedback[len(feedback)-1].Body = strings.Repeat("x", MaxFeedbackBodyRunes+50)
	feedback = append(feedback,
		db.Feedback{ID: 9001, Body: "resolved", Status: "resolved", CreatedAt: base.Add(time.Hour)},
		db.Feedback{ID: 9002, Body: "dismissed", Status: "dismissed", CreatedAt: base.Add(2 * time.Hour)},
	)

	snap, err := BuildSnapshot(testPlan(t), nil, feedback)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if !snap.Plan.FeedbackTruncated {
		t.Fatalf("BuildSnapshot() FeedbackTruncated = false, want true")
	}
	if len(snap.Plan.Feedback) != MaxFeedbackItems {
		t.Fatalf("BuildSnapshot() feedback count = %d, want %d (non-open excluded, rest capped)", len(snap.Plan.Feedback), MaxFeedbackItems)
	}

	first := snap.Plan.Feedback[0]
	if first.ID != int64(MaxFeedbackItems+5) {
		t.Fatalf("BuildSnapshot() first feedback id = %d, want most recent open item", first.ID)
	}
	if !strings.HasSuffix(first.Body, "...(truncated)") || len([]rune(first.Body)) > MaxFeedbackBodyRunes+len("...(truncated)") {
		t.Fatalf("BuildSnapshot() long feedback body not truncated correctly: %q", first.Body)
	}
	if got := first.Tags; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("BuildSnapshot() tags = %v, want sorted [a b]", got)
	}

	snap2, err := BuildSnapshot(testPlan(t), nil, testFeedback(3, base))
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if snap2.Plan.FeedbackTruncated || len(snap2.Plan.Feedback) != 3 {
		t.Fatalf("BuildSnapshot() under cap: FeedbackTruncated=%v, count=%d, want false/3", snap2.Plan.FeedbackTruncated, len(snap2.Plan.Feedback))
	}
}

func TestBuildSnapshot_FeedbackRepositoryScopedByPosition(t *testing.T) {
	plan := testPlan(t)
	feedback := []db.Feedback{
		{
			ID:     1,
			Body:   "scoped to both",
			Status: "open",
			Repositories: []db.FeedbackRepository{
				{RepositoryID: 2},
				{RepositoryID: 1},
				{RepositoryID: 2}, // duplicate, must be deduplicated
			},
			CreatedAt: fixedTime(),
		},
		{ID: 2, Body: "unscoped", Status: "open", CreatedAt: fixedTime().Add(time.Minute)},
	}

	snap, err := BuildSnapshot(plan, nil, feedback)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if len(snap.Plan.Feedback) != 2 {
		t.Fatalf("BuildSnapshot() feedback count = %d, want 2", len(snap.Plan.Feedback))
	}

	var scoped, unscoped *FeedbackSnapshot
	for i := range snap.Plan.Feedback {
		switch snap.Plan.Feedback[i].ID {
		case 1:
			scoped = &snap.Plan.Feedback[i]
		case 2:
			unscoped = &snap.Plan.Feedback[i]
		}
	}
	if scoped == nil || unscoped == nil {
		t.Fatalf("BuildSnapshot() missing expected feedback items: %+v", snap.Plan.Feedback)
	}
	if len(scoped.Repositories) != 2 || scoped.Repositories[0] != 0 || scoped.Repositories[1] != 1 {
		t.Fatalf("BuildSnapshot() scoped feedback repositories = %v, want deduplicated, ascending [0 1]", scoped.Repositories)
	}
	if len(unscoped.Repositories) != 0 {
		t.Fatalf("BuildSnapshot() unscoped feedback repositories = %v, want none", unscoped.Repositories)
	}
}

func TestBuildSnapshot_FeedbackStructuredFieldsCarried(t *testing.T) {
	evidence, impact, action, owner := "path/to/file.go:10", "breaks build", "fix the import", "alice"
	feedback := []db.Feedback{{
		ID: 1, Body: "structured", Status: "open",
		Evidence: &evidence, Impact: &impact, RecommendedAction: &action, Owner: &owner,
		CreatedAt: fixedTime(),
	}}

	snap, err := BuildSnapshot(testPlan(t), nil, feedback)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if len(snap.Plan.Feedback) != 1 {
		t.Fatalf("BuildSnapshot() feedback count = %d, want 1", len(snap.Plan.Feedback))
	}
	fs := snap.Plan.Feedback[0]
	if fs.Evidence != evidence || fs.Impact != impact || fs.RecommendedAction != action || fs.Owner != owner {
		t.Fatalf("BuildSnapshot() structured feedback fields = %+v, want evidence/impact/action/owner carried verbatim", fs)
	}
}

func TestBuildMountManifest_LocalOnlyReadOnlyStableAndDeterministic(t *testing.T) {
	plan := testPlan(t)
	a := BuildMountManifest(plan)
	b := BuildMountManifest(plan)

	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("BuildMountManifest() mounts = %d/%d, want 1 (remote-only repo excluded)", len(a), len(b))
	}
	if a[0] != b[0] {
		t.Fatalf("BuildMountManifest() nondeterministic: %+v vs %+v", a[0], b[0])
	}
	if a[0].Host != "/host/checkouts/primary" || a[0].Container != "/workspace/repositories/0-primary" || !a[0].ReadOnly {
		t.Fatalf("BuildMountManifest() mount = %+v, want read-only /host/checkouts/primary -> /workspace/repositories/0-primary", a[0])
	}
}

func TestContainerPath_SlugifiesRepositoryName(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Primary Repo", "primary-repo"},
		{"foo_bar/baz.go", "foo-bar-baz-go"},
		{"   ", "repo"},
		{"---", "repo"},
		{"UPPER-CASE", "upper-case"},
	}
	for _, c := range cases {
		got := containerPath(3, c.name)
		want := "/workspace/repositories/3-" + c.want
		if got != want {
			t.Errorf("containerPath(3, %q) = %q, want %q", c.name, got, want)
		}
	}
}
