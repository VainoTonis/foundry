package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

// --- fakes ---------------------------------------------------------------

type fakePlanReviewStore struct {
	mu          sync.Mutex
	nextID      int64
	reviews     map[int64]db.PlanReview
	createErr      error
	startErr       error
	completeErr    error
	failErr        error
	listRunningErr error
	// done, if set, receives a value every time a review transitions to
	// a terminal state, so a test can wait for background work started
	// by StartStewardReview instead of racing it.
	done chan struct{}
}

func newFakePlanReviewStore() *fakePlanReviewStore {
	return &fakePlanReviewStore{nextID: 1, reviews: map[int64]db.PlanReview{}}
}

func (f *fakePlanReviewStore) CreatePlanReview(_ context.Context, p db.CreatePlanReviewParams) (db.PlanReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return db.PlanReview{}, f.createErr
	}
	r := db.PlanReview{
		ID:              f.nextID,
		PlanID:          p.PlanID,
		InputSnapshot:   p.InputSnapshot,
		ContractVersion: p.ContractVersion,
		Model:           p.Model,
		Session:         p.Session,
		Status:          db.PlanReviewStatusQueued,
	}
	f.nextID++
	f.reviews[r.ID] = r
	return r, nil
}

func (f *fakePlanReviewStore) StartPlanReview(_ context.Context, id int64) (db.PlanReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return db.PlanReview{}, f.startErr
	}
	r := f.reviews[id]
	r.Status = db.PlanReviewStatusRunning
	f.reviews[id] = r
	return r, nil
}

func (f *fakePlanReviewStore) CompletePlanReview(_ context.Context, id int64, verdict string, report json.RawMessage) (db.PlanReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.completeErr != nil {
		return db.PlanReview{}, f.completeErr
	}
	r := f.reviews[id]
	r.Status = db.PlanReviewStatusCompleted
	v := verdict
	r.Verdict = &v
	r.Report = report
	f.reviews[id] = r
	f.signalDone()
	return r, nil
}

func (f *fakePlanReviewStore) FailPlanReview(_ context.Context, id int64, errMsg string) (db.PlanReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return db.PlanReview{}, f.failErr
	}
	r := f.reviews[id]
	r.Status = db.PlanReviewStatusFailed
	e := errMsg
	r.Error = &e
	f.reviews[id] = r
	f.signalDone()
	return r, nil
}

// signalDone notifies f.done, if set, that a review just reached a
// terminal state. It must be called with f.mu held.
func (f *fakePlanReviewStore) signalDone() {
	if f.done == nil {
		return
	}
	select {
	case f.done <- struct{}{}:
	default:
	}
}

func (f *fakePlanReviewStore) ListRunningPlanReviews(_ context.Context) ([]db.PlanReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listRunningErr != nil {
		return nil, f.listRunningErr
	}
	var out []db.PlanReview
	for _, r := range f.reviews {
		if r.Status == db.PlanReviewStatusRunning {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakePlanReviewStore) get(id int64) db.PlanReview {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reviews[id]
}

type fakeCerberus struct {
	mu       sync.Mutex
	turnIn   cerberus.TurnInput
	turnOut  cerberus.TurnOutput
	turnErr  error
	turnFunc func(context.Context, cerberus.TurnInput) (cerberus.TurnOutput, error)
	cleaned  []string
}

func (f *fakeCerberus) Turn(ctx context.Context, input cerberus.TurnInput) (cerberus.TurnOutput, error) {
	f.mu.Lock()
	f.turnIn = input
	fn := f.turnFunc
	out, err := f.turnOut, f.turnErr
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, input)
	}
	return out, err
}

func (f *fakeCerberus) Clean(_ context.Context, session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleaned = append(f.cleaned, session)
	return nil
}

func (f *fakeCerberus) lastInput() cerberus.TurnInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.turnIn
}

func (f *fakeCerberus) cleanedSessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.cleaned))
	copy(out, f.cleaned)
	return out
}

// --- helpers ---------------------------------------------------------------

func validReportMessage(verdict string) string {
	return fmt.Sprintf(`Here is my final report.

{"verdict":%q,"pass1":"structural findings","pass2":"grounded findings","evidence":["a.go:1"],"uncertainties":["none"],"unavailable_repositories":[]}

Thanks.`, verdict)
}

