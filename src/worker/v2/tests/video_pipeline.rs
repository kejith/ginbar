use ginbar_worker_v2::jobs::{ClaimOutcome, JobStore};
use ginbar_worker_v2::media_executor::MediaJobExecutor;
use ginbar_worker_v2::processing::{
    processed_output_key, Cancellation, FailureClass, OutputFormat, ProcessError,
};
use ginbar_worker_v2::runner::{ConnectionFactory, RunnerSettings, ShutdownToken, WorkerRunner};
use ginbar_worker_v2::video::{
    video_thumbnail_output_key, VideoMetadata, VideoProbe, VideoThumbnailer,
};
use image::{ExtendedColorType, ImageEncoder};
use postgres::{Client, NoTls};
use sha2::{Digest, Sha256};
use std::env;
use std::fs;
use std::num::NonZeroU64;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

const CORE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/001_core.sql");
const LEASE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/002_media_job_leases.sql");
const SOURCE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/003_media_sources.sql");
const SOURCE_KEY: &str = "sources/01/0123456789abcdef0123456789abcdef";

#[derive(Clone)]
struct TestFactory {
    url: String,
    schema: String,
}

impl ConnectionFactory for TestFactory {
    fn connect(&self) -> Result<Client, String> {
        let mut client = Client::connect(&self.url, NoTls)
            .map_err(|error| format!("connect test database: {error}"))?;
        client
            .batch_execute(&format!("SET search_path TO {};", self.schema))
            .map_err(|error| format!("set test search_path: {error}"))?;
        Ok(client)
    }
}

struct TestDb {
    factory: TestFactory,
    owner: Client,
}

impl TestDb {
    fn new() -> Option<Self> {
        let url = env::var("GINBAR_TEST_DATABASE_URL").ok()?;
        let schema = format!("ginbar_m3_video_{}_{}", std::process::id(), unique_suffix());
        let mut owner = Client::connect(&url, NoTls).expect("connect test database");
        owner
            .batch_execute(&format!(
                "CREATE SCHEMA {schema}; SET search_path TO {schema};"
            ))
            .expect("create schema");
        owner.batch_execute(CORE_MIGRATION).expect("core migration");
        owner
            .batch_execute(LEASE_MIGRATION)
            .expect("lease migration");
        owner
            .batch_execute(SOURCE_MIGRATION)
            .expect("source migration");
        Some(Self {
            factory: TestFactory { url, schema },
            owner,
        })
    }

    fn connect(&self) -> Client {
        self.factory
            .connect()
            .expect("connect isolated test client")
    }

    fn insert_video_job(&mut self, body: &[u8]) -> (i64, i64, [u8; 32]) {
        let user_id: i64 = self
            .owner
            .query_one(
                "INSERT INTO users (username) VALUES ($1) RETURNING id",
                &[&format!("video{}", unique_suffix())],
            )
            .unwrap()
            .get(0);
        let post_id: i64 = self
            .owner
            .query_one(
                "INSERT INTO posts (author_user_id) VALUES ($1) RETURNING id",
                &[&user_id],
            )
            .unwrap()
            .get(0);
        let source_sha: [u8; 32] = Sha256::digest(body).into();
        self.owner
            .execute(
                r#"INSERT INTO media_sources
                   (post_id, source_type, storage_key, declared_mime_type, byte_size, sha256)
                   VALUES ($1, 0, $2, 'video/mp4', $3, $4)"#,
                &[
                    &post_id,
                    &SOURCE_KEY,
                    &(body.len() as i64),
                    &source_sha.as_slice(),
                ],
            )
            .unwrap();
        let job_id: i64 = self
            .owner
            .query_one(
                "INSERT INTO media_jobs (post_id, kind) VALUES ($1, 0) RETURNING id",
                &[&post_id],
            )
            .unwrap()
            .get(0);
        (post_id, job_id, source_sha)
    }
}

impl Drop for TestDb {
    fn drop(&mut self) {
        let _ = self.owner.batch_execute("SET search_path TO public;");
        let _ = self
            .owner
            .batch_execute(&format!("DROP SCHEMA {} CASCADE;", self.factory.schema));
    }
}

struct TempRoot(PathBuf);

