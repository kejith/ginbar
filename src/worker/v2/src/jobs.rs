use postgres::{Client, Error};
use std::num::NonZeroU64;
use std::time::Duration;

const STATE_PENDING: i16 = 0;
const STATE_RUNNING: i16 = 1;
#[cfg(test)]
const STATE_SUCCEEDED: i16 = 2;
const STATE_FAILED: i16 = 3;

const CLAIM_ONE_SQL: &str = r#"
WITH candidate AS MATERIALIZED (
    SELECT id, state
    FROM media_jobs
    WHERE state IN (0, 1)
      AND (CASE WHEN state = 0 THEN available_at ELSE lease_expires_at END) <= now()
    ORDER BY
        (CASE WHEN state = 0 THEN available_at ELSE lease_expires_at END),
        priority DESC,
        id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
), updated AS (
    UPDATE media_jobs AS job
    SET
        state = CASE
            WHEN candidate.state = 1 AND job.attempts >= job.max_attempts THEN 3
            ELSE 1
        END,
        attempts = CASE
            WHEN candidate.state = 1 AND job.attempts >= job.max_attempts THEN job.attempts
            ELSE job.attempts + 1
        END,
        claimed_at = CASE
            WHEN candidate.state = 1 AND job.attempts >= job.max_attempts THEN NULL
            ELSE clock_timestamp()
        END,
        claimed_by = CASE
            WHEN candidate.state = 1 AND job.attempts >= job.max_attempts THEN NULL
            ELSE $1
        END,
        lease_expires_at = CASE
            WHEN candidate.state = 1 AND job.attempts >= job.max_attempts THEN NULL
            ELSE clock_timestamp() + ($2::bigint * interval '1 millisecond')
        END,
        lease_generation = CASE
            WHEN candidate.state = 1 AND job.attempts >= job.max_attempts THEN job.lease_generation
            ELSE job.lease_generation + 1
        END,
        last_error = CASE
            WHEN candidate.state = 1 AND job.attempts >= job.max_attempts
                THEN COALESCE(job.last_error, 'lease expired after final attempt')
            ELSE job.last_error
        END,
        updated_at = clock_timestamp()
    FROM candidate
    WHERE job.id = candidate.id
    RETURNING
        job.id,
        job.post_id,
        job.kind,
        job.state,
        job.attempts,
        job.max_attempts,
        job.lease_generation
)
SELECT id, post_id, kind, state, attempts, max_attempts, lease_generation
FROM updated
"#;

const RENEW_LEASE_SQL: &str = r#"
WITH owned AS MATERIALIZED (
    SELECT id, lease_expires_at
    FROM media_jobs
    WHERE id = $1
      AND state = 1
      AND claimed_by = $2
      AND lease_generation = $3
    FOR UPDATE
)
UPDATE media_jobs AS job
SET
    lease_expires_at = clock_timestamp() + ($4::bigint * interval '1 millisecond'),
    updated_at = clock_timestamp()
FROM owned
WHERE job.id = owned.id
  AND owned.lease_expires_at > clock_timestamp()
RETURNING job.id
"#;

const COMPLETE_SQL: &str = r#"
WITH owned AS MATERIALIZED (
    SELECT id, lease_expires_at
    FROM media_jobs
    WHERE id = $1
      AND state = 1
      AND claimed_by = $2
      AND lease_generation = $3
    FOR UPDATE
)
UPDATE media_jobs AS job
SET
    state = 2,
    claimed_at = NULL,
    claimed_by = NULL,
    lease_expires_at = NULL,
    last_error = NULL,
    updated_at = clock_timestamp()
FROM owned
WHERE job.id = owned.id
  AND owned.lease_expires_at > clock_timestamp()
RETURNING job.id
"#;

const FAIL_SQL: &str = r#"
WITH owned AS MATERIALIZED (
    SELECT id, lease_expires_at
    FROM media_jobs
    WHERE id = $1
      AND state = 1
      AND claimed_by = $2
      AND lease_generation = $3
    FOR UPDATE
)
UPDATE media_jobs AS job
SET
    state = CASE
        WHEN NOT $5 OR job.attempts >= job.max_attempts THEN 3
        ELSE 0
    END,
    available_at = CASE
        WHEN $5 AND job.attempts < job.max_attempts
            THEN clock_timestamp() + ($4::bigint * interval '1 millisecond')
        ELSE job.available_at
    END,
    claimed_at = NULL,
    claimed_by = NULL,
    lease_expires_at = NULL,
    last_error = $6,
    updated_at = clock_timestamp()
FROM owned
WHERE job.id = owned.id
  AND owned.lease_expires_at > clock_timestamp()
