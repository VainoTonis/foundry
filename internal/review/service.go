package review

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tonis2/foundry/internal/cerberus"
	"github.com/tonis2/foundry/internal/db"
)

// validReportVerdicts is the exact, closed set of verdicts a Steward
// report may assert. It mirrors db's plan review verdicts so a report
// can never be persisted with a verdict CompletePlanReview would itself
// reject.
var validReportVerdicts = map[string]bool{
	db.PlanReviewVerdictPass:     true,
	db.PlanReviewVerdictRevise:   true,
	db.PlanReviewVerdictEscalate: true,
}

// requiredReportFields is the exact, closed set of keys a Steward final
// report must contain, and no others.
var requiredReportFields = []string{
	ReportFieldVerdict,
	ReportFieldPass1,
	ReportFieldPass2,
	ReportFieldEvidence,
	ReportFieldUncertainty,
	ReportFieldUnavailable,
}

// cerberusClient is the narrow cerberus surface a single bounded
// Steward turn needs: send exactly one turn, and clean up the session
// it created, whatever the outcome.
type cerberusClient interface {
	Turn(ctx context.Context, input cerberus.TurnInput) (cerberus.TurnOutput, error)
	Clean(ctx context.Context, session string) error
}

// planReviewStore is the narrow persistence surface RunStewardReview
// needs to record an honest queued -> running -> completed|failed
// lifecycle for one review attempt.
type planReviewStore interface {
	CreatePlanReview(ctx context.Context, p db.CreatePlanReviewParams) (db.PlanReview, error)
	StartPlanReview(ctx context.Context, id int64) (db.PlanReview, error)
	CompletePlanReview(ctx context.Context, id int64, verdict string, report json.RawMessage) (db.PlanReview, error)
	FailPlanReview(ctx context.Context, id int64, errMsg string) (db.PlanReview, error)
	// ListRunningPlanReviews returns every plan review still marked
	// running, across all plans, so an orphaned review (its executing
	// process is gone) can be found and reconciled to a terminal state
	// without any in-memory registry of in-flight reviews.
	ListRunningPlanReviews(ctx context.Context) ([]db.PlanReview, error)
}

type pgPlanReviewStore struct{ pool *pgxpool.Pool }

func (s pgPlanReviewStore) CreatePlanReview(ctx context.Context, p db.CreatePlanReviewParams) (db.PlanReview, error) {
	return db.CreatePlanReview(ctx, s.pool, p)
}

func (s pgPlanReviewStore) StartPlanReview(ctx context.Context, id int64) (db.PlanReview, error) {
	return db.StartPlanReview(ctx, s.pool, id)
}

func (s pgPlanReviewStore) CompletePlanReview(ctx context.Context, id int64, verdict string, report json.RawMessage) (db.PlanReview, error) {
	return db.CompletePlanReview(ctx, s.pool, id, verdict, report)
}

func (s pgPlanReviewStore) FailPlanReview(ctx context.Context, id int64, errMsg string) (db.PlanReview, error) {
	return db.FailPlanReview(ctx, s.pool, id, errMsg)
}

func (s pgPlanReviewStore) ListRunningPlanReviews(ctx context.Context) ([]db.PlanReview, error) {
	return db.ListRunningPlanReviews(ctx, s.pool)
}

// Service executes bounded Steward plan reviews.
type Service struct {
	store planReviewStore
	cerb  cerberusClient
}

// NewService builds a Service backed by pool for persistence and cerb
// for Steward sessions.
func NewService(pool *pgxpool.Pool, cerb *cerberus.Client) *Service {
	return newService(pgPlanReviewStore{pool: pool}, cerb)
}

func newService(store planReviewStore, cerb cerberusClient) *Service {
	return &Service{store: store, cerb: cerb}
}

// RunOptions bounds exactly one Steward review attempt: one plan
// snapshot, checked against one contract, in one cerberus session,
// using one configured model.
type RunOptions struct {
	Plan     db.Plan
	Steps    []db.PlanStep
	Feedback []db.Feedback
	Contract ContractSource
	// Model is the configured economical review model. Required.
	Model string
	// Timeout bounds the single cerberus turn this review sends.
	// Required; a review that exceeds it fails rather than hanging.
	Timeout time.Duration
}

// stewardReviewPrep is the fast, purely local half of one Steward
// review attempt: the queued-then-started PlanReview row plus the
// exact bounded turn (prompt, mounts) it still owes cerberus. It never
// touches cerberus itself, so preparing it is always quick, which is
// what lets both RunStewardReview and StartStewardReview return
// promptly up through "running" before the (possibly slow) cerberus
// turn is attempted.
type stewardReviewPrep struct {
	review db.PlanReview
	opts   RunOptions
	prompt string
}

