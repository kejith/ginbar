use crate::jobs::JobLease;
use crate::source::{MediaKind, ProcessingError, SourceRecord, SourceType, VerifiedSource};
use postgres::{Client, Error};

const JOB_KIND_PROCESS_SOURCE: i16 = 0;
const MEDIA_STATE_READY: i16 = 1;
pub const PROCESSING_RECIPE_VERSION: u16 = 1;

const LOAD_SOURCE_SQL: &str = r#"
SELECT
    job.kind,
    source.source_type,
    source.storage_key,
    source.source_url,
    source.original_name,
    source.declared_mime_type,
    source.byte_size,
    source.sha256
FROM media_jobs AS job
LEFT JOIN media_sources AS source ON source.post_id = job.post_id
WHERE job.id = $1
  AND job.post_id = $2
  AND job.state = 1
  AND job.claimed_by = $3
  AND job.lease_generation = $4
  AND job.lease_expires_at > clock_timestamp()
"#;

const LOCK_PUBLICATION_INPUTS_SQL: &str = r#"
WITH owned AS MATERIALIZED (
    SELECT
        job.kind,
        job.lease_expires_at,
        post.deleted_at IS NULL AS post_available
    FROM media_jobs AS job
    JOIN posts AS post ON post.id = job.post_id
    WHERE job.id = $1
      AND job.post_id = $2
      AND job.state = 1
      AND job.claimed_by = $3
      AND job.lease_generation = $4
    FOR UPDATE OF job, post
), source AS MATERIALIZED (
    SELECT media_sources.sha256
    FROM media_sources
    JOIN owned ON true
    WHERE media_sources.post_id = $2
    FOR SHARE OF media_sources
)
SELECT
    owned.kind,
    owned.post_available,
    owned.lease_expires_at > clock_timestamp() AS lease_valid,
    source.sha256
FROM owned
LEFT JOIN source ON true
"#;

