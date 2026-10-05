use ginbar_worker_v2::jobs::{ClaimOutcome, FailureOutcome, JobStore};
use ginbar_worker_v2::output::{OutputPublishOutcome, OutputStore};
use ginbar_worker_v2::processing::{processed_output_key, MediaKind, OutputFormat, ProcessedMedia};
use ginbar_worker_v2::publication::{publish_processed, PublishOutcome};
use postgres::{Client, NoTls};
use sha2::{Digest, Sha256};
use std::env;
use std::fs;
use std::num::NonZeroU64;
use std::path::PathBuf;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

const CORE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/001_core.sql");
const LEASE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/002_media_job_leases.sql");
const SOURCE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/003_media_sources.sql");

struct TestDb {
    url: String,
    schema: String,
    owner: Client,
}

impl TestDb {
    fn new() -> Option<Self> {
        let url = env::var("GINBAR_TEST_DATABASE_URL").ok()?;
        let schema = format!(
            "ginbar_m3_regeneration_{}_{}",
            std::process::id(),
            unique_suffix()
        );
        let mut owner = Client::connect(&url, NoTls).expect("connect test database");
        owner
            .batch_execute(&format!(
                "CREATE SCHEMA {schema}; SET search_path TO {schema};"
            ))
            .expect("create schema");
        owner
            .batch_execute(CORE_MIGRATION)
            .expect("apply core migration");
        owner
            .batch_execute(LEASE_MIGRATION)
            .expect("apply lease migration");
        owner
            .batch_execute(SOURCE_MIGRATION)
            .expect("apply source migration");
        Some(Self { url, schema, owner })
    }

    fn connect(&self) -> Client {
        let mut client = Client::connect(&self.url, NoTls).expect("connect test database");
        client
            .batch_execute(&format!("SET search_path TO {};", self.schema))
            .expect("set search path");
        client
    }

    fn insert_source_job(&mut self, body: &[u8]) -> (i64, i64, [u8; 32]) {
        let user_id: i64 = self
            .owner
            .query_one(
                "INSERT INTO users (username) VALUES ($1) RETURNING id",
                &[&format!("r{}", unique_suffix())],
            )
            .expect("insert user")
            .get(0);
        let post_id: i64 = self
            .owner
            .query_one(
                "INSERT INTO posts (author_user_id) VALUES ($1) RETURNING id",
                &[&user_id],
            )
            .expect("insert post")
            .get(0);
        let source_sha: [u8; 32] = Sha256::digest(body).into();
        let source_key = source_key(post_id);
        self.owner
            .execute(
                r#"INSERT INTO media_sources
                   (post_id, source_type, storage_key, declared_mime_type, byte_size, sha256)
                   VALUES ($1, 0, $2, '', $3, $4)"#,
                &[
                    &post_id,
                    &source_key,
                    &(body.len() as i64),
                    &source_sha.as_slice(),
                ],
            )
            .expect("insert source");
        let job_id: i64 = self
            .owner
            .query_one(
                "INSERT INTO media_jobs (post_id, kind) VALUES ($1, 0) RETURNING id",
                &[&post_id],
            )
            .expect("insert job")
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

struct TempRoot {
    path: PathBuf,
}

impl TempRoot {
    fn new() -> Self {
        let path = env::temp_dir().join(format!(
            "ginbar-m3-regeneration-output-{}-{}",
            std::process::id(),
            unique_suffix()
        ));
        fs::create_dir_all(&path).expect("create temp output root");
        Self { path }
    }
}

impl Drop for TempRoot {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.path);
    }
}

fn source_key(post_id: i64) -> String {
    let id = format!("{:032x}", post_id);
    format!("sources/{}/{}", &id[..2], id)
}

fn unique_suffix() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock")
        .as_nanos()
}

fn processed(
    post_id: i64,
    source_sha: &[u8; 32],
    output_byte: u8,
    perceptual_hash: i64,
) -> ProcessedMedia {
    ProcessedMedia {
        kind: MediaKind::Image,
        storage_key: processed_output_key(post_id, source_sha, OutputFormat::Avif)
            .expect("output key"),
        mime_type: "image/avif".to_owned(),
        width: 640,
        height: 480,
        duration_ms: 0,
        byte_size: 456,
        sha256: [output_byte; 32],
        perceptual_hash: Some(perceptual_hash),
    }
}

fn claim(client: &mut Client, worker: &str) -> ginbar_worker_v2::jobs::JobLease {
    match JobStore::new(client)
        .claim_one(worker, NonZeroU64::new(30_000).unwrap())
        .expect("claim")
    {
        ClaimOutcome::Claimed(lease) => lease,
        other => panic!("unexpected claim outcome: {other:?}"),
    }
}

