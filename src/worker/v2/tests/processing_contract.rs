use ginbar_worker_v2::jobs::{ClaimOutcome, JobLease, JobStore};
use ginbar_worker_v2::processing::{
    OutputPlan, ProcessedMedia, ProcessingStore, PublicationOutcome, SourceLoadOutcome,
    PROCESSING_RECIPE_VERSION,
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
            .expect("system clock before unix epoch")
            .as_nanos();
        let schema = format!("ginbar_m3_processing_{}_{}", std::process::id(), nonce);
        let mut owner = Client::connect(&url, NoTls).expect("connect test database");
        owner
            .batch_execute(&format!(
                "CREATE SCHEMA {schema}; SET search_path TO {schema};"
            ))
            .expect("create isolated schema");
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
            .expect("set search_path");
        client
    }

    fn insert_source_job(&mut self, source_sha256: [u8; 32]) -> (i64, i64) {
        let user_id: i64 = self
            .owner
            .query_one(
                "INSERT INTO users (username) VALUES ($1) RETURNING id",
                &[&format!("u{}", unique_suffix())],
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
        let digest: &[u8] = &source_sha256;
        self.owner
            .execute(
                "INSERT INTO media_sources (post_id, source_type, storage_key, declared_mime_type, byte_size, sha256) VALUES ($1, 0, $2, 'ignored/type', 123, $3)",
                &[
                    &post_id,
                    &"sources/01/0123456789abcdef0123456789abcdef",
                    &digest,
                ],
            )
            .expect("insert media source");
        let job_id: i64 = self
            .owner
            .query_one(
                "INSERT INTO media_jobs (post_id, kind) VALUES ($1, 0) RETURNING id",
                &[&post_id],
            )
            .expect("insert media job")
            .get(0);
        (post_id, job_id)
    }

    fn claim(&self, worker_id: &str) -> JobLease {
        let mut client = self.connect();
        let outcome = JobStore::new(&mut client)
            .claim_one(worker_id, lease_ms(5_000))
            .expect("claim job");
        let ClaimOutcome::Claimed(lease) = outcome else {
            panic!("expected claimed job, got {outcome:?}");
        };
        lease
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
fn load_source_requires_current_fenced_lease() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let source_sha256 = [9_u8; 32];
    let (post_id, _job_id) = db.insert_source_job(source_sha256);
    let lease = db.claim("worker-a");
    assert_eq!(lease.post_id, post_id);

    let mut client = db.connect();
    let loaded = ProcessingStore::new(&mut client)
        .load_source(&lease, "worker-a")
        .expect("load source");
    let SourceLoadOutcome::Loaded(source) = loaded else {
        panic!("expected loaded source, got {loaded:?}");
    };
    assert_eq!(source.post_id, post_id);
    assert_eq!(source.sha256, source_sha256);
    assert_eq!(source.declared_mime_type, "ignored/type");

    let stale = ProcessingStore::new(&mut client)
        .load_source(&lease, "worker-b")
        .expect("check stale worker");
    assert_eq!(stale, SourceLoadOutcome::LeaseLost);
}

#[test]
fn publish_processed_atomically_releases_post_and_completes_job() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let source_sha256 = [1_u8; 32];
    let (post_id, job_id) = db.insert_source_job(source_sha256);
    let lease = db.claim("worker-a");
    let (plan, media) = output(post_id, source_sha256);

    let mut client = db.connect();
    let outcome = ProcessingStore::new(&mut client)
        .publish_processed(&lease, "worker-a", &plan, &media)
        .expect("publish processed media");
    assert_eq!(outcome, PublicationOutcome::Published);

    let row = client
        .query_one(
            "SELECT p.release_state, p.released_at IS NOT NULL, m.processing_state, m.storage_key, m.sha256, j.state, j.claimed_by IS NULL, j.lease_expires_at IS NULL FROM posts p JOIN media m ON m.post_id = p.id JOIN media_jobs j ON j.post_id = p.id WHERE p.id = $1 AND j.id = $2",
            &[&post_id, &job_id],
        )
        .expect("read published state");
    assert_eq!(row.get::<_, i16>(0), 1);
    assert!(row.get::<_, bool>(1));
    assert_eq!(row.get::<_, i16>(2), 1);
    assert_eq!(row.get::<_, String>(3), plan.storage_key);
    assert_eq!(row.get::<_, Vec<u8>>(4), media.sha256);
    assert_eq!(row.get::<_, i16>(5), 2);
    assert!(row.get::<_, bool>(6));
    assert!(row.get::<_, bool>(7));
}

#[test]
fn expired_publication_rolls_back_media_and_release() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let source_sha256 = [2_u8; 32];
    let (post_id, job_id) = db.insert_source_job(source_sha256);
    let lease = db.claim("worker-a");
    db.owner
        .execute(
            "UPDATE media_jobs SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE id = $1",
            &[&job_id],
        )
        .expect("expire lease");
    let (plan, media) = output(post_id, source_sha256);

    let mut client = db.connect();
    let outcome = ProcessingStore::new(&mut client)
        .publish_processed(&lease, "worker-a", &plan, &media)
        .expect("reject expired publication");
    assert_eq!(outcome, PublicationOutcome::LeaseLost);
    assert_eq!(count(&mut client, "media"), 0);
    let release_state: i16 = client
        .query_one("SELECT release_state FROM posts WHERE id = $1", &[&post_id])
        .expect("read post")
        .get(0);
    assert_eq!(release_state, 0);
}

