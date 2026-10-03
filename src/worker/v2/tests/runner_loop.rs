use ginbar_worker_v2::jobs::{ClaimOutcome, JobStore};
use ginbar_worker_v2::runner::{
    ActiveJobCancellation, CancellationReason, ConnectionFactory, JobExecution, JobExecutor,
    RunnerSettings, ShutdownToken, WorkerRunner,
};
use postgres::{Client, NoTls};
use std::env;
use std::num::NonZeroU64;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

const CORE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/001_core.sql");
const LEASE_MIGRATION: &str =
    include_str!("../../../backend/v2/internal/schema/migrations/002_media_job_leases.sql");
static USER_SEQUENCE: AtomicUsize = AtomicUsize::new(0);

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
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("system clock before unix epoch")
            .as_nanos();
        let schema = format!("ginbar_m3_runner_{}_{}", std::process::id(), nonce);
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
        Some(Self {
            factory: TestFactory { url, schema },
            owner,
        })
    }

    fn insert_job(&mut self, max_attempts: i32) -> i64 {
        let user_sequence = USER_SEQUENCE.fetch_add(1, Ordering::Relaxed);
        let username = format!("r{}-{user_sequence}", std::process::id());
        let user_id: i64 = self
            .owner
            .query_one(
                "INSERT INTO users (username) VALUES ($1) RETURNING id",
                &[&username],
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
}

impl Drop for TestDb {
    fn drop(&mut self) {
        let _ = self.owner.batch_execute("SET search_path TO public;");
        let _ = self
            .owner
            .batch_execute(&format!("DROP SCHEMA {} CASCADE;", self.factory.schema));
    }
}

struct CompleteExecutor {
    shutdown: ShutdownToken,
    complete_after: usize,
    completed: usize,
    active: Arc<AtomicUsize>,
}

impl JobExecutor for CompleteExecutor {
    fn execute(
        &mut self,
        client: &mut Client,
        lease: &ginbar_worker_v2::jobs::JobLease,
        worker_id: &str,
        _cancellation: &ActiveJobCancellation,
    ) -> JobExecution {
        assert_eq!(
            self.active.fetch_add(1, Ordering::SeqCst),
            0,
            "runner started more than one active job"
        );
        let completed = JobStore::new(client)
            .complete(lease, worker_id)
            .expect("complete claimed job");
        assert_eq!(self.active.fetch_sub(1, Ordering::SeqCst), 1);
        assert!(completed, "claimed job should still be owned");
        self.completed += 1;
        if self.completed == self.complete_after {
            self.shutdown.request();
        }
        JobExecution::Published
    }
}

#[test]
fn runner_repeats_successfully_and_never_overlaps_jobs() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let first = db.insert_job(3);
    let second = db.insert_job(3);
    let shutdown = ShutdownToken::new();
    let active = Arc::new(AtomicUsize::new(0));
    let executor = CompleteExecutor {
        shutdown: shutdown.clone(),
        complete_after: 2,
        completed: 0,
        active: Arc::clone(&active),
    };
    let mut runner = WorkerRunner::new(
        Arc::new(db.factory.clone()),
        "runner-success".to_owned(),
        lease_ms(1_000),
        test_settings(),
        shutdown,
        executor,
    )
    .expect("build runner");
    runner.run().expect("run worker");

    assert_eq!(active.load(Ordering::SeqCst), 0);
    for id in [first, second] {
        let state: i16 = db
            .owner
            .query_one("SELECT state FROM media_jobs WHERE id = $1", &[&id])
            .expect("read completed job")
            .get(0);
        assert_eq!(state, 2);
    }
}

struct LongExecutor {
    shutdown: ShutdownToken,
    minimum_runtime: Duration,
}

impl JobExecutor for LongExecutor {
    fn execute(
        &mut self,
        client: &mut Client,
        lease: &ginbar_worker_v2::jobs::JobLease,
        worker_id: &str,
        cancellation: &ActiveJobCancellation,
    ) -> JobExecution {
        let started = Instant::now();
        while started.elapsed() < self.minimum_runtime {
            assert_eq!(cancellation.reason(), None, "valid lease was cancelled");
            thread::sleep(Duration::from_millis(5));
        }
        let completed = JobStore::new(client)
            .complete(lease, worker_id)
            .expect("complete long-running job");
        assert!(completed, "lease renewal did not keep long-running claim alive");
        self.shutdown.request();
        JobExecution::Published
    }
}

#[test]
fn lease_renewal_keeps_long_running_claim_alive() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let job_id = db.insert_job(3);
    let shutdown = ShutdownToken::new();
    let executor = LongExecutor {
        shutdown: shutdown.clone(),
        minimum_runtime: Duration::from_millis(350),
    };
    let mut runner = WorkerRunner::new(
        Arc::new(db.factory.clone()),
        "runner-renew".to_owned(),
        lease_ms(120),
        test_settings(),
        shutdown,
        executor,
    )
    .expect("build runner");
    runner.run().expect("run worker");

    let row = db
        .owner
        .query_one(
            "SELECT state, attempts, lease_generation FROM media_jobs WHERE id = $1",
            &[&job_id],
        )
        .expect("read renewed job");
    assert_eq!(row.get::<_, i16>("state"), 2);
    assert_eq!(row.get::<_, i32>("attempts"), 1);
    assert_eq!(row.get::<_, i64>("lease_generation"), 1);
}

