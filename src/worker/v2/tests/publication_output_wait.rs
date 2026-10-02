use ginbar_worker_v2::jobs::{ClaimOutcome, JobStore};
use ginbar_worker_v2::processing::{
    OutputPlan, ProcessedMedia, ProcessingStore, PublicationOutcome, PROCESSING_RECIPE_VERSION,
};
use ginbar_worker_v2::source::MediaKind;
use postgres::{Client, NoTls};
use std::env;
use std::num::NonZeroU64;
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

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
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock before epoch")
            .as_nanos();
        let schema = format!("ginbar_m3_output_wait_{}_{}", std::process::id(), nonce);
        let mut owner = Client::connect(&url, NoTls).expect("connect test database");
        owner
            .batch_execute(&format!(
                "CREATE SCHEMA {schema}; SET search_path TO {schema};"
            ))
            .expect("create isolated schema");
        owner.batch_execute(CORE_MIGRATION).expect("apply core migration");
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
            .expect("set search_path");
        client
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

#[test]
fn publication_rechecks_expiry_after_media_row_lock_wait() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };

    let user_id: i64 = db
        .owner
        .query_one(
            "INSERT INTO users (username) VALUES ($1) RETURNING id",
            &[&format!("u{}", unique_suffix())],
        )
        .expect("insert user")
        .get(0);
    let post_id: i64 = db
        .owner
        .query_one(
            "INSERT INTO posts (author_user_id) VALUES ($1) RETURNING id",
            &[&user_id],
        )
        .expect("insert post")
        .get(0);
    let source_sha256 = [3_u8; 32];
    let source_digest: &[u8] = &source_sha256;
    db.owner
        .execute(
            "INSERT INTO media_sources (post_id, source_type, storage_key, declared_mime_type, byte_size, sha256) VALUES ($1, 0, $2, 'ignored/type', 123, $3)",
            &[
                &post_id,
                &"sources/01/0123456789abcdef0123456789abcdef",
                &source_digest,
            ],
        )
        .expect("insert source");
    let job_id: i64 = db
        .owner
        .query_one(
            "INSERT INTO media_jobs (post_id, kind) VALUES ($1, 0) RETURNING id",
            &[&post_id],
        )
        .expect("insert job")
        .get(0);

    let mut claimant = db.connect();
    let outcome = JobStore::new(&mut claimant)
        .claim_one("worker-a", lease_ms(1_000))
        .expect("claim short lease");
    let ClaimOutcome::Claimed(lease) = outcome else {
        panic!("expected claimed job, got {outcome:?}");
    };
    let (plan, media) = output(post_id, source_sha256);
    let output_digest: &[u8] = &media.sha256;
    db.owner
        .execute(
            "INSERT INTO media (post_id, kind, processing_state, storage_key, mime_type, width, height, duration_ms, byte_size, sha256, perceptual_hash) VALUES ($1, 0, 1, $2, $3, $4, $5, 0, $6, $7, $8)",
            &[
                &post_id,
                &media.storage_key,
                &media.mime_type,
                &media.width,
                &media.height,
                &media.byte_size,
                &output_digest,
                &media.perceptual_hash,
            ],
        )
        .expect("insert matching preexisting media");

    let mut locker = db.connect();
    let mut lock_tx = locker.transaction().expect("start media blocker");
    lock_tx
        .query_one(
            "SELECT post_id FROM media WHERE post_id = $1 FOR UPDATE",
            &[&post_id],
        )
        .expect("lock media row");

    let mut publisher = db.connect();
    let publisher_pid: i32 = publisher
        .query_one("SELECT pg_backend_pid()", &[])
        .expect("read publisher pid")
        .get(0);
    let handle = thread::spawn(move || {
        ProcessingStore::new(&mut publisher)
            .publish_processed(&lease, "worker-a", &plan, &media)
            .expect("publication result")
    });

    wait_for_blocked_backend_then_expiry(&mut db.owner, publisher_pid, job_id);
    lock_tx.rollback().expect("release media blocker");
    let outcome = handle.join().expect("join publisher");
    assert_eq!(outcome, PublicationOutcome::LeaseLost);

    let row = db
        .owner
        .query_one(
            "SELECT p.release_state, j.state, m.storage_key FROM posts p JOIN media_jobs j ON j.post_id = p.id JOIN media m ON m.post_id = p.id WHERE p.id = $1 AND j.id = $2",
            &[&post_id, &job_id],
        )
        .expect("read rolled-back state");
    assert_eq!(row.get::<_, i16>(0), 0, "post must remain unreleased");
    assert_eq!(row.get::<_, i16>(1), 1, "job must remain running/expired");
    assert_eq!(row.get::<_, String>(2), output(post_id, source_sha256).0.storage_key);
}

fn output(post_id: i64, source_sha256: [u8; 32]) -> (OutputPlan, ProcessedMedia) {
    let digest = source_sha256
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let storage_key = format!("media/v1/image/{}/{}-{digest}", &digest[..2], post_id);
    let plan = OutputPlan {
        post_id,
        source_sha256,
        media_kind: MediaKind::Image,
        storage_key: storage_key.clone(),
        recipe_version: PROCESSING_RECIPE_VERSION,
    };
    let media = ProcessedMedia {
        kind: MediaKind::Image,
        storage_key,
        mime_type: "image/avif".to_owned(),
        width: 640,
        height: 480,
        duration_ms: 0,
        byte_size: 321,
        sha256: [6_u8; 32],
        perceptual_hash: Some(123),
    };
    (plan, media)
}

fn lease_ms(value: u64) -> NonZeroU64 {
    NonZeroU64::new(value).expect("positive lease")
}

fn unique_suffix() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("clock before epoch")
        .as_nanos()
        ^ u128::from(std::process::id())
}

fn wait_for_blocked_backend_then_expiry(owner: &mut Client, pid: i32, job_id: i64) {
    let deadline = Instant::now() + Duration::from_secs(2);
    loop {
        let blocked: bool = owner
            .query_one(
                "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock')",
                &[&pid],
            )
            .expect("observe blocked publication")
            .get(0);
        if blocked {
            let unexpired: bool = owner
                .query_one(
                    "SELECT clock_timestamp() < lease_expires_at FROM media_jobs WHERE id = $1",
                    &[&job_id],
                )
                .expect("check lease before expiry")
                .get(0);
            assert!(unexpired, "publication did not block before lease expiry");
            break;
        }
        assert!(
            Instant::now() < deadline,
            "publication did not block on the media row lock"
        );
        thread::sleep(Duration::from_millis(5));
    }

    loop {
        let expired: bool = owner
            .query_one(
                "SELECT clock_timestamp() >= lease_expires_at FROM media_jobs WHERE id = $1",
                &[&job_id],
            )
            .expect("wait for lease expiry")
            .get(0);
        if expired {
            break;
        }
        thread::sleep(Duration::from_millis(5));
    }
}
