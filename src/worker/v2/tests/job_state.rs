use ginbar_worker_v2::jobs::{ClaimOutcome, FailureOutcome, JobLease, JobStore};
use postgres::{Client, NoTls};
use std::env;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

const CORE_MIGRATION: &str = include_str!("../../../backend/v2/internal/schema/migrations/001_core.sql");
const LEASE_MIGRATION: &str = include_str!("../../../backend/v2/internal/schema/migrations/002_media_job_leases.sql");

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
            .batch_execute(&format!("CREATE SCHEMA {schema}; SET search_path TO {schema};"))
            .expect("create isolated schema");
        owner.batch_execute(CORE_MIGRATION).expect("apply core migration");
        owner.batch_execute(LEASE_MIGRATION).expect("apply lease migration");
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
        .claim_one("worker-b", Duration::from_secs(30))
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
            .claim_one("worker-a", Duration::from_secs(30))
            .expect("first claim"),
    );
    assert_eq!(first.id, job_id);
    assert_eq!(first.attempt, 1);
    assert_eq!(first.lease_generation, 1);

    db.expire(job_id);

    let mut client_b = db.connect();
    let second = claimed(
        JobStore::new(&mut client_b)
            .claim_one("worker-b", Duration::from_secs(30))
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
        .claim_one("worker-c", Duration::from_secs(30))
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
            .claim_one("worker-a", Duration::from_secs(30))
            .expect("first claim"),
    );
    assert_eq!(first.id, job_id);
    assert_eq!(
        JobStore::new(&mut client)
            .fail(
                &first,
                "worker-a",
                true,
                Duration::ZERO,
                "retry me",
            )
            .expect("first failure"),
        FailureOutcome::RetryScheduled
    );

    let second = claimed(
        JobStore::new(&mut client)
            .claim_one("worker-a", Duration::from_secs(30))
            .expect("second claim"),
    );
    assert_eq!(second.attempt, 2);
    assert_eq!(
        JobStore::new(&mut client)
            .fail(
                &second,
                "worker-a",
                true,
                Duration::ZERO,
                "final failure",
            )
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
            .claim_one("worker-a", Duration::from_secs(30))
            .expect("claim"),
    );
    assert!(JobStore::new(&mut client)
        .renew_lease(&lease, "worker-a", Duration::from_secs(60))
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
