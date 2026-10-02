use ginbar_worker_v2::jobs::{ClaimOutcome, FailureOutcome, JobLease, JobStore};
use postgres::{Client, NoTls};
use std::env;
use std::num::NonZeroU64;
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

const CORE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/001_core.sql");
const LEASE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/002_media_job_leases.sql");

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
        let schema = format!("ginbar_m3_{}_{}", std::process::id(), nonce);
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
        Some(Self { url, schema, owner })
    }

    fn connect(&self) -> Client {
        let mut client = Client::connect(&self.url, NoTls).expect("connect test database");
        client
            .batch_execute(&format!("SET search_path TO {};", self.schema))
            .expect("set search_path");
        client
    }

    fn insert_job(&mut self, max_attempts: i32) -> i64 {
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
        self.owner
            .query_one(
                "INSERT INTO media_jobs (post_id, kind, max_attempts) VALUES ($1, 0, $2) RETURNING id",
                &[&post_id, &max_attempts],
            )
            .expect("insert media job")
            .get(0)
    }

    fn expire(&mut self, job_id: i64) {
        self.owner
            .execute(
                "UPDATE media_jobs SET lease_expires_at = now() - interval '1 second' WHERE id = $1",
                &[&job_id],
            )
            .expect("expire lease");
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
fn claim_skips_locked_candidate() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let first = db.insert_job(3);
    let second = db.insert_job(3);

    let mut locker = db.connect();
    let mut tx = locker.transaction().expect("start lock transaction");
    let locked: i64 = tx
        .query_one(
            "SELECT id FROM media_jobs ORDER BY available_at, priority DESC, id FOR UPDATE LIMIT 1",
            &[],
        )
        .expect("lock first candidate")
        .get(0);
    assert_eq!(locked, first);

    let mut claimant = db.connect();
    let outcome = JobStore::new(&mut claimant)
        .claim_one("worker-b", lease_ms(30_000))
        .expect("claim unlocked job");
    let ClaimOutcome::Claimed(lease) = outcome else {
        panic!("expected claimed job, got {outcome:?}");
    };
    assert_eq!(lease.id, second);
    tx.rollback().expect("release lock");
}

#[test]
fn expired_lease_reclaims_with_fencing_and_stops_at_max_attempts() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let job_id = db.insert_job(2);

    let mut client_a = db.connect();
    let first = claimed(
        JobStore::new(&mut client_a)
            .claim_one("worker-a", lease_ms(30_000))
            .expect("first claim"),
    );
    assert_eq!(first.id, job_id);
    assert_eq!(first.attempt, 1);
    assert_eq!(first.lease_generation, 1);

    db.expire(job_id);

    let mut client_b = db.connect();
    let second = claimed(
        JobStore::new(&mut client_b)
            .claim_one("worker-b", lease_ms(30_000))
            .expect("reclaim"),
    );
    assert_eq!(second.id, job_id);
    assert_eq!(second.attempt, 2);
    assert_eq!(second.lease_generation, 2);

    assert!(!JobStore::new(&mut client_a)
        .complete(&first, "worker-a")
        .expect("stale completion check"));

    db.expire(job_id);

    let mut client_c = db.connect();
    let outcome = JobStore::new(&mut client_c)
        .claim_one("worker-c", lease_ms(30_000))
        .expect("final reclaim pass");
    assert_eq!(outcome, ClaimOutcome::ExhaustedExpired { id: job_id });

    let row = db
        .owner
        .query_one(
            "SELECT state, attempts, claimed_by, lease_expires_at FROM media_jobs WHERE id = $1",
            &[&job_id],
        )
        .expect("read exhausted job");
    assert_eq!(row.get::<_, i16>("state"), 3);
    assert_eq!(row.get::<_, i32>("attempts"), 2);
    assert!(row.get::<_, Option<String>>("claimed_by").is_none());
    assert!(row
        .get::<_, Option<std::time::SystemTime>>("lease_expires_at")
        .is_none());
}

#[test]
fn retryable_failure_requeues_then_terminalizes_at_limit() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let job_id = db.insert_job(2);

    let mut client = db.connect();
    let first = claimed(
        JobStore::new(&mut client)
            .claim_one("worker-a", lease_ms(30_000))
            .expect("first claim"),
    );
    assert_eq!(first.id, job_id);
    assert_eq!(
        JobStore::new(&mut client)
            .fail(&first, "worker-a", true, Duration::ZERO, "retry me")
            .expect("first failure"),
        FailureOutcome::RetryScheduled
    );

    let second = claimed(
        JobStore::new(&mut client)
            .claim_one("worker-a", lease_ms(30_000))
            .expect("second claim"),
    );
    assert_eq!(second.attempt, 2);
    assert_eq!(
        JobStore::new(&mut client)
            .fail(&second, "worker-a", true, Duration::ZERO, "final failure")
            .expect("second failure"),
        FailureOutcome::Terminal
    );

    let state: i16 = db
        .owner
        .query_one("SELECT state FROM media_jobs WHERE id = $1", &[&job_id])
        .expect("read failed job")
        .get(0);
    assert_eq!(state, 3);
}