fn requeue(client: &mut Client, job_id: i64) {
    client
        .execute(
            r#"UPDATE media_jobs
               SET state=0, attempts=0, available_at=clock_timestamp(),
                   claimed_at=NULL, claimed_by=NULL, lease_expires_at=NULL,
                   last_error=NULL, updated_at=clock_timestamp()
               WHERE id=$1"#,
            &[&job_id],
        )
        .expect("requeue job");
}

#[test]
fn unchanged_regeneration_reuses_existing_deterministic_output() {
    let root = TempRoot::new();
    let store = OutputStore::new(&root.path).expect("output store");
    let key = processed_output_key(17, &[0x11; 32], OutputFormat::Avif).expect("output key");

    let first = store
        .publish_bytes(&key, b"deterministic output")
        .expect("first publish");
    let second = store
        .publish_bytes(&key, b"deterministic output")
        .expect("reuse publish");

    assert_eq!(first.outcome, OutputPublishOutcome::Published);
    assert_eq!(second.outcome, OutputPublishOutcome::Reused);
    assert_eq!(first.byte_size, second.byte_size);
    assert_eq!(first.sha256, second.sha256);
}

#[test]
fn regeneration_keeps_ready_media_authoritative_until_success() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let body = b"regeneration source";
    let (post_id, job_id, source_sha) = db.insert_source_job(body);
    let mut client = db.connect();
    let initial_lease = claim(&mut client, "regen-initial");
    let initial = processed(post_id, &source_sha, 0x21, 111);
    assert_eq!(
        publish_processed(
            &mut client,
            &initial_lease,
            "regen-initial",
            &source_sha,
            &initial,
        )
        .expect("initial publish"),
        PublishOutcome::Published
    );

    let released_at: String = client
        .query_one("SELECT released_at::text FROM posts WHERE id=$1", &[&post_id])
        .expect("released at")
        .get(0);
    requeue(&mut client, job_id);
    let regeneration_lease = claim(&mut client, "regen-same");

    let before = client
        .query_one(
            r#"SELECT p.release_state, m.processing_state, m.storage_key, m.sha256, m.perceptual_hash
               FROM posts p JOIN media m ON m.post_id=p.id WHERE p.id=$1"#,
            &[&post_id],
        )
        .expect("published state while running");
    assert_eq!(before.get::<_, i16>(0), 1);
    assert_eq!(before.get::<_, i16>(1), 1);
    assert_eq!(before.get::<_, String>(2), initial.storage_key);
    assert_eq!(before.get::<_, Vec<u8>>(3), initial.sha256.to_vec());
    assert_eq!(before.get::<_, Option<i64>>(4), initial.perceptual_hash);

    assert_eq!(
        publish_processed(
            &mut client,
            &regeneration_lease,
            "regen-same",
            &source_sha,
            &initial,
        )
        .expect("regeneration publish"),
        PublishOutcome::Published
    );
    let after = client
        .query_one(
            r#"SELECT p.release_state, p.released_at::text, m.storage_key, m.sha256,
                      m.perceptual_hash, j.state
               FROM posts p
               JOIN media m ON m.post_id=p.id
               JOIN media_jobs j ON j.id=$2
               WHERE p.id=$1"#,
            &[&post_id, &job_id],
        )
        .expect("state after regeneration");
    assert_eq!(after.get::<_, i16>(0), 1);
    assert_eq!(after.get::<_, String>(1), released_at);
    assert_eq!(after.get::<_, String>(2), initial.storage_key);
    assert_eq!(after.get::<_, Vec<u8>>(3), initial.sha256.to_vec());
    assert_eq!(after.get::<_, Option<i64>>(4), Some(111));
    assert_eq!(after.get::<_, i16>(5), 2);
}