func allLocalPlan(t *testing.T) db.Plan {
	t.Helper()
	a := "/host/checkouts/alpha"
	b := "/host/checkouts/beta"
	return db.Plan{
		ID: 7,
		Repositories: []db.PlanRepository{
			{Position: 0, RepositoryID: 1, Repository: repository.Repository{ID: 1, Name: "alpha", LocalPath: &a}},
			{Position: 1, RepositoryID: 2, Repository: repository.Repository{ID: 2, Name: "beta", LocalPath: &b}},
		},
	}
}

func testRunOptions(plan db.Plan) RunOptions {
	return RunOptions{
		Plan:     plan,
		Steps:    nil,
		Feedback: nil,
		Contract: ContractSource{}, // overridden by tests that need a real contract; see withContract
		Model:    "economical-model",
		Timeout:  2 * time.Second,
	}
}

func withContract(t *testing.T, opts RunOptions) RunOptions {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/contract.md"
	writeFile(t, path, "Every plan must state recoverable understanding.")
	opts.Contract = ContractSource{Version: "v1", GlobalPath: path}
	return opts
}

// --- tests -------------------------------------------------------------

func TestRunStewardReview_SuccessAllLocalPlan(t *testing.T) {
	plan := allLocalPlan(t)
	store := newFakePlanReviewStore()
	cerb := &fakeCerberus{turnOut: cerberus.TurnOutput{Status: "ok", Message: validReportMessage("pass")}}
	svc := newService(store, cerb)

	review, err := svc.RunStewardReview(context.Background(), withContract(t, testRunOptions(plan)))
	if err != nil {
		t.Fatalf("RunStewardReview() error = %v", err)
	}
	if review.Status != db.PlanReviewStatusCompleted {
		t.Fatalf("review.Status = %q, want completed", review.Status)
	}
	if review.Verdict == nil || *review.Verdict != db.PlanReviewVerdictPass {
		t.Fatalf("review.Verdict = %v, want pass", review.Verdict)
	}
	var report map[string]json.RawMessage
	if err := json.Unmarshal(review.Report, &report); err != nil {
		t.Fatalf("review.Report is not valid JSON: %v", err)
	}
	for _, field := range requiredReportFields {
		if _, ok := report[field]; !ok {
			t.Fatalf("persisted report missing field %q", field)
		}
	}

	input := cerb.lastInput()
	wantMounts := BuildMountManifest(plan)
	if len(wantMounts) != 2 {
		t.Fatalf("test setup: want 2 mounts for all-local plan, got %d", len(wantMounts))
	}
	assertMountsEqual(t, input.ExtraMounts, wantMounts)
	for _, m := range input.ExtraMounts {
		if !m.ReadOnly {
			t.Fatalf("mount %+v is not read-only", m)
		}
	}

	cleaned := cerb.cleanedSessions()
	if len(cleaned) != 1 || cleaned[0] != review.Session || cleaned[0] != input.Name {
		t.Fatalf("cleaned sessions = %v, want exactly [%q] matching turn input name %q", cleaned, review.Session, input.Name)
	}
}

func TestRunStewardReview_MixedRemotePlanMountsOnlyLocalRepos(t *testing.T) {
	plan := testPlan(t) // one local ("primary"), one remote-only ("secondary")
	store := newFakePlanReviewStore()
	cerb := &fakeCerberus{turnOut: cerberus.TurnOutput{Status: "ok", Message: validReportMessage("revise")}}
	svc := newService(store, cerb)

	review, err := svc.RunStewardReview(context.Background(), withContract(t, testRunOptions(plan)))
	if err != nil {
		t.Fatalf("RunStewardReview() error = %v", err)
	}
	if review.Verdict == nil || *review.Verdict != db.PlanReviewVerdictRevise {
		t.Fatalf("review.Verdict = %v, want revise", review.Verdict)
	}

	input := cerb.lastInput()
	if len(input.ExtraMounts) != 1 {
		t.Fatalf("ExtraMounts = %+v, want exactly one mount for the single local repo", input.ExtraMounts)
	}
	if input.ExtraMounts[0].Host != "/host/checkouts/primary" || !input.ExtraMounts[0].ReadOnly {
		t.Fatalf("ExtraMounts[0] = %+v, want read-only mount of the local repo", input.ExtraMounts[0])
	}
	if strings.Contains(input.Message, "example.test") {
		t.Fatalf("prompt leaked the remote-only repository's remote URL")
	}
	if !strings.Contains(input.Message, "UNAVAILABLE") {
		t.Fatalf("prompt did not disclose the remote-only repository as unavailable")
	}
}