impl TempRoot {
    fn with_source(body: &[u8]) -> Self {
        let path = env::temp_dir().join(format!(
            "ginbar-m3-video-integration-{}-{}",
            std::process::id(),
            unique_suffix()
        ));
        let source = path.join(SOURCE_KEY);
        fs::create_dir_all(source.parent().unwrap()).unwrap();
        fs::write(source, body).unwrap();
        Self(path)
    }
}

impl Drop for TempRoot {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.0);
    }
}

fn unique_suffix() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos()
}

fn mp4_bytes() -> Vec<u8> {
    let mut body = vec![0, 0, 0, 24];
    body.extend_from_slice(b"ftypisom");
    body.extend_from_slice(&[0, 0, 0, 0]);
    body.extend_from_slice(b"mp42");
    body.extend_from_slice(b"ginbar-compatible-video-test-payload");
    body
}

fn png_frame() -> Vec<u8> {
    let width = 64u32;
    let height = 48u32;
    let mut pixels = vec![0u8; width as usize * height as usize * 4];
    for (index, pixel) in pixels.as_chunks_mut::<4>().0.iter_mut().enumerate() {
        let value = (index % 251) as u8;
        pixel.copy_from_slice(&[value, value.wrapping_mul(3), value.wrapping_mul(7), 255]);
    }
    let mut encoded = Vec::new();
    image::codecs::png::PngEncoder::new(&mut encoded)
        .write_image(&pixels, width, height, ExtendedColorType::Rgba8)
        .unwrap();
    encoded
}

fn compatible_metadata() -> VideoMetadata {
    VideoMetadata {
        output_format: OutputFormat::Mp4,
        mime_type: "video/mp4",
        width: 640,
        height: 360,
        duration_ms: 1_250,
        rotation_degrees: 0,
        video_codec: "h264".to_owned(),
        pixel_format: "yuv420p".to_owned(),
        audio_codecs: vec!["aac".to_owned()],
    }
}

struct StaticProbe(VideoMetadata);

impl VideoProbe for StaticProbe {
    fn probe(
        &self,
        _source: &ginbar_worker_v2::processing::VerifiedSource,
        _cancellation: &dyn Cancellation,
    ) -> Result<VideoMetadata, ProcessError> {
        Ok(self.0.clone())
    }
}

struct FailingProbe;

impl VideoProbe for FailingProbe {
    fn probe(
        &self,
        _source: &ginbar_worker_v2::processing::VerifiedSource,
        _cancellation: &dyn Cancellation,
    ) -> Result<VideoMetadata, ProcessError> {
        Err(ProcessError::terminal("unsupported test video"))
    }
}

struct StaticThumbnailer(Vec<u8>);

impl VideoThumbnailer for StaticThumbnailer {
    fn extract_png(
        &self,
        _source_path: &Path,
        _cancellation: &dyn Cancellation,
    ) -> Result<Vec<u8>, ProcessError> {
        Ok(self.0.clone())
    }
}

struct BlockingThumbnailer {
    entered: Arc<AtomicBool>,
    observed_cancel: Arc<AtomicBool>,
}

impl VideoThumbnailer for BlockingThumbnailer {
    fn extract_png(
        &self,
        _source_path: &Path,
        cancellation: &dyn Cancellation,
    ) -> Result<Vec<u8>, ProcessError> {
        self.entered.store(true, Ordering::Release);
        let deadline = Instant::now() + Duration::from_secs(5);
        while Instant::now() < deadline {
            if cancellation.is_cancelled() {
                self.observed_cancel.store(true, Ordering::Release);
                return Err(ProcessError::retryable(
                    "thumbnail cancelled after ownership loss",
                ));
            }
            thread::sleep(Duration::from_millis(5));
        }
        Err(ProcessError::retryable(
            "test thumbnailer did not observe cancellation",
        ))
    }
}

fn test_settings() -> RunnerSettings {
    RunnerSettings {
        idle_poll: Duration::from_millis(10),
        db_retry_base: Duration::from_millis(10),
        db_retry_max: Duration::from_millis(50),
        renew_retry_base: Duration::from_millis(10),
        renew_retry_max: Duration::from_millis(50),
        renew_failure_limit: 3,
    }
}

