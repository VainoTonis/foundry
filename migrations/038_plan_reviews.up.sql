CREATE TABLE plan_reviews (
    id                    BIGSERIAL PRIMARY KEY,
    plan_id               BIGINT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    input_snapshot        JSONB NOT NULL,
    input_snapshot_sha256 TEXT NOT NULL,
    contract_version      TEXT NOT NULL,
    contract_sha256       TEXT NOT NULL,
    model                 TEXT NOT NULL,
    session               TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'pending',
    verdict               TEXT,
    report                JSONB,
    error                 TEXT,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at          TIMESTAMPTZ,
    CONSTRAINT plan_reviews_status_check CHECK (status IN ('pending', 'completed', 'failed')),
    CONSTRAINT plan_reviews_verdict_check CHECK (verdict IS NULL OR verdict IN ('pass', 'revise', 'escalate')),
    -- Lifecycle consistency: pending rows carry no terminal fields yet;
    -- completed rows must carry a verdict and report and no error;
    -- failed rows must carry an error and no verdict or report. This
    -- keeps a review's persisted shape self-describing and prevents a
    -- half-terminal row from ever being committed.
    CONSTRAINT plan_reviews_lifecycle_check CHECK (
        (status = 'pending'   AND verdict IS NULL     AND report IS NULL     AND error IS NULL     AND completed_at IS NULL) OR
        (status = 'completed' AND verdict IS NOT NULL AND report IS NOT NULL AND error IS NULL     AND completed_at IS NOT NULL) OR
        (status = 'failed'    AND verdict IS NULL      AND report IS NULL     AND error IS NOT NULL AND completed_at IS NOT NULL)
    )
);

CREATE INDEX ON plan_reviews(plan_id);

-- Supports "does a review already exist for this exact plan input"
-- lookups: most recent review for a plan with a given input snapshot
-- fingerprint.
CREATE INDEX ON plan_reviews(plan_id, input_snapshot_sha256, created_at DESC);
