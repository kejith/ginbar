use ginbar_worker_v2::image::{thumbnail_output_key, BoundedImageProcessor};
use ginbar_worker_v2::jobs::{ClaimOutcome, JobLease, JobStore};
use ginbar_worker_v2::output::OutputStore;
use ginbar_worker_v2::processing::{
    prepare_claimed_source, processed_output_key, FailureClass, ImageProcessor, MediaRoot,
    NeverCancelled, OutputFormat,
};
use ginbar_worker_v2::publication::{publish_processed, PublishOutcome};
use image::{ExtendedColorType, ImageEncoder};
use postgres::{Client, NoTls};
use sha2::{Digest, Sha256};
use std::env;
use std::fs;
use std::num::NonZeroU64;
use std::path::PathBuf;
use std::time::{SystemTime, UNIX_EPOCH};

const CORE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/001_core.sql");
const LEASE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/002_media_job_leases.sql");
const SOURCE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/003_media_sources.sql");
const SOURCE_KEY: &str = "sources/01/0123456789abcdef0123456789abcdef";

struct TestDb {
    url: String,
    schema: String,
    owner: Client,
}

impl TestDb {
    fn new() -> Option<Self> {
        let url = env::var("GINBAR_TEST_DATABASE_URL").ok()?;
        let schema = format!("ginbar_m3_image_{}_{}", std::process::id(), unique_suffix());
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
        Some(Self { url, schema, owner })
    }

    fn connect(&self) -> Client {
        let mut client = Client::connect(&self.url, NoTls).expect("connect test database");
        client
            .batch_execute(&format!("SET search_path TO {};", self.schema))
            .expect("set search path");
        client
    }

    fn insert_image_job(&mut self, body: &[u8]) -> (i64, i64, [u8; 32]) {
        let user_id: i64 = self
            .owner
            .query_one(
                "INSERT INTO users (username) VALUES ($1) RETURNING id",
                &[&format!("image{}", unique_suffix())],
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
                   VALUES ($1, 0, $2, 'image/png', $3, $4)"#,
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
            .batch_execute(&format!("DROP SCHEMA {} CASCADE;", self.schema));
    }
}

struct TempRoot(PathBuf);

impl TempRoot {
    fn with_source(body: &[u8]) -> Self {
        let path = env::temp_dir().join(format!(
            "ginbar-m3-image-integration-{}-{}",
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

fn png_bytes() -> Vec<u8> {
    let width = 96u32;
    let height = 64u32;
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

fn claim(client: &mut Client, worker: &str) -> JobLease {
    match JobStore::new(client)
        .claim_one(worker, NonZeroU64::new(30_000).unwrap())
        .unwrap()
    {
        ClaimOutcome::Claimed(lease) => lease,
        other => panic!("unexpected claim outcome: {other:?}"),
    }
}

#[test]
fn crash_after_files_before_database_publication_retries_idempotently() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let body = png_bytes();
    let (post_id, job_id, source_sha) = db.insert_image_job(&body);
    let temp = TempRoot::with_source(&body);
    let media_root = MediaRoot::new(&temp.0).unwrap();

    let mut first_client = db.connect();
    let first_lease = claim(&mut first_client, "image-crash-1");
    let mut first_source = prepare_claimed_source(
        &mut first_client,
        &first_lease,
        &media_root,
        body.len() as u64,
        &NeverCancelled,
    )
    .unwrap();
    let mut first_processor = BoundedImageProcessor::new(OutputStore::new(&temp.0).unwrap());
    let first_output = first_processor
        .process_image(&mut first_source, post_id)
        .unwrap();
    let first_main_bytes = fs::read(temp.0.join(&first_output.storage_key)).unwrap();
    let first_thumb_bytes = fs::read(
        temp.0
            .join(thumbnail_output_key(&first_output.storage_key).unwrap()),
    )
    .unwrap();

    // Simulate a worker crash after both files are durable but before publish_processed.
    db.owner
        .execute(
            "UPDATE media_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1",
            &[&job_id],
        )
        .unwrap();
    drop(first_client);

    let mut retry_client = db.connect();
    let retry_lease = claim(&mut retry_client, "image-crash-2");
    assert!(retry_lease.lease_generation > first_lease.lease_generation);
    let mut retry_source = prepare_claimed_source(
        &mut retry_client,
        &retry_lease,
        &media_root,
        body.len() as u64,
        &NeverCancelled,
    )
    .unwrap();
    let mut retry_processor = BoundedImageProcessor::new(OutputStore::new(&temp.0).unwrap());
    let retry_output = retry_processor
        .process_image(&mut retry_source, post_id)
        .unwrap();
    assert_eq!(retry_output, first_output);
    assert_eq!(
        fs::read(temp.0.join(&retry_output.storage_key)).unwrap(),
        first_main_bytes
    );
    assert_eq!(
        fs::read(
            temp.0
                .join(thumbnail_output_key(&retry_output.storage_key).unwrap())
        )
        .unwrap(),
        first_thumb_bytes
    );

    assert_eq!(
        publish_processed(
            &mut retry_client,
            &retry_lease,
            "image-crash-2",
            &source_sha,
            &retry_output,
        )
        .unwrap(),
        PublishOutcome::Published
    );
    let row = retry_client
        .query_one(
            "SELECT p.release_state, m.storage_key, m.sha256, j.state FROM posts p JOIN media m ON m.post_id=p.id JOIN media_jobs j ON j.id=$2 WHERE p.id=$1",
            &[&post_id, &job_id],
        )
        .unwrap();
    assert_eq!(row.get::<_, i16>(0), 1);
    assert_eq!(row.get::<_, String>(1), retry_output.storage_key);
    assert_eq!(row.get::<_, Vec<u8>>(2), retry_output.sha256.to_vec());
    assert_eq!(row.get::<_, i16>(3), 2);
}

#[test]
fn collision_prevents_database_publication_and_preserves_existing_file() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let body = png_bytes();
    let (post_id, _, source_sha) = db.insert_image_job(&body);
    let temp = TempRoot::with_source(&body);
    let media_root = MediaRoot::new(&temp.0).unwrap();
    let mut client = db.connect();
    let lease = claim(&mut client, "image-collision");
    let mut source = prepare_claimed_source(
        &mut client,
        &lease,
        &media_root,
        body.len() as u64,
        &NeverCancelled,
    )
    .unwrap();

    let main_key = processed_output_key(post_id, &source_sha, OutputFormat::Avif).unwrap();
    let main_path = temp.0.join(&main_key);
    fs::create_dir_all(main_path.parent().unwrap()).unwrap();
    fs::write(&main_path, b"unrelated-existing-object").unwrap();

    let mut processor = BoundedImageProcessor::new(OutputStore::new(&temp.0).unwrap());
    let error = processor.process_image(&mut source, post_id).unwrap_err();
    assert_eq!(error.class(), FailureClass::Terminal);
    assert_eq!(fs::read(&main_path).unwrap(), b"unrelated-existing-object");

    let row = client
        .query_one(
            "SELECT release_state, (SELECT count(*) FROM media WHERE post_id=posts.id) FROM posts WHERE id=$1",
            &[&post_id],
        )
        .unwrap();
    assert_eq!(row.get::<_, i16>(0), 0);
    assert_eq!(row.get::<_, i64>(1), 0);
}