RETURNING job.state
"#;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct JobLease {
    pub id: i64,
    pub post_id: i64,
    pub kind: i16,
    pub attempt: i32,
    pub max_attempts: i32,
    pub lease_generation: i64,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ClaimOutcome {
    Claimed(JobLease),
    ExhaustedExpired { id: i64 },
    None,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FailureOutcome {
    RetryScheduled,
    Terminal,
    LeaseLost,
}

pub struct JobStore<'a> {
    client: &'a mut Client,
}

impl<'a> JobStore<'a> {
    pub fn new(client: &'a mut Client) -> Self {
        Self { client }
    }

    pub fn claim_one(
        &mut self,
        worker_id: &str,
        lease_ms: NonZeroU64,
    ) -> Result<ClaimOutcome, Error> {
        let lease_ms = lease_ms_i64(lease_ms);
        let row = self
            .client
            .query_opt(CLAIM_ONE_SQL, &[&worker_id, &lease_ms])?;
        let Some(row) = row else {
            return Ok(ClaimOutcome::None);
        };

        let id: i64 = row.get("id");
        let state: i16 = row.get("state");
        if state == STATE_FAILED {
            return Ok(ClaimOutcome::ExhaustedExpired { id });
        }

        debug_assert_eq!(state, STATE_RUNNING);
        Ok(ClaimOutcome::Claimed(JobLease {
            id,
            post_id: row.get("post_id"),
            kind: row.get("kind"),
            attempt: row.get("attempts"),
            max_attempts: row.get("max_attempts"),
            lease_generation: row.get("lease_generation"),
        }))
    }

    pub fn renew_lease(
        &mut self,
        lease: &JobLease,
        worker_id: &str,
        lease_ms: NonZeroU64,
    ) -> Result<bool, Error> {
        let lease_ms = lease_ms_i64(lease_ms);
        Ok(self
            .client
            .query_opt(
                RENEW_LEASE_SQL,
                &[&lease.id, &worker_id, &lease.lease_generation, &lease_ms],
            )?
            .is_some())
    }

    pub fn complete(&mut self, lease: &JobLease, worker_id: &str) -> Result<bool, Error> {
        Ok(self
            .client
            .query_opt(
                COMPLETE_SQL,
                &[&lease.id, &worker_id, &lease.lease_generation],
            )?
            .is_some())
    }

    pub fn fail(
        &mut self,
        lease: &JobLease,
        worker_id: &str,
        retryable: bool,
        retry_after: Duration,
        error: &str,
    ) -> Result<FailureOutcome, Error> {
        let retry_ms = duration_ms_i64(retry_after);
        let row = self.client.query_opt(
            FAIL_SQL,
            &[
                &lease.id,
                &worker_id,
                &lease.lease_generation,
                &retry_ms,
                &retryable,
                &error,
            ],
        )?;
        let Some(row) = row else {
            return Ok(FailureOutcome::LeaseLost);
        };

        let state: i16 = row.get("state");
        match state {
            STATE_PENDING => Ok(FailureOutcome::RetryScheduled),
            STATE_FAILED => Ok(FailureOutcome::Terminal),
            _ => unreachable!("failure transition returned unexpected state {state}"),
        }
    }
}

pub fn retry_delay(attempt: i32, base: Duration, max: Duration) -> Duration {
    let shift = attempt.saturating_sub(1).clamp(0, 20) as u32;
    let multiplier = 1u128 << shift;
    let base_ms = base.as_millis();
    let max_ms = max.as_millis();
    let delay_ms = base_ms.saturating_mul(multiplier).min(max_ms);
    Duration::from_millis(delay_ms.min(u64::MAX as u128) as u64)
}

fn lease_ms_i64(lease_ms: NonZeroU64) -> i64 {
    lease_ms.get().min(i64::MAX as u64) as i64
}

fn duration_ms_i64(duration: Duration) -> i64 {
    duration.as_millis().min(i64::MAX as u128) as i64
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn retry_delay_is_bounded_exponential() {
        let base = Duration::from_secs(2);
        let max = Duration::from_secs(30);
        assert_eq!(retry_delay(1, base, max), Duration::from_secs(2));
        assert_eq!(retry_delay(2, base, max), Duration::from_secs(4));
        assert_eq!(retry_delay(4, base, max), Duration::from_secs(16));
        assert_eq!(retry_delay(5, base, max), Duration::from_secs(30));
        assert_eq!(retry_delay(100, base, max), Duration::from_secs(30));
    }

    #[test]
    fn state_constants_match_schema_contract() {
        assert_eq!(STATE_PENDING, 0);
        assert_eq!(STATE_RUNNING, 1);
        assert_eq!(STATE_SUCCEEDED, 2);
        assert_eq!(STATE_FAILED, 3);
    }
}