fn wait_for_job_state(client: &mut Client, job_id: i64, expected: i16) {
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        let state: i16 = client
            .query_one("SELECT state FROM media_jobs WHERE id=$1", &[&job_id])
            .unwrap()
            .get(0);
        if state == expected {
            return;
        }
        assert!(
            Instant::now() < deadline,
            "job did not reach state {expected}"
        );
        thread::sleep(Duration::from_millis(5));
    }
}

fn wait_for_flag(flag: &AtomicBool, message: &str) {
    let deadline = Instant::now() + Duration::from_secs(5);
    while !flag.load(Ordering::Acquire) {
        assert!(Instant::now() < deadline, "{message}");
        thread::sleep(Duration::from_millis(5));
    }
}

#[test]
fn video_runner_publishes_compatible_mp4_and_releases_post() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let body = mp4_bytes();
    let (post_id, job_id, source_sha) = db.insert_video_job(&body);
    let temp = TempRoot::with_source(&body);
    let media_root = ginbar_worker_v2::processing::MediaRoot::new(&temp.0).unwrap();
    let executor = MediaJobExecutor::with_video_components(
        media_root,
        1024 * 1024,
        Box::new(StaticProbe(compatible_metadata())),
        Box::new(StaticThumbnailer(png_frame())),
    )
    .unwrap();
    let shutdown = ShutdownToken::new();
    let runner_shutdown = shutdown.clone();
    let factory = db.factory.clone();
    let handle = thread::spawn(move || {
        let mut runner = WorkerRunner::new(
            Arc::new(factory),
            "video-success".to_owned(),
            NonZeroU64::new(1_000).unwrap(),
            test_settings(),
            runner_shutdown,
            executor,
        )
        .unwrap();
        runner.run().unwrap();
    });

    wait_for_job_state(&mut db.owner, job_id, 2);
    shutdown.request();
    handle.join().unwrap();

    let row = db
        .owner
        .query_one(
            r#"SELECT p.release_state, m.kind, m.mime_type, m.width, m.height,
                      m.duration_ms, m.byte_size, m.sha256
               FROM posts p JOIN media m ON m.post_id=p.id WHERE p.id=$1"#,
            &[&post_id],
        )
        .unwrap();
    assert_eq!(row.get::<_, i16>(0), 1);
    assert_eq!(row.get::<_, i16>(1), 1);
    assert_eq!(row.get::<_, String>(2), "video/mp4");
    assert_eq!(row.get::<_, i32>(3), 640);
    assert_eq!(row.get::<_, i32>(4), 360);
    assert_eq!(row.get::<_, i64>(5), 1_250);
    assert_eq!(row.get::<_, i64>(6), body.len() as i64);
    assert_eq!(row.get::<_, Vec<u8>>(7), source_sha.to_vec());

    let main_key = processed_output_key(post_id, &source_sha, OutputFormat::Mp4).unwrap();
    assert_eq!(fs::read(temp.0.join(&main_key)).unwrap(), body);
    let thumb = fs::read(temp.0.join(video_thumbnail_output_key(&main_key).unwrap())).unwrap();
    assert_eq!(&thumb[4..8], b"ftyp");
    assert_eq!(&thumb[8..12], b"avif");
}

#[test]
fn video_runner_classifies_terminal_probe_failure_without_publication() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let body = mp4_bytes();
    let (post_id, job_id, _) = db.insert_video_job(&body);
    let temp = TempRoot::with_source(&body);
    let media_root = ginbar_worker_v2::processing::MediaRoot::new(&temp.0).unwrap();
    let executor = MediaJobExecutor::with_video_components(
        media_root,
        1024 * 1024,
        Box::new(FailingProbe),
        Box::new(StaticThumbnailer(png_frame())),
    )
    .unwrap();
    let shutdown = ShutdownToken::new();
    let runner_shutdown = shutdown.clone();
    let factory = db.factory.clone();
    let handle = thread::spawn(move || {
        let mut runner = WorkerRunner::new(
            Arc::new(factory),
            "video-terminal".to_owned(),
            NonZeroU64::new(1_000).unwrap(),
            test_settings(),
            runner_shutdown,
            executor,
        )
        .unwrap();
        runner.run().unwrap();
    });

    wait_for_job_state(&mut db.owner, job_id, 3);
    shutdown.request();
    handle.join().unwrap();

    let row = db
        .owner
        .query_one(
            r#"SELECT p.release_state,
                      (SELECT count(*) FROM media WHERE post_id=p.id),
                      j.last_error
               FROM posts p JOIN media_jobs j ON j.id=$2 WHERE p.id=$1"#,
            &[&post_id, &job_id],
        )
        .unwrap();
    assert_eq!(row.get::<_, i16>(0), 0);
    assert_eq!(row.get::<_, i64>(1), 0);
    assert_eq!(
        row.get::<_, Option<String>>(2).as_deref(),
        Some("unsupported test video")
    );
}