const PUBLISH_SQL: &str = r#"
WITH media_write AS (
    INSERT INTO media (
        post_id,
        kind,
        processing_state,
        storage_key,
        mime_type,
        width,
        height,
        duration_ms,
        byte_size,
        sha256,
        perceptual_hash
    )
    VALUES ($2, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
    ON CONFLICT (post_id) DO UPDATE SET
        kind = EXCLUDED.kind,
        processing_state = EXCLUDED.processing_state,
        storage_key = EXCLUDED.storage_key,
        mime_type = EXCLUDED.mime_type,
        width = EXCLUDED.width,
        height = EXCLUDED.height,
        duration_ms = EXCLUDED.duration_ms,
        byte_size = EXCLUDED.byte_size,
        sha256 = EXCLUDED.sha256,
        perceptual_hash = EXCLUDED.perceptual_hash,
        updated_at = clock_timestamp()
    WHERE media.kind = EXCLUDED.kind
      AND media.processing_state = EXCLUDED.processing_state
      AND media.storage_key = EXCLUDED.storage_key
      AND media.mime_type = EXCLUDED.mime_type
      AND media.width = EXCLUDED.width
      AND media.height = EXCLUDED.height
      AND media.duration_ms = EXCLUDED.duration_ms
      AND media.byte_size = EXCLUDED.byte_size
      AND media.sha256 = EXCLUDED.sha256
      AND media.perceptual_hash IS NOT DISTINCT FROM EXCLUDED.perceptual_hash
    RETURNING post_id
), released AS (
    UPDATE posts AS post
    SET
        release_state = 1,
        released_at = COALESCE(post.released_at, clock_timestamp())
    FROM media_write
    WHERE post.id = media_write.post_id
      AND post.deleted_at IS NULL
    RETURNING post.id
), completed AS (
    UPDATE media_jobs AS job
    SET
        state = 2,
        claimed_at = NULL,
        claimed_by = NULL,
        lease_expires_at = NULL,
        last_error = NULL,
        updated_at = clock_timestamp()
    FROM released
    WHERE job.id = $1
      AND job.post_id = $2
      AND job.state = 1
      AND job.claimed_by = $3
      AND job.lease_generation = $4
      AND job.lease_expires_at > clock_timestamp()
      AND released.id = job.post_id
    RETURNING job.id
)
SELECT
    EXISTS (SELECT 1 FROM media_write) AS media_written,
    EXISTS (SELECT 1 FROM released) AS post_released,
    EXISTS (SELECT 1 FROM completed) AS job_completed
"#;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SourceLoadOutcome {
    Loaded(SourceRecord),
    LeaseLost,
    MissingSource,
    UnsupportedJobKind { kind: i16 },
    InvalidSourceMetadata,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum PublicationOutcome {
    Published,
    LeaseLost,
    SourceUnavailable,
    SourceChanged,
    PostUnavailable,
    OutputConflict,
    InvalidOutput,
    UnsupportedJobKind { kind: i16 },
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OutputPlan {
    pub post_id: i64,
    pub source_sha256: [u8; 32],
    pub media_kind: MediaKind,
    pub storage_key: String,
    pub recipe_version: u16,
}

impl OutputPlan {
    pub fn for_source(source: &VerifiedSource) -> Self {
        let media_kind = source.media_type().kind();
        Self {
            post_id: source.source().post_id,
            source_sha256: source.source().sha256,
            media_kind,
            storage_key: output_storage_key(
                source.source().post_id,
                &source.source().sha256,
                media_kind,
            ),
            recipe_version: PROCESSING_RECIPE_VERSION,
        }
    }

    fn is_valid(&self) -> bool {
        self.post_id > 0
            && self.recipe_version == PROCESSING_RECIPE_VERSION
            && self.storage_key
                == output_storage_key(self.post_id, &self.source_sha256, self.media_kind)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ProcessedMedia {
    pub kind: MediaKind,
    pub storage_key: String,
    pub mime_type: String,
    pub width: i32,
    pub height: i32,
    pub duration_ms: i64,
    pub byte_size: i64,
    pub sha256: [u8; 32],
    pub perceptual_hash: Option<i64>,
}

impl ProcessedMedia {
    pub fn validate_for(&self, plan: &OutputPlan) -> Result<(), ProcessingError> {
        if self.kind != plan.media_kind {
            return Err(ProcessingError::terminal(
                "processor returned a media kind that does not match its output plan",
            ));
        }
        if self.storage_key != plan.storage_key {
            return Err(ProcessingError::terminal(
                "processor returned a storage key that does not match its output plan",
            ));
        }
        if self.mime_type.is_empty() || self.width <= 0 || self.height <= 0 || self.byte_size <= 0 {
            return Err(ProcessingError::terminal(
                "processor returned incomplete media metadata",
            ));
        }
        if self.duration_ms < 0 || (self.kind == MediaKind::Image && self.duration_ms != 0) {
            return Err(ProcessingError::terminal(
                "processor returned an invalid media duration",
            ));
        }
        Ok(())
    }
}

pub trait ImageProcessor {
    fn process_image(
        &mut self,
        source: &mut VerifiedSource,
        plan: &OutputPlan,
    ) -> Result<ProcessedMedia, ProcessingError>;
}

pub trait VideoProcessor {
    fn process_video(
        &mut self,
        source: &mut VerifiedSource,
        plan: &OutputPlan,
    ) -> Result<ProcessedMedia, ProcessingError>;
}

pub fn dispatch_verified_source<I, V>(
    source: &mut VerifiedSource,
    image: &mut I,
    video: &mut V,
) -> Result<(OutputPlan, ProcessedMedia), ProcessingError>
where
    I: ImageProcessor,
    V: VideoProcessor,
{
    let plan = OutputPlan::for_source(source);
    let media = match plan.media_kind {
        MediaKind::Image => image.process_image(source, &plan)?,
        MediaKind::Video => video.process_video(source, &plan)?,
    };
    media.validate_for(&plan)?;
    Ok((plan, media))
}

pub struct ProcessingStore<'a> {
    client: &'a mut Client,
}

impl<'a> ProcessingStore<'a> {
    pub fn new(client: &'a mut Client) -> Self {
        Self { client }
    }

    pub fn load_source(
        &mut self,
        lease: &JobLease,
        worker_id: &str,
    ) -> Result<SourceLoadOutcome, Error> {
        let row = self.client.query_opt(
            LOAD_SOURCE_SQL,
            &[
                &lease.id,
                &lease.post_id,
                &worker_id,
                &lease.lease_generation,
            ],
        )?;
        let Some(row) = row else {
            return Ok(SourceLoadOutcome::LeaseLost);
        };
        let kind: i16 = row.get("kind");
        if kind != JOB_KIND_PROCESS_SOURCE {
            return Ok(SourceLoadOutcome::UnsupportedJobKind { kind });
        }

        let Some(storage_key) = row.get::<_, Option<String>>("storage_key") else {
            return Ok(SourceLoadOutcome::MissingSource);
        };
        let Some(source_type) = row.get::<_, Option<i16>>("source_type") else {
            return Ok(SourceLoadOutcome::InvalidSourceMetadata);
        };
        let Some(byte_size) = row.get::<_, Option<i64>>("byte_size") else {
            return Ok(SourceLoadOutcome::InvalidSourceMetadata);
        };
        let Some(sha256) = row.get::<_, Option<Vec<u8>>>("sha256") else {
            return Ok(SourceLoadOutcome::InvalidSourceMetadata);
        };
        let Some(source_type) = SourceType::from_db(source_type) else {
            return Ok(SourceLoadOutcome::InvalidSourceMetadata);
        };
        let Ok(byte_size) = u64::try_from(byte_size) else {
            return Ok(SourceLoadOutcome::InvalidSourceMetadata);
        };
        let Ok(sha256) = <[u8; 32]>::try_from(sha256) else {
            return Ok(SourceLoadOutcome::InvalidSourceMetadata);
        };
        let source_url: Option<String> = row.get("source_url");
        if storage_key.is_empty()
            || byte_size == 0
            || (source_type == SourceType::Upload && source_url.is_some())
            || (source_type == SourceType::Url && source_url.is_none())
        {
            return Ok(SourceLoadOutcome::InvalidSourceMetadata);
        }

        Ok(SourceLoadOutcome::Loaded(SourceRecord {
            post_id: lease.post_id,
            source_type,
            storage_key,
            source_url,
            original_name: row.get("original_name"),
            declared_mime_type: row
                .get::<_, Option<String>>("declared_mime_type")
                .unwrap_or_default(),
            byte_size,
            sha256,
        }))
    }

    pub fn publish_processed(
        &mut self,
        lease: &JobLease,
        worker_id: &str,
        plan: &OutputPlan,
        media: &ProcessedMedia,
    ) -> Result<PublicationOutcome, Error> {
        if plan.post_id != lease.post_id || !plan.is_valid() || media.validate_for(plan).is_err() {
            return Ok(PublicationOutcome::InvalidOutput);
        }

        let mut transaction = self.client.transaction()?;
        let inputs = transaction.query_opt(
            LOCK_PUBLICATION_INPUTS_SQL,
            &[
                &lease.id,
                &lease.post_id,
                &worker_id,
                &lease.lease_generation,
            ],
        )?;
        let Some(inputs) = inputs else {
            transaction.rollback()?;
            return Ok(PublicationOutcome::LeaseLost);
        };
        let kind: i16 = inputs.get("kind");
        if kind != JOB_KIND_PROCESS_SOURCE {
            transaction.rollback()?;
            return Ok(PublicationOutcome::UnsupportedJobKind { kind });
        }
        if !inputs.get::<_, bool>("post_available") {
            transaction.rollback()?;
            return Ok(PublicationOutcome::PostUnavailable);
        }
        if !inputs.get::<_, bool>("lease_valid") {
            transaction.rollback()?;
            return Ok(PublicationOutcome::LeaseLost);
        }
        let Some(source_sha256) = inputs.get::<_, Option<Vec<u8>>>("sha256") else {
            transaction.rollback()?;
            return Ok(PublicationOutcome::SourceUnavailable);
        };
        if source_sha256.as_slice() != &plan.source_sha256[..] {
            transaction.rollback()?;
            return Ok(PublicationOutcome::SourceChanged);
        }

        let kind = media.kind as i16;
        let digest: &[u8] = &media.sha256;
        let result = transaction.query_one(
            PUBLISH_SQL,
            &[
                &lease.id,
                &lease.post_id,
                &worker_id,
                &lease.lease_generation,
                &kind,
                &MEDIA_STATE_READY,
                &media.storage_key,
                &media.mime_type,
                &media.width,
                &media.height,
                &media.duration_ms,
                &media.byte_size,
                &digest,
                &media.perceptual_hash,
            ],
        )?;
        let media_written: bool = result.get("media_written");
        let post_released: bool = result.get("post_released");
        let job_completed: bool = result.get("job_completed");
        if !media_written {
            transaction.rollback()?;
            return Ok(PublicationOutcome::OutputConflict);
        }
        if !post_released {
            transaction.rollback()?;
            return Ok(PublicationOutcome::PostUnavailable);
        }
        if !job_completed {
            transaction.rollback()?;
            return Ok(PublicationOutcome::LeaseLost);
        }

        transaction.commit()?;
        Ok(PublicationOutcome::Published)
    }
}

fn output_storage_key(post_id: i64, source_sha256: &[u8; 32], media_kind: MediaKind) -> String {
    let digest = encode_lower_hex(source_sha256);
    let class = match media_kind {
        MediaKind::Image => "image",
        MediaKind::Video => "video",
    };
    format!(
        "media/v{PROCESSING_RECIPE_VERSION}/{class}/{}/{}-{digest}",
        &digest[..2], post_id
    )
}

fn encode_lower_hex(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut encoded = String::with_capacity(bytes.len() * 2);
    for byte in bytes {
        encoded.push(HEX[(byte >> 4) as usize] as char);
        encoded.push(HEX[(byte & 0x0f) as usize] as char);
    }
    encoded
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::source::{SniffedMediaType, SourceVerifier};
    use sha2::{Digest, Sha256};
    use std::fs::{self, File};
    use std::io::Write;
    use std::path::PathBuf;
    use std::time::{SystemTime, UNIX_EPOCH};

    #[test]
    fn output_plan_is_deterministic_and_class_specific() {
        let fixture = Fixture::new(b"\xff\xd8\xffprocessor-source");
        let verifier = SourceVerifier::new(&fixture.root, 1024).expect("create verifier");
        let verified = verifier
            .verify(fixture.record(), || false)
            .expect("verify source");
        assert_eq!(verified.media_type(), SniffedMediaType::Jpeg);
        let first = OutputPlan::for_source(&verified);
        let second = OutputPlan::for_source(&verified);
        assert_eq!(first, second);
        assert_eq!(first.media_kind, MediaKind::Image);
        assert!(first.storage_key.starts_with("media/v1/image/"));
        assert!(first.storage_key.contains("/1-"));
        assert!(first.is_valid());
    }

    #[test]
    fn dispatch_routes_image_processor_and_validates_output() {
        let fixture = Fixture::new(b"\xff\xd8\xffprocessor-source");
        let verifier = SourceVerifier::new(&fixture.root, 1024).expect("create verifier");
        let mut verified = verifier
            .verify(fixture.record(), || false)
            .expect("verify source");
        let mut image = FakeImageProcessor { calls: 0 };
        let mut video = PanicVideoProcessor;
        let (plan, media) = dispatch_verified_source(&mut verified, &mut image, &mut video)
            .expect("dispatch image");
        assert_eq!(image.calls, 1);
        assert_eq!(plan.storage_key, media.storage_key);
        assert_eq!(media.kind, MediaKind::Image);
    }

    #[test]
    fn dispatch_routes_video_processor_and_validates_output() {
        let mut body = vec![0x1a, 0x45, 0xdf, 0xa3, 0x42, 0x82, 0x84];
        body.extend_from_slice(b"webm");
        body.extend_from_slice(b"processor-source");
        let fixture = Fixture::new(&body);
        let verifier = SourceVerifier::new(&fixture.root, 1024).expect("create verifier");
        let mut verified = verifier
            .verify(fixture.record(), || false)
            .expect("verify source");
        assert_eq!(verified.media_type(), SniffedMediaType::WebM);
        let mut image = PanicImageProcessor;
        let mut video = FakeVideoProcessor { calls: 0 };
        let (plan, media) = dispatch_verified_source(&mut verified, &mut image, &mut video)
            .expect("dispatch video");
        assert_eq!(video.calls, 1);
        assert_eq!(plan.storage_key, media.storage_key);
        assert_eq!(media.kind, MediaKind::Video);
        assert!(plan.storage_key.starts_with("media/v1/video/"));
    }

    struct FakeImageProcessor {
        calls: usize,
    }

    impl ImageProcessor for FakeImageProcessor {
        fn process_image(
            &mut self,
            _source: &mut VerifiedSource,
            plan: &OutputPlan,
        ) -> Result<ProcessedMedia, ProcessingError> {
            self.calls += 1;
            Ok(ProcessedMedia {
                kind: MediaKind::Image,
                storage_key: plan.storage_key.clone(),
                mime_type: "image/avif".to_owned(),
                width: 10,
                height: 20,
                duration_ms: 0,
                byte_size: 123,
                sha256: [7; 32],
                perceptual_hash: None,
            })
        }
    }

    struct PanicVideoProcessor;

    impl VideoProcessor for PanicVideoProcessor {
        fn process_video(
            &mut self,
            _source: &mut VerifiedSource,
            _plan: &OutputPlan,
        ) -> Result<ProcessedMedia, ProcessingError> {
            panic!("video processor must not be called for an image")
        }
    }

    struct PanicImageProcessor;

    impl ImageProcessor for PanicImageProcessor {
        fn process_image(
            &mut self,
            _source: &mut VerifiedSource,
            _plan: &OutputPlan,
        ) -> Result<ProcessedMedia, ProcessingError> {
            panic!("image processor must not be called for a video")
        }
    }

    struct FakeVideoProcessor {
        calls: usize,
    }

    impl VideoProcessor for FakeVideoProcessor {
        fn process_video(
            &mut self,
            _source: &mut VerifiedSource,
            plan: &OutputPlan,
        ) -> Result<ProcessedMedia, ProcessingError> {
            self.calls += 1;
            Ok(ProcessedMedia {
                kind: MediaKind::Video,
                storage_key: plan.storage_key.clone(),
                mime_type: "video/mp4".to_owned(),
                width: 1920,
                height: 1080,
                duration_ms: 1_000,
                byte_size: 456,
                sha256: [8; 32],
                perceptual_hash: None,
            })
        }
    }

    struct Fixture {
        root: PathBuf,
        body: Vec<u8>,
        sha256: [u8; 32],
    }

    impl Fixture {
        fn new(body: &[u8]) -> Self {
            let nonce = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .expect("clock before epoch")
                .as_nanos();
            let root = std::env::temp_dir().join(format!(
                "ginbar-worker-processing-{}-{nonce}",
                std::process::id()
            ));
            let path = root.join("sources/01/0123456789abcdef0123456789abcdef");
            fs::create_dir_all(path.parent().expect("fixture parent")).expect("create fixture dirs");
            let mut file = File::create(path).expect("create fixture source");
            file.write_all(body).expect("write fixture source");
            file.sync_all().expect("sync fixture source");
            Self {
                root,
                body: body.to_vec(),
                sha256: Sha256::digest(body).into(),
            }
        }

        fn record(&self) -> SourceRecord {
            SourceRecord {
                post_id: 1,
                source_type: SourceType::Upload,
                storage_key: "sources/01/0123456789abcdef0123456789abcdef".to_owned(),
                source_url: None,
                original_name: None,
                declared_mime_type: "ignored/type".to_owned(),
                byte_size: self.body.len() as u64,
                sha256: self.sha256,
            }
        }
    }

    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }

    #[test]
    fn processed_media_validation_rejects_plan_mismatch() {
        let plan = OutputPlan {
            post_id: 5,
            source_sha256: [1; 32],
            media_kind: MediaKind::Image,
            storage_key: output_storage_key(5, &[1; 32], MediaKind::Image),
            recipe_version: PROCESSING_RECIPE_VERSION,
        };
        let media = ProcessedMedia {
            kind: MediaKind::Video,
            storage_key: plan.storage_key.clone(),
            mime_type: "video/mp4".to_owned(),
            width: 1,
            height: 1,
            duration_ms: 1,
            byte_size: 1,
            sha256: [2; 32],
            perceptual_hash: None,
        };
        assert!(media.validate_for(&plan).is_err());
    }
}