// prepareReview builds the deterministic snapshot and contract-bound
// prompt for opts.Plan and persists the review's queued -> running
// transition, without ever calling cerberus. It is the synchronous,
// always-fast half of a Steward review attempt shared by
// RunStewardReview (which then runs the turn inline) and
// StartStewardReview (which then runs it in the background).
func (s *Service) prepareReview(ctx context.Context, opts RunOptions) (stewardReviewPrep, error) {
	if strings.TrimSpace(opts.Model) == "" {
		return stewardReviewPrep{}, fmt.Errorf("run steward review: model is required")
	}
	if opts.Timeout <= 0 {
		return stewardReviewPrep{}, fmt.Errorf("run steward review: timeout is required")
	}

	contract, err := LoadContract(opts.Contract)
	if err != nil {
		return stewardReviewPrep{}, fmt.Errorf("run steward review: %w", err)
	}
	snapshot, err := BuildSnapshot(opts.Plan, opts.Steps, opts.Feedback)
	if err != nil {
		return stewardReviewPrep{}, fmt.Errorf("run steward review: %w", err)
	}
	promptCtx, err := BuildContext(snapshot, contract)
	if err != nil {
		return stewardReviewPrep{}, fmt.Errorf("run steward review: %w", err)
	}

	session := stewardSessionName(opts.Plan.ID, snapshot.SHA256)

	review, err := s.store.CreatePlanReview(ctx, db.CreatePlanReviewParams{
		PlanID:          opts.Plan.ID,
		InputSnapshot:   snapshot.JSON,
		ContractVersion: contract.Version,
		ContractContent: contract.Content,
		Model:           opts.Model,
		Session:         session,
	})
	if err != nil {
		return stewardReviewPrep{}, fmt.Errorf("run steward review: create review: %w", err)
	}

	review, err = s.store.StartPlanReview(ctx, review.ID)
	if err != nil {
		return stewardReviewPrep{}, fmt.Errorf("run steward review: start review: %w", err)
	}

	return stewardReviewPrep{review: review, opts: opts, prompt: promptCtx.Prompt}, nil
}

// executeReview sends prep's single bounded cerberus turn, strictly
// validates the final assistant message as a two-pass report, and
// persists the review's running -> completed|failed transition. It
// always cleans up the cerberus session it created, whatever the
// outcome, and never persists a passing verdict unless Steward
// actually returned a well-formed report saying so.
func (s *Service) executeReview(ctx context.Context, prep stewardReviewPrep) (db.PlanReview, error) {
	review, opts := prep.review, prep.opts

	// From this point the session exists (or may exist) in cerberus, so
	// it is always cleaned up, whatever the turn or validation outcome.
	defer func() {
		_ = s.cerb.Clean(context.Background(), review.Session)
	}()

	turnCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	out, turnErr := s.cerb.Turn(turnCtx, cerberus.TurnInput{
		Name:        review.Session,
		NoRepo:      true,
		Model:       opts.Model,
		Message:     prep.prompt,
		ExtraMounts: BuildMountManifest(opts.Plan),
	})
	if turnErr != nil {
		return s.failReview(ctx, review.ID, fmt.Errorf("cerberus turn: %w", turnErr))
	}
	if out.Status == "error" {
		msg := out.Error
		if strings.TrimSpace(msg) == "" {
			msg = "cerberus turn returned an error status with no message"
		}
		return s.failReview(ctx, review.ID, fmt.Errorf("cerberus turn: %s", msg))
	}
	if strings.TrimSpace(out.Message) == "" {
		return s.failReview(ctx, review.ID, fmt.Errorf("cerberus turn returned no final assistant message"))
	}

	raw, err := extractReportJSON(out.Message)
	if err != nil {
		return s.failReview(ctx, review.ID, fmt.Errorf("malformed steward report: %w", err))
	}
	verdict, err := validateReport(raw)
	if err != nil {
		return s.failReview(ctx, review.ID, fmt.Errorf("malformed steward report: %w", err))
	}

	completed, err := s.store.CompletePlanReview(ctx, review.ID, verdict, raw)
	if err != nil {
		return db.PlanReview{}, fmt.Errorf("run steward review: complete review: %w", err)
	}
	return completed, nil
}

// RunStewardReview runs exactly one Steward review of opts.Plan
// end-to-end and blocks until it reaches a terminal, persisted
// queued -> running -> completed|failed outcome: it prepares the
// review (see prepareReview) and then executes its bounded cerberus
// turn (see executeReview) inline, in the caller's own goroutine.
func (s *Service) RunStewardReview(ctx context.Context, opts RunOptions) (db.PlanReview, error) {
	prep, err := s.prepareReview(ctx, opts)
	if err != nil {
		return db.PlanReview{}, err
	}
	return s.executeReview(ctx, prep)
}