#[test]
fn video_lease_loss_after_canonical_file_never_publishes_database_state() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let body = mp4_bytes();
    let (post_id, job_id, source_sha) = db.insert_video_job(&body);
    let temp = TempRoot::with_source(&body);
    let media_root = ginbar_worker_v2::processing::MediaRoot::new(&temp.0).unwrap();
    let entered = Arc::new(AtomicBool::new(false));
    let observed_cancel = Arc::new(AtomicBool::new(false));
    let executor = MediaJobExecutor::with_video_components(
        media_root,
        1024 * 1024,
        Box::new(StaticProbe(compatible_metadata())),
        Box::new(BlockingThumbnailer {
            entered: Arc::clone(&entered),
            observed_cancel: Arc::clone(&observed_cancel),
        }),
    )
    .unwrap();
    let shutdown = ShutdownToken::new();
    let runner_shutdown = shutdown.clone();
    let factory = db.factory.clone();
    let handle = thread::spawn(move || {
        let mut runner = WorkerRunner::new(
            Arc::new(factory),
            "video-stale".to_owned(),
            NonZeroU64::new(150).unwrap(),
            test_settings(),
            runner_shutdown,
            executor,
        )
        .unwrap();
        runner.run().unwrap();
    });

    wait_for_flag(&entered, "video thumbnailer was not entered");
    let main_key = processed_output_key(post_id, &source_sha, OutputFormat::Mp4).unwrap();
    assert_eq!(fs::read(temp.0.join(&main_key)).unwrap(), body);

    let mut other = db.connect();
    let deadline = Instant::now() + Duration::from_secs(5);
    let reclaimed = loop {
        db.owner
            .execute(
                "UPDATE media_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1",
                &[&job_id],
            )
            .unwrap();
        match JobStore::new(&mut other)
            .claim_one("video-other", NonZeroU64::new(1_000).unwrap())
            .unwrap()
        {
            ClaimOutcome::Claimed(lease) if lease.id == job_id => break lease,
            ClaimOutcome::None => {}
            outcome => panic!("unexpected reclaim outcome: {outcome:?}"),
        }
        assert!(Instant::now() < deadline, "could not reclaim video job");
        thread::sleep(Duration::from_millis(5));
    };

    wait_for_flag(
        &observed_cancel,
        "video subprocess boundary did not observe ownership cancellation",
    );
    shutdown.request();
    handle.join().unwrap();

    let row = db
        .owner
        .query_one(
            r#"SELECT p.release_state,
                      (SELECT count(*) FROM media WHERE post_id=p.id),
                      j.state, j.claimed_by, j.lease_generation
               FROM posts p JOIN media_jobs j ON j.id=$2 WHERE p.id=$1"#,
            &[&post_id, &job_id],
        )
        .unwrap();
    assert_eq!(row.get::<_, i16>(0), 0);
    assert_eq!(row.get::<_, i64>(1), 0);
    assert_eq!(row.get::<_, i16>(2), 1);
    assert_eq!(
        row.get::<_, Option<String>>(3).as_deref(),
        Some("video-other")
    );
    assert_eq!(row.get::<_, i64>(4), reclaimed.lease_generation);
    assert!(!temp
        .0
        .join(video_thumbnail_output_key(&main_key).unwrap())
        .exists());
}

#[test]
fn failure_classes_used_by_video_runner_are_stable() {
    assert_eq!(
        ProcessError::terminal("terminal").class(),
        FailureClass::Terminal
    );
    assert_eq!(
        ProcessError::retryable("retryable").class(),
        FailureClass::Retryable
    );
}