#[test]
fn newer_generation_and_source_change_fence_old_regeneration_and_replace_media() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let body = b"old source";
    let (post_id, job_id, old_source_sha) = db.insert_source_job(body);
    let mut client = db.connect();
    let initial_lease = claim(&mut client, "regen-old-publish");
    let old_media = processed(post_id, &old_source_sha, 0x31, 301);
    assert_eq!(
        publish_processed(
            &mut client,
            &initial_lease,
            "regen-old-publish",
            &old_source_sha,
            &old_media,
        )
        .expect("initial publish"),
        PublishOutcome::Published
    );

    requeue(&mut client, job_id);
    let stale_lease = claim(&mut client, "regen-stale");
    let new_source_sha: [u8; 32] = Sha256::digest(b"new source").into();
    client
        .execute(
            "UPDATE media_sources SET sha256=$2, byte_size=$3 WHERE post_id=$1",
            &[
                &post_id,
                &new_source_sha.as_slice(),
                &(b"new source".len() as i64),
            ],
        )
        .expect("replace authoritative source digest");
    client
        .execute(
            r#"UPDATE media_jobs
               SET state=0, attempts=0, available_at=clock_timestamp(),
                   claimed_at=NULL, claimed_by=NULL, lease_expires_at=NULL,
                   lease_generation=lease_generation+1,
                   last_error=NULL, updated_at=clock_timestamp()
               WHERE id=$1"#,
            &[&job_id],
        )
        .expect("supersede stale regeneration");

    assert_eq!(
        publish_processed(
            &mut client,
            &stale_lease,
            "regen-stale",
            &old_source_sha,
            &old_media,
        )
        .expect("stale publish"),
        PublishOutcome::LeaseLostOrConflict
    );
    let retained = client
        .query_one(
            "SELECT storage_key, sha256, perceptual_hash FROM media WHERE post_id=$1",
            &[&post_id],
        )
        .expect("retained media");
    assert_eq!(retained.get::<_, String>(0), old_media.storage_key);
    assert_eq!(retained.get::<_, Vec<u8>>(1), old_media.sha256.to_vec());
    assert_eq!(retained.get::<_, Option<i64>>(2), Some(301));

    let fresh_lease = claim(&mut client, "regen-fresh");
    assert!(fresh_lease.lease_generation > stale_lease.lease_generation);
    let mut new_media = processed(post_id, &new_source_sha, 0x32, 302);
    new_media.width = 800;
    new_media.height = 600;
    new_media.byte_size = 789;
    assert_ne!(new_media.storage_key, old_media.storage_key);
    assert_eq!(
        publish_processed(
            &mut client,
            &fresh_lease,
            "regen-fresh",
            &new_source_sha,
            &new_media,
        )
        .expect("fresh publish"),
        PublishOutcome::Published
    );
    let replaced = client
        .query_one(
            r#"SELECT p.release_state, m.storage_key, m.sha256, m.perceptual_hash,
                      m.width, m.height, m.byte_size, j.state
               FROM posts p
               JOIN media m ON m.post_id=p.id
               JOIN media_jobs j ON j.id=$2
               WHERE p.id=$1"#,
            &[&post_id, &job_id],
        )
        .expect("replaced media");
    assert_eq!(replaced.get::<_, i16>(0), 1);
    assert_eq!(replaced.get::<_, String>(1), new_media.storage_key);
    assert_eq!(replaced.get::<_, Vec<u8>>(2), new_media.sha256.to_vec());
    assert_eq!(replaced.get::<_, Option<i64>>(3), Some(302));
    assert_eq!(replaced.get::<_, i32>(4), 800);
    assert_eq!(replaced.get::<_, i32>(5), 600);
    assert_eq!(replaced.get::<_, i64>(6), 789);
    assert_eq!(replaced.get::<_, i16>(7), 2);
}

#[test]
fn failed_regeneration_keeps_last_good_media_and_can_retry_idempotently() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let body = b"failure source";
    let (post_id, job_id, source_sha) = db.insert_source_job(body);
    let mut client = db.connect();
    let initial_lease = claim(&mut client, "regen-good");
    let good_media = processed(post_id, &source_sha, 0x41, 401);
    assert_eq!(
        publish_processed(
            &mut client,
            &initial_lease,
            "regen-good",
            &source_sha,
            &good_media,
        )
        .expect("initial publish"),
        PublishOutcome::Published
    );

    requeue(&mut client, job_id);
    let failed_lease = claim(&mut client, "regen-fail");
    assert_eq!(
        JobStore::new(&mut client)
            .fail(
                &failed_lease,
                "regen-fail",
                false,
                Duration::from_secs(1),
                "codec failure",
            )
            .expect("fail regeneration"),
        FailureOutcome::Terminal
    );
    let retained = client
        .query_one(
            r#"SELECT p.release_state, m.storage_key, m.sha256, m.perceptual_hash, j.state
               FROM posts p
               JOIN media m ON m.post_id=p.id
               JOIN media_jobs j ON j.id=$2
               WHERE p.id=$1"#,
            &[&post_id, &job_id],
        )
        .expect("retained state");
    assert_eq!(retained.get::<_, i16>(0), 1);
    assert_eq!(retained.get::<_, String>(1), good_media.storage_key);
    assert_eq!(retained.get::<_, Vec<u8>>(2), good_media.sha256.to_vec());
    assert_eq!(retained.get::<_, Option<i64>>(3), Some(401));
    assert_eq!(retained.get::<_, i16>(4), 3);

    requeue(&mut client, job_id);
    let retry_lease = claim(&mut client, "regen-retry");
    assert_eq!(
        publish_processed(
            &mut client,
            &retry_lease,
            "regen-retry",
            &source_sha,
            &good_media,
        )
        .expect("retry publish"),
        PublishOutcome::Published
    );
    let state: i16 = client
        .query_one("SELECT state FROM media_jobs WHERE id=$1", &[&job_id])
        .expect("retry job state")
        .get(0);
    assert_eq!(state, 2);
}