#[test]
fn completion_and_renewal_require_current_unexpired_lease() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let job_id = db.insert_job(3);

    let mut client = db.connect();
    let lease = claimed(
        JobStore::new(&mut client)
            .claim_one("worker-a", lease_ms(30_000))
            .expect("claim"),
    );
    assert!(JobStore::new(&mut client)
        .renew_lease(&lease, "worker-a", lease_ms(60_000))
        .expect("renew lease"));
    assert!(JobStore::new(&mut client)
        .complete(&lease, "worker-a")
        .expect("complete job"));
    assert!(!JobStore::new(&mut client)
        .complete(&lease, "worker-a")
        .expect("repeat completion"));

    let state: i16 = db
        .owner
        .query_one("SELECT state FROM media_jobs WHERE id = $1", &[&job_id])
        .expect("read completed job")
        .get(0);
    assert_eq!(state, 2);
}

#[test]
fn mutations_recheck_expiry_after_row_lock_wait() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };

    let complete_job = db.insert_job(3);
    let mut complete_client = db.connect();
    let complete_lease = claimed(
        JobStore::new(&mut complete_client)
            .claim_one("complete-owner", lease_ms(500))
            .expect("claim completion test job"),
    );
    let complete_pid: i32 = complete_client
        .query_one("SELECT pg_backend_pid()", &[])
        .expect("read completion backend pid")
        .get(0);
    let mut locker = db.connect();
    let mut tx = locker.transaction().expect("start completion lock");
    tx.query_one(
        "SELECT id FROM media_jobs WHERE id = $1 FOR UPDATE",
        &[&complete_job],
    )
    .expect("lock completion row");
    let complete_handle = thread::spawn(move || {
        JobStore::new(&mut complete_client)
            .complete(&complete_lease, "complete-owner")
            .expect("completion after lock wait")
    });
    wait_for_blocked_backend_then_expiry(&mut db.owner, complete_pid, complete_job);
    tx.rollback().expect("release completion lock");
    assert!(!complete_handle.join().expect("join completion thread"));

    let renew_job = db.insert_job(3);
    let mut renew_client = db.connect();
    let renew_lease = claimed(
        JobStore::new(&mut renew_client)
            .claim_one("renew-owner", lease_ms(500))
            .expect("claim renewal test job"),
    );
    let renew_pid: i32 = renew_client
        .query_one("SELECT pg_backend_pid()", &[])
        .expect("read renewal backend pid")
        .get(0);
    let mut locker = db.connect();
    let mut tx = locker.transaction().expect("start renewal lock");
    tx.query_one(
        "SELECT id FROM media_jobs WHERE id = $1 FOR UPDATE",
        &[&renew_job],
    )
    .expect("lock renewal row");
    let renew_handle = thread::spawn(move || {
        JobStore::new(&mut renew_client)
            .renew_lease(&renew_lease, "renew-owner", lease_ms(1_000))
            .expect("renewal after lock wait")
    });
    wait_for_blocked_backend_then_expiry(&mut db.owner, renew_pid, renew_job);
    tx.rollback().expect("release renewal lock");
    assert!(!renew_handle.join().expect("join renewal thread"));

    let fail_job = db.insert_job(3);
    let mut fail_client = db.connect();
    let fail_lease = claimed(
        JobStore::new(&mut fail_client)
            .claim_one("fail-owner", lease_ms(500))
            .expect("claim failure test job"),
    );
    let fail_pid: i32 = fail_client
        .query_one("SELECT pg_backend_pid()", &[])
        .expect("read failure backend pid")
        .get(0);
    let mut locker = db.connect();
    let mut tx = locker.transaction().expect("start failure lock");
    tx.query_one(
        "SELECT id FROM media_jobs WHERE id = $1 FOR UPDATE",
        &[&fail_job],
    )
    .expect("lock failure row");
    let fail_handle = thread::spawn(move || {
        JobStore::new(&mut fail_client)
            .fail(
                &fail_lease,
                "fail-owner",
                false,
                Duration::ZERO,
                "should lose expired lease",
            )
            .expect("failure transition after lock wait")
    });
    wait_for_blocked_backend_then_expiry(&mut db.owner, fail_pid, fail_job);
    tx.rollback().expect("release failure lock");
    assert_eq!(
        fail_handle.join().expect("join failure thread"),
        FailureOutcome::LeaseLost
    );
}

fn wait_for_blocked_backend_then_expiry(owner: &mut Client, pid: i32, job_id: i64) {
    let deadline = Instant::now() + Duration::from_secs(2);
    loop {
        let blocked: bool = owner
            .query_one(
                "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock')",
                &[&pid],
            )
            .expect("observe blocked mutation")
            .get(0);
        if blocked {
            let unexpired: bool = owner
                .query_one(
                    "SELECT clock_timestamp() < lease_expires_at FROM media_jobs WHERE id = $1",
                    &[&job_id],
                )
                .expect("check lease before expiry")
                .get(0);
            assert!(unexpired, "mutation did not block before lease expiry");
            break;
        }
        assert!(
            Instant::now() < deadline,
            "mutation did not block on the row lock"
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

fn lease_ms(ms: u64) -> NonZeroU64 {
    NonZeroU64::new(ms).expect("test lease duration must be positive")
}

fn claimed(outcome: ClaimOutcome) -> JobLease {
    match outcome {
        ClaimOutcome::Claimed(lease) => lease,
        other => panic!("expected claimed job, got {other:?}"),
    }
}

fn unique_suffix() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("system clock before unix epoch")
        .as_nanos()
}
