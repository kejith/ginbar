use ginbar_worker_v2::jobs::{ClaimOutcome, JobStore};
use ginbar_worker_v2::processing::{
    load_source, prepare_claimed_source, processed_output_key, ImageFormat, MediaKind, MediaRoot,
    MediaType, NeverCancelled, OutputFormat, ProcessedMedia,
};
use ginbar_worker_v2::publication::{publish_processed, PublishOutcome};
use postgres::{Client, NoTls};
use sha2::{Digest, Sha256};
use std::env;
use std::fs;
use std::num::NonZeroU64;
use std::path::PathBuf;
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

const CORE_MIGRATION: &str = include_str!("../../../backend/v2/internal/schema/migrations/001_core.sql");
const LEASE_MIGRATION: &str = include_str!("../../../backend/v2/internal/schema/migrations/002_media_job_leases.sql");
const SOURCE_MIGRATION: &str = include_str!("../../../backend/v2/internal/schema/migrations/003_media_sources.sql");

struct TestDb { url: String, schema: String, owner: Client }
impl TestDb {
    fn new() -> Option<Self> {
        let url = env::var("GINBAR_TEST_DATABASE_URL").ok()?;
        let schema = format!("ginbar_m3_processing_{}_{}", std::process::id(), unique_suffix());
        let mut owner = Client::connect(&url, NoTls).expect("connect test database");
        owner.batch_execute(&format!("CREATE SCHEMA {schema}; SET search_path TO {schema};")).expect("create schema");
        owner.batch_execute(CORE_MIGRATION).expect("apply core migration");
        owner.batch_execute(LEASE_MIGRATION).expect("apply lease migration");
        owner.batch_execute(SOURCE_MIGRATION).expect("apply source migration");
        Some(Self { url, schema, owner })
    }
    fn connect(&self) -> Client {
        let mut client = Client::connect(&self.url, NoTls).expect("connect test database");
        client.batch_execute(&format!("SET search_path TO {};", self.schema)).expect("set search path");
        client
    }
    fn insert_source_job(&mut self, body: &[u8]) -> (i64, i64, String, [u8; 32]) {
        let user_id: i64 = self.owner.query_one(
            "INSERT INTO users (username) VALUES ($1) RETURNING id",
            &[&format!("p{}", unique_suffix())],
        ).expect("insert user").get(0);
        let post_id: i64 = self.owner.query_one(
            "INSERT INTO posts (author_user_id) VALUES ($1) RETURNING id", &[&user_id],
        ).expect("insert post").get(0);
        let source_sha: [u8; 32] = Sha256::digest(body).into();
        let storage_key = source_key(post_id);
        self.owner.execute(
            r#"INSERT INTO media_sources (post_id, source_type, storage_key, declared_mime_type, byte_size, sha256)
               VALUES ($1, 0, $2, '', $3, $4)"#,
            &[&post_id, &storage_key, &(body.len() as i64), &source_sha.as_slice()],
        ).expect("insert source");
        let job_id: i64 = self.owner.query_one(
            "INSERT INTO media_jobs (post_id, kind) VALUES ($1, 0) RETURNING id", &[&post_id],
        ).expect("insert job").get(0);
        (post_id, job_id, storage_key, source_sha)
    }
}
impl Drop for TestDb {
    fn drop(&mut self) {
        let _ = self.owner.batch_execute("SET search_path TO public;");
        let _ = self.owner.batch_execute(&format!("DROP SCHEMA {} CASCADE;", self.schema));
    }
}
struct TempRoot { path: PathBuf }
impl TempRoot {
    fn new(storage_key: &str, body: &[u8]) -> Self {
        let path = env::temp_dir().join(format!("ginbar-m3-processing-{}-{}", std::process::id(), unique_suffix()));
        let destination = path.join(storage_key);
        fs::create_dir_all(destination.parent().expect("source parent")).expect("create source dir");
        fs::write(&destination, body).expect("write source");
        Self { path }
    }
}
impl Drop for TempRoot { fn drop(&mut self) { let _ = fs::remove_dir_all(&self.path); } }
fn source_key(post_id: i64) -> String { let id = format!("{:032x}", post_id); format!("sources/{}/{}", &id[..2], id) }
fn unique_suffix() -> u128 { SystemTime::now().duration_since(UNIX_EPOCH).expect("clock").as_nanos() }
fn processed(post_id: i64, source_sha: &[u8; 32]) -> ProcessedMedia {
    ProcessedMedia {
        kind: MediaKind::Image,
        storage_key: processed_output_key(post_id, source_sha, OutputFormat::Avif).expect("output key"),
        mime_type: "image/avif".to_owned(), width: 640, height: 480, duration_ms: 0,
        byte_size: 456, sha256: [0x22; 32], perceptual_hash: Some(123),
    }
}
fn claim(client: &mut Client, worker: &str, lease_ms: u64) -> ginbar_worker_v2::jobs::JobLease {
    match JobStore::new(client).claim_one(worker, NonZeroU64::new(lease_ms).unwrap()).expect("claim") {
        ClaimOutcome::Claimed(lease) => lease,
        other => panic!("unexpected claim outcome: {other:?}"),
    }
}
fn wait_for_blocked_backend_then_expiry(owner: &mut Client, pid: i32, job_id: i64) {
    let deadline = Instant::now() + Duration::from_secs(2);
    loop {
        let blocked: bool = owner.query_one(
            "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')",
            &[&pid],
        ).expect("observe blocked publisher").get(0);
        if blocked {
            let unexpired: bool = owner.query_one(
                "SELECT clock_timestamp() < lease_expires_at FROM media_jobs WHERE id=$1",
                &[&job_id],
            ).expect("check lease before expiry").get(0);
            assert!(unexpired, "publisher did not block before lease expiry");
            break;
        }
        assert!(Instant::now() < deadline, "publisher did not block on the job row");
        thread::sleep(Duration::from_millis(5));
    }
    loop {
        let expired: bool = owner.query_one(
            "SELECT clock_timestamp() >= lease_expires_at FROM media_jobs WHERE id=$1",
            &[&job_id],
        ).expect("wait for lease expiry").get(0);
        if expired { break; }
        thread::sleep(Duration::from_millis(5));
    }
}

