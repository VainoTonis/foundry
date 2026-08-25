-- Migrate plan_reviews to the canonical queued -> running -> completed /
-- failed lifecycle from Plan 90. A review is queued when created, moves
-- to running once a Steward pass actually starts working it, and ends
-- exactly once in completed or failed. started_at records when a review
-- left queued, distinct from completed_at which records when it left
-- running. All existing provenance columns (input snapshot, fingerprints,
-- contract/model/session attribution) are untouched.

-- Both existing CHECK constraints must be dropped before status is
-- rewritten below: plan_reviews_status_check still only allows the old
-- pending/completed/failed values, so backfilling 'queued' into
-- existing 038 rows while it is still active would be rejected outright.
ALTER TABLE plan_reviews DROP CONSTRAINT plan_reviews_lifecycle_check;
ALTER TABLE plan_reviews DROP CONSTRAINT plan_reviews_status_check;

UPDATE plan_reviews SET status = 'queued' WHERE status = 'pending';

ALTER TABLE plan_reviews ADD COLUMN started_at TIMESTAMPTZ;

-- Existing completed/failed rows predate started_at and have no record
-- of when they actually began running. Backfill started_at from
-- completed_at so these terminal rows satisfy the lifecycle constraint
-- below without fabricating a distinct start time.
UPDATE plan_reviews SET started_at = completed_at WHERE status IN ('completed', 'failed');

ALTER TABLE plan_reviews ALTER COLUMN status SET DEFAULT 'queued';

ALTER TABLE plan_reviews ADD CONSTRAINT plan_reviews_status_check
    CHECK (status IN ('queued', 'running', 'completed', 'failed'));

-- Lifecycle consistency: queued rows carry no started/terminal fields;
-- running rows carry started_at but no terminal fields; completed rows
-- must carry a verdict and report and no error; failed rows must carry
-- an error and no verdict or report. Both terminal states require
-- started_at and completed_at, since a review can only reach a terminal
-- state after having run. This keeps a review's persisted shape
-- self-describing and prevents a half-terminal row from ever being
-- committed.
ALTER TABLE plan_reviews ADD CONSTRAINT plan_reviews_lifecycle_check CHECK (
    (status = 'queued'    AND started_at IS NULL     AND verdict IS NULL     AND report IS NULL     AND error IS NULL     AND completed_at IS NULL) OR
    (status = 'running'   AND started_at IS NOT NULL AND verdict IS NULL     AND report IS NULL     AND error IS NULL     AND completed_at IS NULL) OR
    (status = 'completed' AND started_at IS NOT NULL AND verdict IS NOT NULL AND report IS NOT NULL AND error IS NULL     AND completed_at IS NOT NULL) OR
    (status = 'failed'    AND started_at IS NOT NULL AND verdict IS NULL     AND report IS NULL     AND error IS NOT NULL AND completed_at IS NOT NULL)
);