// StartStewardReview creates and starts exactly one Steward review the
// same way RunStewardReview does, then hands its bounded cerberus turn
// to a background goroutine and returns as soon as the review has left
// queued for running, without waiting for that turn to resolve. This
// is what lets review creation return promptly.
//
// The review this returns is not an anonymous, in-memory handle: it is
// the durable PlanReview row (keyed to the durable cerberus session
// prepareReview named for it), so its eventual completed or failed
// outcome is always later visible via GetPlanReview/ListPlanReviews
// even though this call has already returned, and
// ReconcileInterruptedReviews can always find and terminate it if the
// goroutine executing it never gets the chance to.
func (s *Service) StartStewardReview(ctx context.Context, opts RunOptions) (db.PlanReview, error) {
	prep, err := s.prepareReview(ctx, opts)
	if err != nil {
		return db.PlanReview{}, err
	}
	go func() {
		if _, err := s.executeReview(context.Background(), prep); err != nil {
			log.Printf("steward review %d (plan %d): %v", prep.review.ID, prep.opts.Plan.ID, err)
		}
	}()
	return prep.review, nil
}

// ReconcileInterruptedReviews fails every plan review still marked
// running, on the assumption that whatever was executing it (a
// StartStewardReview goroutine) is gone: an in-memory goroutine never
// survives a process restart, so a review left running by one is an
// orphan that would otherwise stay "running" forever with no terminal
// outcome ever persisted. It should be called once at startup, before
// any new review is started, so every review row is guaranteed to
// reach a terminal, persisted outcome instead of depending on an
// anonymous in-memory job surviving a restart. It returns the number
// of reviews it failed.
func (s *Service) ReconcileInterruptedReviews(ctx context.Context) (int, error) {
	running, err := s.store.ListRunningPlanReviews(ctx)
	if err != nil {
		return 0, fmt.Errorf("reconcile interrupted plan reviews: %w", err)
	}
	failed := 0
	for _, rv := range running {
		_ = s.cerb.Clean(ctx, rv.Session)
		if _, err := s.store.FailPlanReview(ctx, rv.ID, "steward review was interrupted before completing (service restarted while it was running)"); err != nil {
			return failed, fmt.Errorf("reconcile interrupted plan reviews: fail review %d: %w", rv.ID, err)
		}
		failed++
	}
	return failed, nil
}

// failReview records cause as review's terminal failure and returns
// cause as the error RunStewardReview reports. If the failure itself
// cannot be persisted, both errors are surfaced, but the review is
// never reported as anything other than failed.
func (s *Service) failReview(ctx context.Context, id int64, cause error) (db.PlanReview, error) {
	failed, ferr := s.store.FailPlanReview(ctx, id, cause.Error())
	if ferr != nil {
		return db.PlanReview{}, fmt.Errorf("%w (also failed to record failure: %v)", cause, ferr)
	}
	return failed, cause
}

// stewardSessionName derives a cerberus session name for one review
// attempt of planID at snapshot fingerprint, unique per attempt so
// repeated reviews of the same plan never collide on an in-flight or
// leftover session name.
func stewardSessionName(planID int64, fingerprint string) string {
	short := fingerprint
	if len(short) > 12 {
		short = short[:12]
	}
	return fmt.Sprintf("foundry-steward-%d-%s-%d", planID, short, time.Now().UnixNano())
}

// extractReportJSON locates the first balanced top-level JSON object in
// message and returns it verbatim, so prose surrounding Steward's final
// report is never treated as part of it.
func extractReportJSON(message string) (json.RawMessage, error) {
	start := strings.IndexByte(message, '{')
	if start == -1 {
		return nil, fmt.Errorf("no JSON object found in final message")
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(message); i++ {
		c := message[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return json.RawMessage(message[start : i+1]), nil
			}
		}
	}
	return nil, fmt.Errorf("unbalanced JSON object in final message")
}

// validateReport strictly decodes raw as a report object containing
// exactly requiredReportFields and no others, with a verdict in
// validReportVerdicts, and returns that verdict. It fails on any
// missing field, any extra field, any trailing content after the
// object, or any verdict outside the closed set, so malformed Steward
// output can never become a persisted pass.
func validateReport(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	var fields map[string]json.RawMessage
	if err := dec.Decode(&fields); err != nil {
		return "", fmt.Errorf("parse report: %w", err)
	}
	if dec.More() {
		return "", fmt.Errorf("report contains trailing content after its JSON object")
	}

	for _, field := range requiredReportFields {
		if _, ok := fields[field]; !ok {
			return "", fmt.Errorf("report is missing required field %q", field)
		}
	}
	if len(fields) != len(requiredReportFields) {
		return "", fmt.Errorf("report contains unexpected fields")
	}

	var verdict string
	if err := json.Unmarshal(fields[ReportFieldVerdict], &verdict); err != nil {
		return "", fmt.Errorf("report verdict is not a string: %w", err)
	}
	if !validReportVerdicts[verdict] {
		return "", fmt.Errorf("report verdict %q is not one of pass, revise, escalate", verdict)
	}
	return verdict, nil
}
