BEGIN;

ALTER TABLE media_jobs
    ADD COLUMN lease_generation bigint NOT NULL DEFAULT 0
        CHECK (lease_generation >= 0),
    ADD CONSTRAINT media_jobs_pending_attempts_check
        CHECK (state <> 0 OR attempts < max_attempts),
    ADD CONSTRAINT media_jobs_running_lease_check
        CHECK (
            state <> 1 OR (
                attempts BETWEEN 1 AND max_attempts AND
                claimed_at IS NOT NULL AND
                claimed_by IS NOT NULL AND
                btrim(claimed_by) <> '' AND
                lease_expires_at IS NOT NULL
            )
        ),
    ADD CONSTRAINT media_jobs_nonrunning_lease_clear_check
        CHECK (
            state = 1 OR (
                claimed_at IS NULL AND
                claimed_by IS NULL AND
                lease_expires_at IS NULL
            )
        );

DROP INDEX media_jobs_claim_idx;

CREATE INDEX media_jobs_runnable_idx ON media_jobs (
    (CASE WHEN state = 0 THEN available_at ELSE lease_expires_at END),
    priority DESC,
    id
)
WHERE state IN (0, 1);

COMMIT;