#[test]
fn loads_verifies_and_publishes_under_fenced_lease() {
    let Some(mut db) = TestDb::new() else { eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set"); return; };
    let body = b"\x89PNG\r\n\x1a\nprocessing contract";
    let (post_id, job_id, storage_key, source_sha) = db.insert_source_job(body);
    let mut client = db.connect();
    let source = load_source(&mut client, post_id).expect("load source").expect("source exists");
    assert_eq!(source.sha256, source_sha);
    let lease = claim(&mut client, "processor-1", 5_000);
    assert_eq!(lease.id, job_id);
    let temp = TempRoot::new(&storage_key, body);
    let root = MediaRoot::new(&temp.path).expect("media root");
    let verified = prepare_claimed_source(&mut client, &lease, &root, 1024, &NeverCancelled).expect("prepare source");
    assert_eq!(verified.media_type, MediaType::Image(ImageFormat::Png));
    assert_eq!(verified.sha256, source_sha);
    let output = processed(post_id, &verified.sha256);
    assert_eq!(publish_processed(&mut client, &lease, "processor-1", &verified.sha256, &output).expect("publish"), PublishOutcome::Published);
    let row = client.query_one(
        r#"SELECT p.release_state, p.released_at IS NOT NULL, m.processing_state, m.storage_key, m.sha256,
                  j.state, j.claimed_by, j.lease_expires_at IS NULL
           FROM posts p JOIN media m ON m.post_id=p.id JOIN media_jobs j ON j.post_id=p.id WHERE p.id=$1"#,
        &[&post_id],
    ).expect("read publication");
    assert_eq!(row.get::<_, i16>(0), 1);
    assert!(row.get::<_, bool>(1));
    assert_eq!(row.get::<_, i16>(2), 1);
    assert_eq!(row.get::<_, String>(3), output.storage_key);
    assert_eq!(row.get::<_, Vec<u8>>(4), output.sha256.to_vec());
    assert_eq!(row.get::<_, i16>(5), 2);
    assert_eq!(row.get::<_, Option<String>>(6), None);
    assert!(row.get::<_, bool>(7));
}

#[test]
fn source_digest_change_blocks_publication() {
    let Some(mut db) = TestDb::new() else { eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set"); return; };
    let body = b"\x89PNG\r\n\x1a\nprocessing contract";
    let (post_id, _, _, source_sha) = db.insert_source_job(body);
    let mut client = db.connect();
    let lease = claim(&mut client, "processor-source-race", 5_000);
    client.execute("UPDATE media_sources SET sha256=$2 WHERE post_id=$1", &[&post_id, &[0x44u8; 32].as_slice()]).expect("replace source digest");
    let output = processed(post_id, &source_sha);
    assert_eq!(publish_processed(&mut client, &lease, "processor-source-race", &source_sha, &output).expect("publish"), PublishOutcome::LeaseLostOrConflict);
    let row = client.query_one("SELECT release_state, (SELECT count(*) FROM media WHERE post_id=posts.id) FROM posts WHERE id=$1", &[&post_id]).expect("read state");
    assert_eq!(row.get::<_, i16>(0), 0);
    assert_eq!(row.get::<_, i64>(1), 0);
}

#[test]
fn expired_lease_cannot_publish_or_release() {
    let Some(mut db) = TestDb::new() else { eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set"); return; };
    let body = b"\x89PNG\r\n\x1a\nprocessing contract";
    let (post_id, _, _, source_sha) = db.insert_source_job(body);
    let mut client = db.connect();
    let lease = claim(&mut client, "processor-2", 5_000);
    client.execute("UPDATE media_jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", &[&lease.id]).expect("expire lease");
    assert_eq!(publish_processed(&mut client, &lease, "processor-2", &source_sha, &processed(post_id, &source_sha)).expect("publish"), PublishOutcome::LeaseLostOrConflict);
    let row = client.query_one("SELECT p.release_state, (SELECT count(*) FROM media WHERE post_id=p.id) FROM posts p WHERE p.id=$1", &[&post_id]).expect("read state");
    assert_eq!(row.get::<_, i16>(0), 0);
    assert_eq!(row.get::<_, i64>(1), 0);
}

#[test]
fn publication_rechecks_expiry_after_row_lock_wait() {
    let Some(mut db) = TestDb::new() else { eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set"); return; };
    let body = b"\x89PNG\r\n\x1a\nprocessing contract";
    let (post_id, _, _, source_sha) = db.insert_source_job(body);
    let mut publish_client = db.connect();
    let lease = claim(&mut publish_client, "processor-lockwait", 500);
    let publish_pid: i32 = publish_client.query_one("SELECT pg_backend_pid()", &[]).expect("publisher pid").get(0);

    let mut locker = db.connect();
    let mut tx = locker.transaction().expect("lock transaction");
    tx.query_one("SELECT id FROM media_jobs WHERE id=$1 FOR UPDATE", &[&lease.id]).expect("lock job");
    let lease_for_thread = lease.clone();
    let output = processed(post_id, &source_sha);
    let handle = thread::spawn(move || {
        publish_processed(&mut publish_client, &lease_for_thread, "processor-lockwait", &source_sha, &output).expect("publish")
    });
    wait_for_blocked_backend_then_expiry(&mut db.owner, publish_pid, lease.id);
    tx.rollback().expect("unlock job");
    assert_eq!(handle.join().expect("publisher thread"), PublishOutcome::LeaseLostOrConflict);

    let row = db.owner.query_one(
        "SELECT release_state, (SELECT count(*) FROM media WHERE post_id=posts.id) FROM posts WHERE id=$1",
        &[&post_id],
    ).expect("read state");
    assert_eq!(row.get::<_, i16>(0), 0);
    assert_eq!(row.get::<_, i64>(1), 0);
}