func TestRunStewardReview_ModelAttribution(t *testing.T) {
	plan := allLocalPlan(t)
	store := newFakePlanReviewStore()
	cerb := &fakeCerberus{turnOut: cerberus.TurnOutput{Status: "ok", Message: validReportMessage("pass")}}
	svc := newService(store, cerb)

	opts := withContract(t, testRunOptions(plan))
	opts.Model = "claude-cheap-1"
	review, err := svc.RunStewardReview(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunStewardReview() error = %v", err)
	}
	if review.Model != "claude-cheap-1" {
		t.Fatalf("persisted review.Model = %q, want %q", review.Model, "claude-cheap-1")
	}
	if cerb.lastInput().Model != "claude-cheap-1" {
		t.Fatalf("turn input.Model = %q, want %q", cerb.lastInput().Model, "claude-cheap-1")
	}
}

func TestRunStewardReview_MalformedReportFailsReviewAndCleansSession(t *testing.T) {
	cases := []struct {
		name    string
		message string
	}{
		{"not json", "I think this plan looks fine."},
		{"missing field", `{"verdict":"pass","pass1":"x","pass2":"x","evidence":[],"uncertainties":[]}`},
		{"extra field", `{"verdict":"pass","pass1":"x","pass2":"x","evidence":[],"uncertainties":[],"unavailable_repositories":[],"extra":"nope"}`},
		{"bad verdict", `{"verdict":"looks great","pass1":"x","pass2":"x","evidence":[],"uncertainties":[],"unavailable_repositories":[]}`},
		{"empty message", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan := allLocalPlan(t)
			store := newFakePlanReviewStore()
			cerb := &fakeCerberus{turnOut: cerberus.TurnOutput{Status: "ok", Message: c.message}}
			svc := newService(store, cerb)

			_, err := svc.RunStewardReview(context.Background(), withContract(t, testRunOptions(plan)))
			if err == nil {
				t.Fatalf("RunStewardReview() error = nil, want malformed report error")
			}

			stored := onlyReview(t, store)
			if stored.Status != db.PlanReviewStatusFailed {
				t.Fatalf("stored review status = %q, want failed", stored.Status)
			}
			if stored.Verdict != nil {
				t.Fatalf("failed review has a verdict: %v", *stored.Verdict)
			}
			if len(cerb.cleanedSessions()) != 1 {
				t.Fatalf("cleaned sessions = %v, want exactly one cleanup", cerb.cleanedSessions())
			}
		})
	}
}

func TestRunStewardReview_TurnErrorFailsReview(t *testing.T) {
	plan := allLocalPlan(t)
	store := newFakePlanReviewStore()
	cerb := &fakeCerberus{turnErr: errors.New("cerberus binary exploded")}
	svc := newService(store, cerb)

	_, err := svc.RunStewardReview(context.Background(), withContract(t, testRunOptions(plan)))
	if err == nil || !strings.Contains(err.Error(), "cerberus binary exploded") {
		t.Fatalf("RunStewardReview() error = %v, want it to wrap the turn error", err)
	}
	stored := onlyReview(t, store)
	if stored.Status != db.PlanReviewStatusFailed {
		t.Fatalf("stored review status = %q, want failed", stored.Status)
	}
	if len(cerb.cleanedSessions()) != 1 {
		t.Fatalf("cleaned sessions = %v, want exactly one cleanup", cerb.cleanedSessions())
	}
}

func TestRunStewardReview_TurnErrorStatusFailsReview(t *testing.T) {
	plan := allLocalPlan(t)
	store := newFakePlanReviewStore()
	cerb := &fakeCerberus{turnOut: cerberus.TurnOutput{Status: "error", Error: "session not found"}}
	svc := newService(store, cerb)

	_, err := svc.RunStewardReview(context.Background(), withContract(t, testRunOptions(plan)))
	if err == nil || !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("RunStewardReview() error = %v, want it to wrap the turn's error status", err)
	}
	stored := onlyReview(t, store)
	if stored.Status != db.PlanReviewStatusFailed {
		t.Fatalf("stored review status = %q, want failed", stored.Status)
	}
}

