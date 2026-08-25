package review

import (
	"context"
	"encoding/json"
	"fmt"
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

// RunStewardReview builds the deterministic snapshot and contract-bound
// prompt for opts.Plan, mounts every locally available plan repository
// read-only, sends exactly one bounded cerberus turn using opts.Model,
// strictly validates the final assistant message as a two-pass report,
// and persists an honest queued -> running -> completed|failed
// lifecycle. It always cleans up the cerberus session it created,
// whatever the outcome, and never persists a passing verdict unless
// Steward actually returned a well-formed report saying so.
func (s *Service) RunStewardReview(ctx context.Context, opts RunOptions) (db.PlanReview, error) {
	if strings.TrimSpace(opts.Model) == "" {
		return db.PlanReview{}, fmt.Errorf("run steward review: model is required")
	}
	if opts.Timeout <= 0 {
		return db.PlanReview{}, fmt.Errorf("run steward review: timeout is required")
	}

	contract, err := LoadContract(opts.Contract)
	if err != nil {
		return db.PlanReview{}, fmt.Errorf("run steward review: %w", err)
	}
	snapshot, err := BuildSnapshot(opts.Plan, opts.Steps, opts.Feedback)
	if err != nil {
		return db.PlanReview{}, fmt.Errorf("run steward review: %w", err)
	}
	promptCtx, err := BuildContext(snapshot, contract)
	if err != nil {
		return db.PlanReview{}, fmt.Errorf("run steward review: %w", err)
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
		return db.PlanReview{}, fmt.Errorf("run steward review: create review: %w", err)
	}

	if _, err := s.store.StartPlanReview(ctx, review.ID); err != nil {
		return db.PlanReview{}, fmt.Errorf("run steward review: start review: %w", err)
	}

	// From this point the session exists (or may exist) in cerberus, so
	// it is always cleaned up, whatever the turn or validation outcome.
	defer func() {
		_ = s.cerb.Clean(context.Background(), session)
	}()

	turnCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	out, turnErr := s.cerb.Turn(turnCtx, cerberus.TurnInput{
		Name:        session,
		NoRepo:      true,
		Model:       opts.Model,
		Message:     promptCtx.Prompt,
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
