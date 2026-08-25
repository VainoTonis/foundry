package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// PlanReview is an immutable, fingerprinted record of one Steward review
// run against a plan. Every review persists the exact input snapshot it
// was given and the exact contract content it was checked against,
// alongside SHA-256 fingerprints computed from that exact content, so a
// review's provenance can be verified without trusting the caller.
//
// A review starts queued, moves to running once a Steward pass actually
// starts working it, and ends exactly once, either completed (with a
// Verdict and structured Report) or failed (with an Error). Once
// terminal, a review row is never mutated again: StartPlanReview fails
// with ErrPlanReviewNotQueued if called on a review that has already
// left queued, and CompletePlanReview and FailPlanReview both fail with
// ErrPlanReviewNotRunning if called on a review that is not running
// (still queued, or already terminal).
type PlanReview struct {
	ID                  int64           `json:"id"`
	PlanID              int64           `json:"plan_id"`
	InputSnapshot       json.RawMessage `json:"input_snapshot"`
	InputSnapshotSHA256 string          `json:"input_snapshot_sha256"`
	ContractVersion     string          `json:"contract_version"`
	ContractSHA256      string          `json:"contract_sha256"`
	Model               string          `json:"model"`
	Session             string          `json:"session"`
	Status              string          `json:"status"`
	Verdict             *string         `json:"verdict,omitempty"`
	Report              json.RawMessage `json:"report,omitempty"`
	Error               *string         `json:"error,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	StartedAt           *time.Time      `json:"started_at,omitempty"`
	CompletedAt         *time.Time      `json:"completed_at,omitempty"`
}

// Plan review lifecycle states.
const (
	PlanReviewStatusQueued    = "queued"
	PlanReviewStatusRunning   = "running"
	PlanReviewStatusCompleted = "completed"
	PlanReviewStatusFailed    = "failed"
)

// Plan review verdicts, only ever set on a completed review.
const (
	PlanReviewVerdictPass     = "pass"
	PlanReviewVerdictRevise   = "revise"
	PlanReviewVerdictEscalate = "escalate"
)

var validPlanReviewVerdicts = map[string]bool{
	PlanReviewVerdictPass:     true,
	PlanReviewVerdictRevise:   true,
	PlanReviewVerdictEscalate: true,
}

// ErrPlanReviewNotQueued is returned by StartPlanReview when the
// targeted review has already left the queued state (it is already
// running or terminal).
var ErrPlanReviewNotQueued = errors.New("plan review is not queued")

// ErrPlanReviewNotRunning is returned by CompletePlanReview and
// FailPlanReview when the targeted review is not running (it is still
// queued, or was already completed or failed). Plan reviews are
// immutable once terminal, so a second transition attempt is rejected
// rather than overwriting the first outcome.
var ErrPlanReviewNotRunning = errors.New("plan review is not running")

const planReviewSelectColumns = `id, plan_id, input_snapshot, input_snapshot_sha256, contract_version, contract_sha256,
	model, session, status, verdict, report, error, created_at, started_at, completed_at`

func scanPlanReview(row pgx.Row) (PlanReview, error) {
	var r PlanReview
	err := row.Scan(&r.ID, &r.PlanID, &r.InputSnapshot, &r.InputSnapshotSHA256, &r.ContractVersion, &r.ContractSHA256,
		&r.Model, &r.Session, &r.Status, &r.Verdict, &r.Report, &r.Error, &r.CreatedAt, &r.StartedAt, &r.CompletedAt)
	return r, err
}

// CreatePlanReviewParams holds the exact review input. InputSnapshot and
// ContractContent are persisted verbatim; their SHA-256 fingerprints are
// computed by CreatePlanReview itself from that exact content (never
// accepted from the caller), so a stored fingerprint can never drift
// from what is actually stored.
type CreatePlanReviewParams struct {
	PlanID          int64
	InputSnapshot   json.RawMessage
	ContractVersion string
	ContractContent string
	Model           string
	Session         string
}

// CreatePlanReview inserts a new plan review in the queued state. It
// fails, persisting nothing, if InputSnapshot, ContractVersion,
// ContractContent, Model, or Session is empty, or if PlanID does not
// name an existing plan (in which case the returned error wraps
// ErrNotFound).
func CreatePlanReview(ctx context.Context, pool querier, p CreatePlanReviewParams) (PlanReview, error) {
	if len(p.InputSnapshot) == 0 {
		return PlanReview{}, fmt.Errorf("create plan review: input snapshot is required")
	}
	if p.ContractVersion == "" {
		return PlanReview{}, fmt.Errorf("create plan review: contract version is required")
	}
	if p.ContractContent == "" {
		return PlanReview{}, fmt.Errorf("create plan review: contract content is required")
	}
	if p.Model == "" {
		return PlanReview{}, fmt.Errorf("create plan review: model is required")
	}
	if p.Session == "" {
		return PlanReview{}, fmt.Errorf("create plan review: session is required")
	}

	inputHash := sha256Hex(p.InputSnapshot)
	contractHash := sha256Hex([]byte(p.ContractContent))

	row := pool.QueryRow(ctx,
		`INSERT INTO plan_reviews (plan_id, input_snapshot, input_snapshot_sha256, contract_version, contract_sha256, model, session, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, 'queued')
		 RETURNING `+planReviewSelectColumns,
		p.PlanID, p.InputSnapshot, inputHash, p.ContractVersion, contractHash, p.Model, p.Session,
	)
	r, err := scanPlanReview(row)
	if err != nil {
		if isForeignKeyViolation(err) {
			return PlanReview{}, fmt.Errorf("create plan review: plan %d does not exist: %w", p.PlanID, ErrNotFound)
		}
		return PlanReview{}, err
	}
	return r, nil
}

// StartPlanReview transitions a queued plan review to running, recording
// the time it left queued. If the review does not exist, the error
// wraps ErrNotFound; if it exists but is no longer queued, the error
// wraps ErrPlanReviewNotQueued.
func StartPlanReview(ctx context.Context, pool querier, id int64) (PlanReview, error) {
	row := pool.QueryRow(ctx,
		`UPDATE plan_reviews SET status = 'running', started_at = now()
		 WHERE id = $1 AND status = 'queued'
		 RETURNING `+planReviewSelectColumns,
		id,
	)
	r, err := scanPlanReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanReview{}, planReviewStartTransitionErr(ctx, pool, id)
	}
	if err != nil {
		return PlanReview{}, err
	}
	return r, nil
}

// CompletePlanReview transitions a running plan review to completed,
// recording its verdict and structured terminal report. Verdict must be
// one of PlanReviewVerdictPass, PlanReviewVerdictRevise, or
// PlanReviewVerdictEscalate, and report must be non-empty; otherwise the
// call fails without touching the row. If the review does not exist, the
// error wraps ErrNotFound; if it exists but is not running, the error
// wraps ErrPlanReviewNotRunning.
func CompletePlanReview(ctx context.Context, pool querier, id int64, verdict string, report json.RawMessage) (PlanReview, error) {
	if !validPlanReviewVerdicts[verdict] {
		return PlanReview{}, fmt.Errorf("complete plan review: invalid verdict %q", verdict)
	}
	if len(report) == 0 {
		return PlanReview{}, fmt.Errorf("complete plan review: report is required")
	}

	row := pool.QueryRow(ctx,
		`UPDATE plan_reviews SET status = 'completed', verdict = $1, report = $2, completed_at = now()
		 WHERE id = $3 AND status = 'running'
		 RETURNING `+planReviewSelectColumns,
		verdict, report, id,
	)
	r, err := scanPlanReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanReview{}, planReviewTransitionErr(ctx, pool, id)
	}
	if err != nil {
		return PlanReview{}, err
	}
	return r, nil
}

// FailPlanReview transitions a running plan review to failed, recording
// a non-empty error message and no verdict or report. If the review
// does not exist, the error wraps ErrNotFound; if it exists but is not
// running, the error wraps ErrPlanReviewNotRunning.
func FailPlanReview(ctx context.Context, pool querier, id int64, errMsg string) (PlanReview, error) {
	if errMsg == "" {
		return PlanReview{}, fmt.Errorf("fail plan review: error message is required")
	}

	row := pool.QueryRow(ctx,
		`UPDATE plan_reviews SET status = 'failed', error = $1, completed_at = now()
		 WHERE id = $2 AND status = 'running'
		 RETURNING `+planReviewSelectColumns,
		errMsg, id,
	)
	r, err := scanPlanReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanReview{}, planReviewTransitionErr(ctx, pool, id)
	}
	if err != nil {
		return PlanReview{}, err
	}
	return r, nil
}

// planReviewStartTransitionErr distinguishes "no such review" from
// "review exists but is no longer queued" after an UPDATE ... WHERE
// status = 'queued' matched zero rows, so StartPlanReview can report the
// right one of ErrNotFound or ErrPlanReviewNotQueued.
func planReviewStartTransitionErr(ctx context.Context, pool querier, id int64) error {
	var status string
	err := pool.QueryRow(ctx, `SELECT status FROM plan_reviews WHERE id = $1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrPlanReviewNotQueued
}