#[derive(Clone)]
struct FailHeartbeatFactory {
    inner: TestFactory,
    connects: Arc<AtomicUsize>,
}

impl ConnectionFactory for FailHeartbeatFactory {
    fn connect(&self) -> Result<Client, String> {
        let call = self.connects.fetch_add(1, Ordering::SeqCst);
        if call == 0 {
            self.inner.connect()
        } else {
            Err("injected renewal connection failure".to_owned())
        }
    }
}

struct WaitForCancellationExecutor {
    shutdown: ShutdownToken,
    expected: CancellationReason,
}

impl JobExecutor for WaitForCancellationExecutor {
    fn execute(
        &mut self,
        _client: &mut Client,
        _lease: &ginbar_worker_v2::jobs::JobLease,
        _worker_id: &str,
        cancellation: &ActiveJobCancellation,
    ) -> JobExecution {
        let deadline = Instant::now() + Duration::from_secs(2);
        loop {
            if let Some(reason) = cancellation.reason() {
                assert_eq!(reason, self.expected);
                self.shutdown.request();
                return JobExecution::Cancelled(reason);
            }
            assert!(Instant::now() < deadline, "cancellation was not observed");
            thread::sleep(Duration::from_millis(5));
        }
    }
}

#[test]
fn repeated_renewal_db_failure_is_classified_and_requeued() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let job_id = db.insert_job(3);
    let shutdown = ShutdownToken::new();
    let factory = FailHeartbeatFactory {
        inner: db.factory.clone(),
        connects: Arc::new(AtomicUsize::new(0)),
    };
    let settings = RunnerSettings {
        renew_retry_base: Duration::from_millis(10),
        renew_retry_max: Duration::from_millis(20),
        renew_failure_limit: 2,
        ..test_settings()
    };
    let executor = WaitForCancellationExecutor {
        shutdown: shutdown.clone(),
        expected: CancellationReason::RenewalFailure,
    };
    let mut runner = WorkerRunner::new(
        Arc::new(factory),
        "runner-renew-fail".to_owned(),
        lease_ms(150),
        settings,
        shutdown,
        executor,
    )
    .expect("build runner");
    runner.run().expect("run worker");

    let row = db
        .owner
        .query_one(
            "SELECT state, last_error FROM media_jobs WHERE id = $1",
            &[&job_id],
        )
        .expect("read requeued job");
    assert_eq!(row.get::<_, i16>("state"), 0);
    assert_eq!(
        row.get::<_, Option<String>>("last_error").as_deref(),
        Some("lease renewal failed repeatedly")
    );
}

struct LoseGenerationExecutor {
    factory: TestFactory,
    shutdown: ShutdownToken,
}

impl JobExecutor for LoseGenerationExecutor {
    fn execute(
        &mut self,
        client: &mut Client,
        lease: &ginbar_worker_v2::jobs::JobLease,
        _worker_id: &str,
        cancellation: &ActiveJobCancellation,
    ) -> JobExecution {
        client
            .execute(
                "UPDATE media_jobs SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE id = $1",
                &[&lease.id],
            )
            .expect("expire active lease");
        let mut other = self.factory.connect().expect("connect reclaiming worker");
        let outcome = JobStore::new(&mut other)
            .claim_one("runner-other", lease_ms(1_000))
            .expect("reclaim expired job");
        let ClaimOutcome::Claimed(reclaimed) = outcome else {
            panic!("expected reclaim, got {outcome:?}");
        };
        assert_eq!(reclaimed.id, lease.id);
        assert_eq!(reclaimed.lease_generation, lease.lease_generation + 1);

        let deadline = Instant::now() + Duration::from_secs(2);
        loop {
            if cancellation.reason() == Some(CancellationReason::LeaseLost) {
                self.shutdown.request();
                return JobExecution::Cancelled(CancellationReason::LeaseLost);
            }
            assert!(Instant::now() < deadline, "runner did not observe lost generation");
            thread::sleep(Duration::from_millis(5));
        }
    }
}

#[test]
fn lost_generation_cancels_active_work_without_stale_completion() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let job_id = db.insert_job(4);
    let shutdown = ShutdownToken::new();
    let executor = LoseGenerationExecutor {
        factory: db.factory.clone(),
        shutdown: shutdown.clone(),
    };
    let mut runner = WorkerRunner::new(
        Arc::new(db.factory.clone()),
        "runner-lost".to_owned(),
        lease_ms(150),
        test_settings(),
        shutdown,
        executor,
    )
    .expect("build runner");
    runner.run().expect("run worker");

    let row = db
        .owner
        .query_one(
            "SELECT state, claimed_by, attempts, lease_generation FROM media_jobs WHERE id = $1",
            &[&job_id],
        )
        .expect("read reclaimed job");
    assert_eq!(row.get::<_, i16>("state"), 1);
    assert_eq!(
        row.get::<_, Option<String>>("claimed_by").as_deref(),
        Some("runner-other")
    );
    assert_eq!(row.get::<_, i32>("attempts"), 2);
    assert_eq!(row.get::<_, i64>("lease_generation"), 2);
}

