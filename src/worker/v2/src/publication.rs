use crate::jobs::JobLease;
use crate::processing::{validate_processed_storage_key, MediaKind, ProcessedMedia};
use postgres::{Client, Error as PgError};
use std::fmt;
use std::fmt::Write as _;

const PUBLISH_SQL: &str = r#"
WITH owned AS MATERIALIZED (
    SELECT job.id, job.post_id, job.lease_expires_at
    FROM media_jobs AS job
    JOIN media_sources AS source ON source.post_id = job.post_id
    JOIN posts AS post ON post.id = job.post_id
    WHERE job.id = $1
      AND job.post_id = $2
      AND job.kind = 0
      AND job.state = 1
      AND job.claimed_by = $3
      AND job.lease_generation = $4
      AND source.sha256 = $5
      AND post.deleted_at IS NULL
      AND post.release_state IN (0, 1)
    FOR UPDATE OF job, post
), valid AS MATERIALIZED (
    SELECT id, post_id FROM owned
    WHERE lease_expires_at > clock_timestamp()
), written_media AS (
    INSERT INTO media (
        post_id, kind, processing_state, storage_key, mime_type,
        width, height, duration_ms, byte_size, sha256, perceptual_hash
    )
    SELECT valid.post_id, $6, 1, $7, $8, $9, $10, $11, $12, $13, $14
    FROM valid
    ON CONFLICT (post_id) DO UPDATE
    SET kind = EXCLUDED.kind,
        processing_state = 1,
        mime_type = EXCLUDED.mime_type,
        width = EXCLUDED.width,
        height = EXCLUDED.height,
        duration_ms = EXCLUDED.duration_ms,
        byte_size = EXCLUDED.byte_size,
        perceptual_hash = EXCLUDED.perceptual_hash,
        updated_at = clock_timestamp()
    WHERE media.storage_key = EXCLUDED.storage_key
      AND media.sha256 = EXCLUDED.sha256
    RETURNING post_id
), released AS (
    UPDATE posts AS post
    SET release_state = 1,
        released_at = COALESCE(post.released_at, clock_timestamp())
    FROM written_media
    WHERE post.id = written_media.post_id
      AND post.deleted_at IS NULL
      AND post.release_state IN (0, 1)
    RETURNING post.id
)
UPDATE media_jobs AS job
SET state = 2,
    claimed_at = NULL,
    claimed_by = NULL,
    lease_expires_at = NULL,
    last_error = NULL,
    updated_at = clock_timestamp()
FROM valid, released
WHERE job.id = valid.id
  AND released.id = valid.post_id
RETURNING job.id
"#;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PublishOutcome {
    Published,
    LeaseLostOrConflict,
}

#[derive(Debug)]
pub enum PublicationError {
    Invalid(String),
    Database(PgError),
}

impl fmt::Display for PublicationError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Invalid(message) => f.write_str(message),
            Self::Database(error) => write!(f, "publish processed media: {error}"),
        }
    }
}

impl std::error::Error for PublicationError {}

impl From<PgError> for PublicationError {
    fn from(error: PgError) -> Self {
        Self::Database(error)
    }
}

pub fn publish_processed(
    client: &mut Client,
    lease: &JobLease,
    worker_id: &str,
    source_sha256: &[u8; 32],
    media: &ProcessedMedia,
) -> Result<PublishOutcome, PublicationError> {
    validate_publication(lease, worker_id, source_sha256, media)?;
    let kind = media.kind.as_i16();
    let source_sha256 = source_sha256.as_slice();
    let output_sha256 = media.sha256.as_slice();
    let row = client.query_opt(
        PUBLISH_SQL,
        &[
            &lease.id,
            &lease.post_id,
            &worker_id,
            &lease.lease_generation,
            &source_sha256,
            &kind,
            &media.storage_key,
            &media.mime_type,
            &media.width,
            &media.height,
            &media.duration_ms,
            &media.byte_size,
            &output_sha256,
            &media.perceptual_hash,
        ],
    )?;
    Ok(if row.is_some() {
        PublishOutcome::Published
    } else {
        PublishOutcome::LeaseLostOrConflict
    })
}

fn validate_publication(
    lease: &JobLease,
    worker_id: &str,
    source_sha256: &[u8; 32],
    media: &ProcessedMedia,
) -> Result<(), PublicationError> {
    if lease.id <= 0 || lease.post_id <= 0 || lease.kind != 0 {
        return Err(PublicationError::Invalid(
            "publication requires a positive kind-0 media job lease".to_owned(),
        ));
    }
    if worker_id.trim().is_empty() {
        return Err(PublicationError::Invalid(
            "publication requires worker identity".to_owned(),
        ));
    }
    if media.storage_key.is_empty() || media.storage_key.len() > 512 {
        return Err(PublicationError::Invalid(
            "processed media storage key length is invalid".to_owned(),
        ));
    }
    validate_processed_storage_key(&media.storage_key, lease.post_id)
        .map_err(|error| PublicationError::Invalid(error.to_string()))?;
    let expected_digest = digest_hex(source_sha256);
    if !media.storage_key.contains(&format!("-{expected_digest}.")) {
        return Err(PublicationError::Invalid(
            "processed media key does not embed verified source digest".to_owned(),
        ));
    }
    if media.mime_type.is_empty() || media.mime_type.len() > 255 {
        return Err(PublicationError::Invalid(
            "processed media MIME type length is invalid".to_owned(),
        ));
    }
    if media.width <= 0 || media.height <= 0 {
        return Err(PublicationError::Invalid(
            "processed media dimensions must be positive".to_owned(),
        ));
    }
    if media.duration_ms < 0 || media.byte_size <= 0 {
        return Err(PublicationError::Invalid(
            "duration must be non-negative and byte size must be positive".to_owned(),
        ));
    }
    if matches!(media.kind, MediaKind::Image) && media.duration_ms != 0 {
        return Err(PublicationError::Invalid(
            "image duration must be zero".to_owned(),
        ));
    }
    Ok(())
}

fn digest_hex(digest: &[u8; 32]) -> String {
    let mut encoded = String::with_capacity(64);
    for byte in digest {
        write!(&mut encoded, "{byte:02x}").expect("writing to String cannot fail");
    }
    encoded
}