// planReviewTransitionErr distinguishes "no such review" from "review
// exists but is not running" after an UPDATE ... WHERE status = 'running'
// matched zero rows, so CompletePlanReview and FailPlanReview can report
// the right one of ErrNotFound or ErrPlanReviewNotRunning.
func planReviewTransitionErr(ctx context.Context, pool querier, id int64) error {
	var status string
	err := pool.QueryRow(ctx, `SELECT status FROM plan_reviews WHERE id = $1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrPlanReviewNotRunning
}

// GetPlanReview returns a single plan review by id, or ErrNotFound.
func GetPlanReview(ctx context.Context, pool querier, id int64) (PlanReview, error) {
	row := pool.QueryRow(ctx, `SELECT `+planReviewSelectColumns+` FROM plan_reviews WHERE id = $1`, id)
	r, err := scanPlanReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanReview{}, ErrNotFound
	}
	return r, err
}

// ListPlanReviews returns every review for planID, most recent first.
func ListPlanReviews(ctx context.Context, pool querier, planID int64) ([]PlanReview, error) {
	rows, err := pool.Query(ctx, `SELECT `+planReviewSelectColumns+` FROM plan_reviews WHERE plan_id = $1 ORDER BY id DESC`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanReview
	for rows.Next() {
		r, err := scanPlanReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetLatestPlanReviewByInputHash returns the most recently created
// review of planID whose input_snapshot_sha256 matches inputHash, so a
// caller holding a freshly computed snapshot fingerprint can tell
// whether an existing review already covers that exact input without
// re-running Steward. It returns ErrNotFound if no such review exists.
func GetLatestPlanReviewByInputHash(ctx context.Context, pool querier, planID int64, inputHash string) (PlanReview, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+planReviewSelectColumns+` FROM plan_reviews
		 WHERE plan_id = $1 AND input_snapshot_sha256 = $2
		 ORDER BY created_at DESC, id DESC LIMIT 1`,
		planID, inputHash,
	)
	r, err := scanPlanReview(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanReview{}, ErrNotFound
	}
	return r, err
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