struct AbandonExecutor {
    shutdown: ShutdownToken,
}

impl JobExecutor for AbandonExecutor {
    fn execute(
        &mut self,
        _client: &mut Client,
        _lease: &ginbar_worker_v2::jobs::JobLease,
        _worker_id: &str,
        _cancellation: &ActiveJobCancellation,
    ) -> JobExecution {
        self.shutdown.request();
        JobExecution::OwnershipLost
    }
}

#[test]
fn restart_reclaims_abandoned_lease_and_completes_next_generation() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let job_id = db.insert_job(4);

    let first_shutdown = ShutdownToken::new();
    let mut first_runner = WorkerRunner::new(
        Arc::new(db.factory.clone()),
        "runner-crash-a".to_owned(),
        lease_ms(1_000),
        test_settings(),
        first_shutdown.clone(),
        AbandonExecutor {
            shutdown: first_shutdown,
        },
    )
    .expect("build first runner");
    first_runner.run().expect("run first worker");

    db.owner
        .execute(
            "UPDATE media_jobs SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE id = $1",
            &[&job_id],
        )
        .expect("expire abandoned lease");

    let second_shutdown = ShutdownToken::new();
    let mut second_runner = WorkerRunner::new(
        Arc::new(db.factory.clone()),
        "runner-crash-b".to_owned(),
        lease_ms(1_000),
        test_settings(),
        second_shutdown.clone(),
        CompleteExecutor {
            shutdown: second_shutdown,
            complete_after: 1,
            completed: 0,
            active: Arc::new(AtomicUsize::new(0)),
        },
    )
    .expect("build second runner");
    second_runner.run().expect("run replacement worker");

    let row = db
        .owner
        .query_one(
            "SELECT state, attempts, lease_generation FROM media_jobs WHERE id = $1",
            &[&job_id],
        )
        .expect("read reclaimed job");
    assert_eq!(row.get::<_, i16>("state"), 2);
    assert_eq!(row.get::<_, i32>("attempts"), 2);
    assert_eq!(row.get::<_, i64>("lease_generation"), 2);
}

struct ShutdownAwareExecutor;

impl JobExecutor for ShutdownAwareExecutor {
    fn execute(
        &mut self,
        _client: &mut Client,
        _lease: &ginbar_worker_v2::jobs::JobLease,
        _worker_id: &str,
        cancellation: &ActiveJobCancellation,
    ) -> JobExecution {
        let deadline = Instant::now() + Duration::from_secs(2);
        loop {
            if cancellation.reason() == Some(CancellationReason::Shutdown) {
                return JobExecution::Cancelled(CancellationReason::Shutdown);
            }
            assert!(Instant::now() < deadline, "active job did not observe shutdown");
            thread::sleep(Duration::from_millis(5));
        }
    }
}

#[test]
fn shutdown_during_active_job_requeues_owned_work() {
    let Some(mut db) = TestDb::new() else {
        eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
        return;
    };
    let job_id = db.insert_job(3);
    let shutdown = ShutdownToken::new();
    let runner_shutdown = shutdown.clone();
    let factory = db.factory.clone();
    let handle = thread::spawn(move || {
        let mut runner = WorkerRunner::new(
            Arc::new(factory),
            "runner-shutdown".to_owned(),
            lease_ms(500),
            test_settings(),
            runner_shutdown,
            ShutdownAwareExecutor,
        )
        .expect("build runner");
        runner.run().expect("run worker");
    });

    wait_for_state(&mut db.owner, job_id, 1);
    shutdown.request();
    handle.join().expect("join runner");

    let row = db
        .owner
        .query_one(
            "SELECT state, claimed_by, lease_expires_at, last_error FROM media_jobs WHERE id = $1",
            &[&job_id],
        )
        .expect("read shutdown job");
    assert_eq!(row.get::<_, i16>("state"), 0);
    assert!(row.get::<_, Option<String>>("claimed_by").is_none());
    assert!(row
        .get::<_, Option<SystemTime>>("lease_expires_at")
        .is_none());
    assert_eq!(
        row.get::<_, Option<String>>("last_error").as_deref(),
        Some("worker shutdown")
    );
}

fn wait_for_state(client: &mut Client, job_id: i64, expected: i16) {
    let deadline = Instant::now() + Duration::from_secs(2);
    loop {
        let state: i16 = client
            .query_one("SELECT state FROM media_jobs WHERE id = $1", &[&job_id])
            .expect("read job state")
            .get(0);
        if state == expected {
            return;
        }
        assert!(Instant::now() < deadline, "job did not reach expected state");
        thread::sleep(Duration::from_millis(5));
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

fn lease_ms(ms: u64) -> NonZeroU64 {
    NonZeroU64::new(ms).expect("test lease duration must be positive")
}
