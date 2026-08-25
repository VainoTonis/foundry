ALTER TABLE plan_reviews DROP CONSTRAINT plan_reviews_lifecycle_check;
ALTER TABLE plan_reviews DROP CONSTRAINT plan_reviews_status_check;

UPDATE plan_reviews SET status = 'pending' WHERE status IN ('queued', 'running');

ALTER TABLE plan_reviews ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE plan_reviews DROP COLUMN started_at;

ALTER TABLE plan_reviews ADD CONSTRAINT plan_reviews_status_check
    CHECK (status IN ('pending', 'completed', 'failed'));

ALTER TABLE plan_reviews ADD CONSTRAINT plan_reviews_lifecycle_check CHECK (
    (status = 'pending'   AND verdict IS NULL     AND report IS NULL     AND error IS NULL     AND completed_at IS NULL) OR
    (status = 'completed' AND verdict IS NOT NULL AND report IS NOT NULL AND error IS NULL     AND completed_at IS NOT NULL) OR
    (status = 'failed'    AND verdict IS NULL      AND report IS NULL     AND error IS NOT NULL AND completed_at IS NOT NULL)
);