func TestRunStewardReview_TimeoutFailsReviewAndCleansSession(t *testing.T) {
	plan := allLocalPlan(t)
	store := newFakePlanReviewStore()
	cerb := &fakeCerberus{
		turnFunc: func(ctx context.Context, _ cerberus.TurnInput) (cerberus.TurnOutput, error) {
			<-ctx.Done()
			return cerberus.TurnOutput{}, ctx.Err()
		},
	}
	svc := newService(store, cerb)

	opts := withContract(t, testRunOptions(plan))
	opts.Timeout = 10 * time.Millisecond

	_, err := svc.RunStewardReview(context.Background(), opts)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunStewardReview() error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	stored := onlyReview(t, store)
	if stored.Status != db.PlanReviewStatusFailed {
		t.Fatalf("stored review status = %q, want failed", stored.Status)
	}
	if len(cerb.cleanedSessions()) != 1 {
		t.Fatalf("cleaned sessions = %v, want exactly one cleanup", cerb.cleanedSessions())
	}
}

func TestRunStewardReview_PersistenceFailureNeverFabricatesAPass(t *testing.T) {
	plan := allLocalPlan(t)
	store := newFakePlanReviewStore()
	store.completeErr = errors.New("database is on fire")
	cerb := &fakeCerberus{turnOut: cerberus.TurnOutput{Status: "ok", Message: validReportMessage("pass")}}
	svc := newService(store, cerb)

	_, err := svc.RunStewardReview(context.Background(), withContract(t, testRunOptions(plan)))
	if err == nil || !strings.Contains(err.Error(), "database is on fire") {
		t.Fatalf("RunStewardReview() error = %v, want it to surface the persistence failure", err)
	}

	stored := onlyReview(t, store)
	if stored.Status == db.PlanReviewStatusCompleted {
		t.Fatalf("stored review status = completed despite a persistence failure; a well-formed report must never be silently dropped as a pass without being recorded")
	}
	// The session must still be cleaned up even though completion could
	// not be persisted.
	if len(cerb.cleanedSessions()) != 1 {
		t.Fatalf("cleaned sessions = %v, want exactly one cleanup", cerb.cleanedSessions())
	}
}

func TestStartStewardReview_ReturnsPromptlyThenCompletesInBackground(t *testing.T) {
	plan := allLocalPlan(t)
	store := newFakePlanReviewStore()
	store.done = make(chan struct{}, 1)
	turnStarted := make(chan struct{})
	unblock := make(chan struct{})
	cerb := &fakeCerberus{
		turnFunc: func(ctx context.Context, _ cerberus.TurnInput) (cerberus.TurnOutput, error) {
			close(turnStarted)
			<-unblock
			return cerberus.TurnOutput{Status: "ok", Message: validReportMessage("pass")}, nil
		},
	}
	svc := newService(store, cerb)

	review, err := svc.StartStewardReview(context.Background(), withContract(t, testRunOptions(plan)))
	if err != nil {
		t.Fatalf("StartStewardReview() error = %v", err)
	}
	// StartStewardReview must return before the cerberus turn resolves:
	// the turn is still blocked on unblock, so the review it returned
	// can only be running, never a terminal state.
	if review.Status != db.PlanReviewStatusRunning {
		t.Fatalf("review.Status = %q, want running (StartStewardReview must return promptly)", review.Status)
	}
	<-turnStarted
	close(unblock)

	select {
	case <-store.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for background review execution to persist a terminal outcome")
	}

	stored := store.get(review.ID)
	if stored.Status != db.PlanReviewStatusCompleted {
		t.Fatalf("stored review status = %q, want completed", stored.Status)
	}
	if stored.Verdict == nil || *stored.Verdict != db.PlanReviewVerdictPass {
		t.Fatalf("stored review verdict = %v, want pass", stored.Verdict)
	}
	if len(cerb.cleanedSessions()) != 1 {
		t.Fatalf("cleaned sessions = %v, want exactly one cleanup", cerb.cleanedSessions())
	}
}