#[test]
fn publication_rechecks_expiry_after_job_lock_wait() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let source_sha256 = [3_u8; 32];
    let (post_id, job_id) = db.insert_source_job(source_sha256);
    let mut claimant = db.connect();
    let outcome = JobStore::new(&mut claimant)
        .claim_one("worker-a", lease_ms(400))
        .expect("claim short lease");
    let ClaimOutcome::Claimed(lease) = outcome else {
        panic!("expected claim, got {outcome:?}");
    };
    let (plan, media) = output(post_id, source_sha256);

    let mut locker = db.connect();
    let mut lock_tx = locker.transaction().expect("start blocker");
    lock_tx
        .query_one(
            "SELECT id FROM media_jobs WHERE id = $1 FOR UPDATE",
            &[&job_id],
        )
        .expect("lock job");

    let mut publisher = db.connect();
    let publisher_pid: i32 = publisher
        .query_one("SELECT pg_backend_pid()", &[])
        .expect("read publisher backend pid")
        .get(0);
    let handle = thread::spawn(move || {
        ProcessingStore::new(&mut publisher)
            .publish_processed(&lease, "worker-a", &plan, &media)
            .expect("publication result")
    });
    wait_for_blocked_backend_then_expiry(&mut db.owner, publisher_pid, job_id);
    lock_tx.rollback().expect("release blocker");
    let outcome = handle.join().expect("join publisher");
    assert_eq!(outcome, PublicationOutcome::LeaseLost);

    let mut check = db.connect();
    assert_eq!(count(&mut check, "media"), 0);
    let release_state: i16 = check
        .query_one("SELECT release_state FROM posts WHERE id = $1", &[&post_id])
        .expect("read post")
        .get(0);
    assert_eq!(release_state, 0);
}

#[test]
fn stale_generation_cannot_publish_after_reclaim() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let source_sha256 = [7_u8; 32];
    let (post_id, job_id) = db.insert_source_job(source_sha256);
    let first = db.claim("worker-a");
    db.owner
        .execute(
            "UPDATE media_jobs SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE id = $1",
            &[&job_id],
        )
        .expect("expire first lease");
    let second = db.claim("worker-b");
    assert!(second.lease_generation > first.lease_generation);
    let (plan, media) = output(post_id, source_sha256);

    let mut client = db.connect();
    let stale = ProcessingStore::new(&mut client)
        .publish_processed(&first, "worker-a", &plan, &media)
        .expect("reject stale generation");
    assert_eq!(stale, PublicationOutcome::LeaseLost);
    assert_eq!(count(&mut client, "media"), 0);

    let current = ProcessingStore::new(&mut client)
        .publish_processed(&second, "worker-b", &plan, &media)
        .expect("publish current generation");
    assert_eq!(current, PublicationOutcome::Published);
}

#[test]
fn publication_rejects_changed_source_and_conflicting_media() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let source_sha256 = [4_u8; 32];
    let (post_id, _job_id) = db.insert_source_job(source_sha256);
    let lease = db.claim("worker-a");
    let (plan, media) = output(post_id, source_sha256);
    let replacement = vec![5_u8; 32];
    db.owner
        .execute(
            "UPDATE media_sources SET sha256 = $2 WHERE post_id = $1",
            &[&post_id, &replacement],
        )
        .expect("change source digest");

    let mut client = db.connect();
    let outcome = ProcessingStore::new(&mut client)
        .publish_processed(&lease, "worker-a", &plan, &media)
        .expect("reject changed source");
    assert_eq!(outcome, PublicationOutcome::SourceChanged);
    assert_eq!(count(&mut client, "media"), 0);

    let original_digest: &[u8] = &source_sha256;
    db.owner
        .execute(
            "UPDATE media_sources SET sha256 = $2 WHERE post_id = $1",
            &[&post_id, &original_digest],
        )
        .expect("restore source digest");
    let conflicting_digest = [8_u8; 32];
    let conflicting_digest: &[u8] = &conflicting_digest;
    db.owner
        .execute(
            "INSERT INTO media (post_id, kind, processing_state, storage_key, mime_type, width, height, byte_size, sha256) VALUES ($1, 0, 1, 'media/conflict', 'image/avif', 1, 1, 1, $2)",
            &[&post_id, &conflicting_digest],
        )
        .expect("insert conflicting media");
    let outcome = ProcessingStore::new(&mut client)
        .publish_processed(&lease, "worker-a", &plan, &media)
        .expect("reject output conflict");
    assert_eq!(outcome, PublicationOutcome::OutputConflict);
    let release_state: i16 = client
        .query_one("SELECT release_state FROM posts WHERE id = $1", &[&post_id])
        .expect("read post")
        .get(0);
    assert_eq!(release_state, 0);
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

fn count(client: &mut Client, table: &str) -> i64 {
    client
        .query_one(&format!("SELECT count(*) FROM {table}"), &[])
        .expect("count table")
        .get(0)
}

fn lease_ms(value: u64) -> NonZeroU64 {
    NonZeroU64::new(value).expect("positive lease")
}

fn unique_suffix() -> u128 {
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("system clock before unix epoch")
        .as_nanos();
    now ^ u128::from(std::process::id())
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
            "publication did not block on the job row lock"
        );
        thread::sleep(Duration::from_millis(5));
    }

    loop {
        let expired: bool = owner
            .query_one(
                "SELECT clock_timestamp() >= lease_expires_at FROM media_jobs WHERE id = $1",
                &[&job_id],
            )
            .expect("wait for publication lease expiry")
            .get(0);
        if expired {
            break;
        }
        thread::sleep(Duration::from_millis(5));
    }
}
