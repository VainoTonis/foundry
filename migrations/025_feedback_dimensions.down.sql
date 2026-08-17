DROP INDEX IF EXISTS idx_feedback_session_id;
DROP INDEX IF EXISTS idx_feedback_status;
DROP INDEX IF EXISTS idx_feedback_dimension;

ALTER TABLE feedback
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS owner,
    DROP COLUMN IF EXISTS recommended_action,
    DROP COLUMN IF EXISTS impact,
    DROP COLUMN IF EXISTS evidence,
    DROP COLUMN IF EXISTS tags,
    DROP COLUMN IF EXISTS score,
    DROP COLUMN IF EXISTS target,
    DROP COLUMN IF EXISTS dimension;

UPDATE feedback SET body = '' WHERE body IS NULL;
ALTER TABLE feedback ALTER COLUMN body SET NOT NULL;