func TestReconcileInterruptedReviews_FailsOrphanedRunningReviews(t *testing.T) {
	plan := allLocalPlan(t)
	store := newFakePlanReviewStore()
	cerb := &fakeCerberus{}
	svc := newService(store, cerb)

	// Simulate a review left running by a process that died mid-turn:
	// prepareReview alone takes it from queued to running, and nothing
	// ever resolves it further.
	prep, err := svc.prepareReview(context.Background(), withContract(t, testRunOptions(plan)))
	if err != nil {
		t.Fatalf("prepareReview() error = %v", err)
	}
	if prep.review.Status != db.PlanReviewStatusRunning {
		t.Fatalf("prep.review.Status = %q, want running", prep.review.Status)
	}

	n, err := svc.ReconcileInterruptedReviews(context.Background())
	if err != nil {
		t.Fatalf("ReconcileInterruptedReviews() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("ReconcileInterruptedReviews() = %d, want 1", n)
	}

	stored := store.get(prep.review.ID)
	if stored.Status != db.PlanReviewStatusFailed {
		t.Fatalf("stored review status = %q, want failed", stored.Status)
	}
	if stored.Error == nil || strings.TrimSpace(*stored.Error) == "" {
		t.Fatalf("stored review has no error message: %v", stored.Error)
	}

	// A second reconciliation pass finds nothing left to fail.
	n2, err := svc.ReconcileInterruptedReviews(context.Background())
	if err != nil {
		t.Fatalf("ReconcileInterruptedReviews() second pass error = %v", err)
	}
	if n2 != 0 {
		t.Fatalf("ReconcileInterruptedReviews() second pass = %d, want 0", n2)
	}
}

func TestRunStewardReview_RequiresModelAndTimeout(t *testing.T) {
	plan := allLocalPlan(t)
	store := newFakePlanReviewStore()
	cerb := &fakeCerberus{}
	svc := newService(store, cerb)

	noModel := withContract(t, testRunOptions(plan))
	noModel.Model = ""
	if _, err := svc.RunStewardReview(context.Background(), noModel); err == nil {
		t.Fatalf("RunStewardReview() with no model: want error, got nil")
	}

	noTimeout := withContract(t, testRunOptions(plan))
	noTimeout.Timeout = 0
	if _, err := svc.RunStewardReview(context.Background(), noTimeout); err == nil {
		t.Fatalf("RunStewardReview() with no timeout: want error, got nil")
	}

	if len(store.reviews) != 0 {
		t.Fatalf("invalid options must not create a review row; got %d", len(store.reviews))
	}
}

func onlyReview(t *testing.T, store *fakePlanReviewStore) db.PlanReview {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.reviews) != 1 {
		t.Fatalf("store has %d reviews, want exactly 1", len(store.reviews))
	}
	for _, r := range store.reviews {
		return r
	}
	panic("unreachable")
}

func assertMountsEqual(t *testing.T, got, want []cerberus.Mount) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("mounts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mounts[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// --- report validation unit tests --------------------------------------

func TestExtractReportJSON(t *testing.T) {
	raw, err := extractReportJSON(`prose before {"a":1,"b":{"c":2}} prose after`)
	if err != nil {
		t.Fatalf("extractReportJSON() error = %v", err)
	}
	if string(raw) != `{"a":1,"b":{"c":2}}` {
		t.Fatalf("extractReportJSON() = %q", raw)
	}

	if _, err := extractReportJSON("no braces here"); err == nil {
		t.Fatalf("extractReportJSON() with no object: want error, got nil")
	}
	if _, err := extractReportJSON(`{"a": "unterminated`); err == nil {
		t.Fatalf("extractReportJSON() with unbalanced object: want error, got nil")
	}
	// A brace inside a quoted string must not affect balancing.
	raw, err = extractReportJSON(`{"a":"} not a close"}`)
	if err != nil {
		t.Fatalf("extractReportJSON() error = %v", err)
	}
	if string(raw) != `{"a":"} not a close"}` {
		t.Fatalf("extractReportJSON() = %q", raw)
	}
}

func TestValidateReport(t *testing.T) {
	valid := `{"verdict":"escalate","pass1":"x","pass2":"x","evidence":[],"uncertainties":[],"unavailable_repositories":[]}`
	verdict, err := validateReport(json.RawMessage(valid))
	if err != nil || verdict != "escalate" {
		t.Fatalf("validateReport() = (%q, %v), want (escalate, nil)", verdict, err)
	}

	cases := []string{
		`{"verdict":"pass"}`,
		`{"verdict":"pass","pass1":"x","pass2":"x","evidence":[],"uncertainties":[],"unavailable_repositories":[],"extra":1}`,
		`{"verdict":"maybe","pass1":"x","pass2":"x","evidence":[],"uncertainties":[],"unavailable_repositories":[]}`,
		`{"verdict":1,"pass1":"x","pass2":"x","evidence":[],"uncertainties":[],"unavailable_repositories":[]}`,
		`not json`,
	}
	for _, c := range cases {
		if _, err := validateReport(json.RawMessage(c)); err == nil {
			t.Fatalf("validateReport(%q): want error, got nil", c)
		}
	}
}
