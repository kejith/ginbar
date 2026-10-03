use crate::image::BoundedImageProcessor;
use crate::jobs::{retry_delay, ClaimOutcome, FailureOutcome, JobLease, JobStore};
use crate::output::OutputStore;
use crate::processing::{
    prepare_claimed_source, Cancellation, FailureClass, ImageProcessor, MediaRoot, MediaType,
};
use crate::publication::{publish_processed, PublicationError, PublishOutcome};
use postgres::{Client, NoTls};
use std::num::NonZeroU64;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

const DEFAULT_IDLE_POLL: Duration = Duration::from_millis(500);
const DEFAULT_DB_RETRY_BASE: Duration = Duration::from_millis(250);
const DEFAULT_DB_RETRY_MAX: Duration = Duration::from_secs(5);
const DEFAULT_RENEW_RETRY_BASE: Duration = Duration::from_millis(100);
const DEFAULT_RENEW_RETRY_MAX: Duration = Duration::from_secs(1);
const DEFAULT_RENEW_FAILURE_LIMIT: u32 = 3;
const JOB_RETRY_BASE: Duration = Duration::from_secs(2);
const JOB_RETRY_MAX: Duration = Duration::from_secs(30);
const SHUTDOWN_POLL_GRANULARITY: Duration = Duration::from_millis(50);

#[derive(Debug, Clone)]
pub struct RunnerSettings {
    pub idle_poll: Duration,
    pub db_retry_base: Duration,
    pub db_retry_max: Duration,
    pub renew_retry_base: Duration,
    pub renew_retry_max: Duration,
    pub renew_failure_limit: u32,
}

impl Default for RunnerSettings {
    fn default() -> Self {
        Self {
            idle_poll: DEFAULT_IDLE_POLL,
            db_retry_base: DEFAULT_DB_RETRY_BASE,
            db_retry_max: DEFAULT_DB_RETRY_MAX,
            renew_retry_base: DEFAULT_RENEW_RETRY_BASE,
            renew_retry_max: DEFAULT_RENEW_RETRY_MAX,
            renew_failure_limit: DEFAULT_RENEW_FAILURE_LIMIT,
        }
    }
}

impl RunnerSettings {
    pub fn from_env() -> Result<Self, String> {
        let defaults = Self::default();
        Ok(Self {
            idle_poll: duration_from_env("GINBAR_WORKER_IDLE_POLL_MS", defaults.idle_poll)?,
            db_retry_base: duration_from_env(
                "GINBAR_WORKER_DB_RETRY_BASE_MS",
                defaults.db_retry_base,
            )?,
            db_retry_max: duration_from_env(
                "GINBAR_WORKER_DB_RETRY_MAX_MS",
                defaults.db_retry_max,
            )?,
            renew_retry_base: duration_from_env(
                "GINBAR_WORKER_RENEW_RETRY_BASE_MS",
                defaults.renew_retry_base,
            )?,
            renew_retry_max: duration_from_env(
                "GINBAR_WORKER_RENEW_RETRY_MAX_MS",
                defaults.renew_retry_max,
            )?,
            renew_failure_limit: positive_u32_from_env(
                "GINBAR_WORKER_RENEW_FAILURE_LIMIT",
                defaults.renew_failure_limit,
            )?,
        })
    }

    fn validate(&self) -> Result<(), String> {
        if self.idle_poll.is_zero()
            || self.db_retry_base.is_zero()
            || self.db_retry_max.is_zero()
            || self.renew_retry_base.is_zero()
            || self.renew_retry_max.is_zero()
        {
            return Err("worker polling and retry durations must be positive".to_owned());
        }
        if self.db_retry_base > self.db_retry_max {
            return Err("worker DB retry base must not exceed its maximum".to_owned());
        }
        if self.renew_retry_base > self.renew_retry_max {
            return Err("worker renewal retry base must not exceed its maximum".to_owned());
        }
        if self.renew_failure_limit == 0 {
            return Err("worker renewal failure limit must be positive".to_owned());
        }
        Ok(())
    }
}

fn duration_from_env(name: &str, default: Duration) -> Result<Duration, String> {
    match std::env::var(name) {
        Ok(value) => {
            let millis = value
                .parse::<u64>()
                .map_err(|_| format!("{name} must be a positive integer"))?;
            NonZeroU64::new(millis)
                .map(|value| Duration::from_millis(value.get()))
                .ok_or_else(|| format!("{name} must be positive"))
        }
        Err(std::env::VarError::NotPresent) => Ok(default),
        Err(error) => Err(format!("read {name}: {error}")),
    }
}

fn positive_u32_from_env(name: &str, default: u32) -> Result<u32, String> {
    match std::env::var(name) {
        Ok(value) => {
            let parsed = value
                .parse::<u32>()
                .map_err(|_| format!("{name} must be a positive integer"))?;
            if parsed == 0 {
                return Err(format!("{name} must be positive"));
            }
            Ok(parsed)
        }
        Err(std::env::VarError::NotPresent) => Ok(default),
        Err(error) => Err(format!("read {name}: {error}")),
    }
}

pub trait ConnectionFactory: Send + Sync {
    fn connect(&self) -> Result<Client, String>;
}

#[derive(Debug, Clone)]
pub struct PostgresConnectionFactory {
    database_url: String,
}

impl PostgresConnectionFactory {
    pub fn new(database_url: impl Into<String>) -> Self {
        Self {
            database_url: database_url.into(),
        }
    }
}

impl ConnectionFactory for PostgresConnectionFactory {
    fn connect(&self) -> Result<Client, String> {
        Client::connect(&self.database_url, NoTls)
            .map_err(|error| format!("connect PostgreSQL: {error}"))
    }
}

#[derive(Debug, Clone, Default)]
pub struct ShutdownToken {
    requested: Arc<AtomicBool>,
    external: Option<&'static AtomicBool>,
}

impl ShutdownToken {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn with_external(external: &'static AtomicBool) -> Self {
        Self {
            requested: Arc::new(AtomicBool::new(false)),
            external: Some(external),
        }
    }

    pub fn request(&self) {
        self.requested.store(true, Ordering::Release);
    }

    pub fn is_requested(&self) -> bool {
        self.requested.load(Ordering::Acquire)
            || self
                .external
                .is_some_and(|external| external.load(Ordering::Acquire))
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CancellationReason {
    Shutdown,
    LeaseLost,
    RenewalFailure,
}

#[derive(Debug, Clone)]
pub struct ActiveJobCancellation {
    shutdown: ShutdownToken,
    lease_lost: Arc<AtomicBool>,
    renewal_failed: Arc<AtomicBool>,
}

impl ActiveJobCancellation {
    fn new(shutdown: ShutdownToken) -> Self {
        Self {
            shutdown,
            lease_lost: Arc::new(AtomicBool::new(false)),
            renewal_failed: Arc::new(AtomicBool::new(false)),
        }
    }

    pub fn reason(&self) -> Option<CancellationReason> {
        if self.lease_lost.load(Ordering::Acquire) {
            return Some(CancellationReason::LeaseLost);
        }
        if self.renewal_failed.load(Ordering::Acquire) {
            return Some(CancellationReason::RenewalFailure);
        }
        if self.shutdown.is_requested() {
            return Some(CancellationReason::Shutdown);
        }
        None
    }

    fn mark_lease_lost(&self) {
        self.lease_lost.store(true, Ordering::Release);
    }

    fn mark_renewal_failed(&self) {
        self.renewal_failed.store(true, Ordering::Release);
    }
}

impl Cancellation for ActiveJobCancellation {
    fn is_cancelled(&self) -> bool {
        self.reason().is_some()
    }
}

#[derive(Debug)]
pub enum JobExecution {
    Published,
    Failure {
        class: FailureClass,
        message: String,
    },
    Cancelled(CancellationReason),
    OwnershipLost,
}

pub trait JobExecutor {
    fn execute(
        &mut self,
        client: &mut Client,
        lease: &JobLease,
        worker_id: &str,
        cancellation: &ActiveJobCancellation,
    ) -> JobExecution;
}

pub struct StillImageJobExecutor {
    media_root: MediaRoot,
    max_source_bytes: u64,
    processor: BoundedImageProcessor,
}

impl StillImageJobExecutor {
    pub fn new(media_root: MediaRoot, max_source_bytes: u64) -> Result<Self, String> {
        let output_store = OutputStore::new(media_root.root())
            .map_err(|error| format!("open output store: {error}"))?;
        Ok(Self {
            media_root,
            max_source_bytes,
            processor: BoundedImageProcessor::new(output_store),
        })
    }
}

impl JobExecutor for StillImageJobExecutor {
    fn execute(
        &mut self,
        client: &mut Client,
        lease: &JobLease,
        worker_id: &str,
        cancellation: &ActiveJobCancellation,
    ) -> JobExecution {
        if let Some(reason) = cancellation.reason() {
            return JobExecution::Cancelled(reason);
        }

        let mut source = match prepare_claimed_source(
            client,
            lease,
            &self.media_root,
            self.max_source_bytes,
            cancellation,
        ) {
            Ok(source) => source,
            Err(error) => {
                if let Some(reason) = cancellation.reason() {
                    return JobExecution::Cancelled(reason);
                }
                return JobExecution::Failure {
                    class: error.class(),
                    message: error.to_string(),
                };
            }
        };

        if matches!(source.media_type, MediaType::Video(_)) {
            return JobExecution::Failure {
                class: FailureClass::Terminal,
                message: "video processing is intentionally out of scope for this M3 slice"
                    .to_owned(),
            };
        }
        if let Some(reason) = cancellation.reason() {
            return JobExecution::Cancelled(reason);
        }

        let processed = match self.processor.process_image(&mut source, lease.post_id) {
            Ok(processed) => processed,
            Err(error) => {
                if let Some(reason) = cancellation.reason() {
                    return JobExecution::Cancelled(reason);
                }
                return JobExecution::Failure {
                    class: error.class(),
                    message: error.to_string(),
                };
            }
        };

        // AVIF encode and OutputStore publication are synchronous in processing v1.
        // If ownership is lost during one of those calls, the call may finish before
        // cancellation is observed. Re-check before the authoritative DB commit; any
        // deterministic no-overwrite files already created remain safe for retry.
        if let Some(reason) = cancellation.reason() {
            return JobExecution::Cancelled(reason);
        }

        match publish_processed(client, lease, worker_id, &source.sha256, &processed) {
            Ok(PublishOutcome::Published) => JobExecution::Published,
            Ok(PublishOutcome::LeaseLostOrConflict) => JobExecution::OwnershipLost,
            Err(PublicationError::Database(error)) => JobExecution::Failure {
                class: FailureClass::Retryable,
                message: format!("publish processed media: {error}"),
            },
            Err(PublicationError::Invalid(message)) => JobExecution::Failure {
                class: FailureClass::Terminal,
                message,
            },
        }
    }
}

pub struct WorkerRunner<E> {
    factory: Arc<dyn ConnectionFactory>,
    client: Option<Client>,
    worker_id: String,
    lease_ms: NonZeroU64,
    settings: RunnerSettings,
    shutdown: ShutdownToken,
    executor: E,
}

impl<E> WorkerRunner<E>
where
    E: JobExecutor,
{
    pub fn new(
        factory: Arc<dyn ConnectionFactory>,
        worker_id: String,
        lease_ms: NonZeroU64,
        settings: RunnerSettings,
        shutdown: ShutdownToken,
        executor: E,
    ) -> Result<Self, String> {
        if worker_id.trim().is_empty() {
            return Err("worker ID must not be empty".to_owned());
        }
        settings.validate()?;
        Ok(Self {
            factory,
            client: None,
            worker_id,
            lease_ms,
            settings,
            shutdown,
            executor,
        })
    }

    pub fn run(&mut self) -> Result<(), String> {
        let shutdown = self.shutdown.clone();
        let settings = self.settings.clone();
        drive_loop(&shutdown, &settings, &ThreadWaiter, || self.step())
    }

    fn step(&mut self) -> StepResult {
        if self.shutdown.is_requested() {
            return StepResult::Shutdown;
        }

        if self.client.is_none() {
            match self.factory.connect() {
                Ok(client) => self.client = Some(client),
                Err(error) => return StepResult::Transient(error),
            }
        }

        let claim = {
            let client = self.client.as_mut().expect("client initialized above");
            JobStore::new(client).claim_one(&self.worker_id, self.lease_ms)
        };
        let claim = match claim {
            Ok(claim) => claim,
            Err(error) => {
                self.client = None;
                return StepResult::Transient(format!("claim media job: {error}"));
            }
        };

        let lease = match claim {
            ClaimOutcome::Claimed(lease) => lease,
            ClaimOutcome::ExhaustedExpired { id } => {
                eprintln!("finalized exhausted expired job={id}");
                return StepResult::Worked;
            }
            ClaimOutcome::None => return StepResult::Idle,
        };

        let cancellation = ActiveJobCancellation::new(self.shutdown.clone());
        let heartbeat = LeaseHeartbeat::start(
            Arc::clone(&self.factory),
            lease.clone(),
            self.worker_id.clone(),
            self.lease_ms,
            self.settings.clone(),
            cancellation.clone(),
        );

        let execution = {
            let client = self.client.as_mut().expect("claim used an initialized client");
            self.executor
                .execute(client, &lease, &self.worker_id, &cancellation)
        };
        heartbeat.stop_and_join();

        self.handle_execution(&lease, execution)
    }

    fn handle_execution(&mut self, lease: &JobLease, execution: JobExecution) -> StepResult {
        match execution {
            JobExecution::Published | JobExecution::OwnershipLost => StepResult::Worked,
            JobExecution::Cancelled(CancellationReason::LeaseLost) => StepResult::Worked,
            JobExecution::Cancelled(CancellationReason::Shutdown) => {
                if let Some(client) = self.client.as_mut() {
                    match JobStore::new(client).fail(
                        lease,
                        &self.worker_id,
                        true,
                        Duration::ZERO,
                        "worker shutdown",
                    ) {
                        Ok(FailureOutcome::RetryScheduled | FailureOutcome::Terminal) => {}
                        Ok(FailureOutcome::LeaseLost) => {}
                        Err(error) => {
                            eprintln!(
                                "job {} could not be requeued during shutdown: {error}; lease expiry will recover it",
                                lease.id
                            );
                            self.client = None;
                        }
                    }
                }
                StepResult::Shutdown
            }
            JobExecution::Cancelled(CancellationReason::RenewalFailure) => self.record_failure(
                lease,
                FailureClass::Retryable,
                "lease renewal failed repeatedly",
            ),
            JobExecution::Failure { class, message } => {
                self.record_failure(lease, class, &message)
            }
        }
    }

    fn record_failure(
        &mut self,
        lease: &JobLease,
        class: FailureClass,
        message: &str,
    ) -> StepResult {
        let retryable = class == FailureClass::Retryable;
        let retry_after = retry_delay(lease.attempt, JOB_RETRY_BASE, JOB_RETRY_MAX);
        let result = {
            let Some(client) = self.client.as_mut() else {
                return StepResult::Transient(format!(
                    "job {} failure could not be recorded because the primary connection is unavailable",
                    lease.id
                ));
            };
            JobStore::new(client).fail(
                lease,
                &self.worker_id,
                retryable,
                retry_after,
                message,
            )
        };

        match result {
            Ok(FailureOutcome::RetryScheduled | FailureOutcome::Terminal | FailureOutcome::LeaseLost) => {
                StepResult::Worked
            }
            Err(error) => {
                self.client = None;
                StepResult::Transient(format!("record job {} failure: {error}", lease.id))
            }
        }
    }
}

#[derive(Debug)]
enum StepResult {
    Worked,
    Idle,
    Transient(String),
    Shutdown,
}

trait Waiter {
    fn wait(&self, duration: Duration, shutdown: &ShutdownToken) -> bool;
}

struct ThreadWaiter;

impl Waiter for ThreadWaiter {
    fn wait(&self, duration: Duration, shutdown: &ShutdownToken) -> bool {
        let deadline = Instant::now() + duration;
        while !shutdown.is_requested() {
            let now = Instant::now();
            if now >= deadline {
                return false;
            }
            thread::sleep((deadline - now).min(SHUTDOWN_POLL_GRANULARITY));
        }
        true
    }
}

fn drive_loop<F, W>(
    shutdown: &ShutdownToken,
    settings: &RunnerSettings,
    waiter: &W,
    mut step: F,
) -> Result<(), String>
where
    F: FnMut() -> StepResult,
    W: Waiter,
{
    let mut transient_failures = 0i32;
    loop {
        if shutdown.is_requested() {
            return Ok(());
        }
        match step() {
            StepResult::Worked => transient_failures = 0,
            StepResult::Idle => {
                transient_failures = 0;
                if waiter.wait(settings.idle_poll, shutdown) {
                    return Ok(());
                }
            }
            StepResult::Transient(error) => {
                transient_failures = transient_failures.saturating_add(1);
                eprintln!("worker transient failure: {error}");
                let delay = retry_delay(
                    transient_failures,
                    settings.db_retry_base,
                    settings.db_retry_max,
                );
                if waiter.wait(delay, shutdown) {
                    return Ok(());
                }
            }
            StepResult::Shutdown => return Ok(()),
        }
    }
}

struct LeaseHeartbeat {
    stop: Arc<HeartbeatStop>,
    thread: JoinHandle<()>,
}

impl LeaseHeartbeat {
    fn start(
        factory: Arc<dyn ConnectionFactory>,
        lease: JobLease,
        worker_id: String,
        lease_ms: NonZeroU64,
        settings: RunnerSettings,
        cancellation: ActiveJobCancellation,
    ) -> Self {
        let stop = Arc::new(HeartbeatStop::default());
        let thread_stop = Arc::clone(&stop);
        let thread = thread::spawn(move || {
            heartbeat_loop(
                factory,
                &lease,
                &worker_id,
                lease_ms,
                &settings,
                &cancellation,
                &thread_stop,
            );
        });
        Self { stop, thread }
    }

    fn stop_and_join(self) {
        self.stop.request();
        if self.thread.join().is_err() {
            eprintln!("lease-renewal thread panicked");
        }
    }
}

#[derive(Default)]
struct HeartbeatStop {
    stopped: Mutex<bool>,
    wake: Condvar,
}

impl HeartbeatStop {
    fn request(&self) {
        let mut stopped = self
            .stopped
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        *stopped = true;
        self.wake.notify_one();
    }

    fn wait(&self, duration: Duration) -> bool {
        let stopped = self
            .stopped
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        if *stopped {
            return true;
        }
        let (stopped, _) = self
            .wake
            .wait_timeout(stopped, duration)
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        *stopped
    }
}

fn heartbeat_loop(
    factory: Arc<dyn ConnectionFactory>,
    lease: &JobLease,
    worker_id: &str,
    lease_ms: NonZeroU64,
    settings: &RunnerSettings,
    cancellation: &ActiveJobCancellation,
    stop: &HeartbeatStop,
) {
    let interval = renewal_interval(lease_ms);
    let mut next_wait = interval;
    let mut consecutive_failures = 0u32;
    let mut client: Option<Client> = None;

    loop {
        if stop.wait(next_wait) {
            return;
        }

        if client.is_none() {
            match factory.connect() {
                Ok(connected) => client = Some(connected),
                Err(error) => {
                    consecutive_failures = consecutive_failures.saturating_add(1);
                    eprintln!(
                        "job {} lease renewal connection failure {}/{}: {error}",
                        lease.id, consecutive_failures, settings.renew_failure_limit
                    );
                    if consecutive_failures >= settings.renew_failure_limit {
                        cancellation.mark_renewal_failed();
                        return;
                    }
                    next_wait = retry_delay(
                        consecutive_failures as i32,
                        settings.renew_retry_base,
                        settings.renew_retry_max,
                    );
                    continue;
                }
            }
        }

        let renewal = JobStore::new(client.as_mut().expect("renewal client initialized"))
            .renew_lease(lease, worker_id, lease_ms);
        match renewal {
            Ok(true) => {
                consecutive_failures = 0;
                next_wait = interval;
            }
            Ok(false) => {
                cancellation.mark_lease_lost();
                return;
            }
            Err(error) => {
                client = None;
                consecutive_failures = consecutive_failures.saturating_add(1);
                eprintln!(
                    "job {} lease renewal failure {}/{}: {error}",
                    lease.id, consecutive_failures, settings.renew_failure_limit
                );
                if consecutive_failures >= settings.renew_failure_limit {
                    cancellation.mark_renewal_failed();
                    return;
                }
                next_wait = retry_delay(
                    consecutive_failures as i32,
                    settings.renew_retry_base,
                    settings.renew_retry_max,
                );
            }
        }
    }
}

fn renewal_interval(lease_ms: NonZeroU64) -> Duration {
    Duration::from_millis((lease_ms.get() / 3).max(1))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    struct RecordingWaiter {
        waits: Mutex<Vec<Duration>>,
        shutdown_on_wait: bool,
    }

    impl RecordingWaiter {
        fn new(shutdown_on_wait: bool) -> Self {
            Self {
                waits: Mutex::new(Vec::new()),
                shutdown_on_wait,
            }
        }

        fn waits(&self) -> Vec<Duration> {
            self.waits.lock().expect("lock waits").clone()
        }
    }

    impl Waiter for RecordingWaiter {
        fn wait(&self, duration: Duration, shutdown: &ShutdownToken) -> bool {
            self.waits.lock().expect("lock waits").push(duration);
            if self.shutdown_on_wait {
                shutdown.request();
            }
            shutdown.is_requested()
        }
    }

    #[test]
    fn idle_loop_waits_once_instead_of_busy_spinning() {
        let shutdown = ShutdownToken::new();
        let settings = RunnerSettings::default();
        let waiter = RecordingWaiter::new(true);
        let calls = AtomicUsize::new(0);

        drive_loop(&shutdown, &settings, &waiter, || {
            calls.fetch_add(1, Ordering::Relaxed);
            StepResult::Idle
        })
        .expect("drive idle loop");

        assert_eq!(calls.load(Ordering::Relaxed), 1);
        assert_eq!(waiter.waits(), vec![settings.idle_poll]);
    }

    #[test]
    fn successful_work_repeats_without_poll_delay() {
        let shutdown = ShutdownToken::new();
        let settings = RunnerSettings::default();
        let waiter = RecordingWaiter::new(false);
        let calls = AtomicUsize::new(0);

        drive_loop(&shutdown, &settings, &waiter, || {
            let call = calls.fetch_add(1, Ordering::Relaxed) + 1;
            if call == 3 {
                shutdown.request();
            }
            StepResult::Worked
        })
        .expect("drive work loop");

        assert_eq!(calls.load(Ordering::Relaxed), 3);
        assert!(waiter.waits().is_empty());
    }

    #[test]
    fn transient_errors_use_bounded_backoff() {
        let shutdown = ShutdownToken::new();
        let settings = RunnerSettings {
            db_retry_base: Duration::from_millis(10),
            db_retry_max: Duration::from_millis(25),
            ..RunnerSettings::default()
        };
        let waiter = RecordingWaiter::new(false);
        let calls = AtomicUsize::new(0);

        drive_loop(&shutdown, &settings, &waiter, || {
            let call = calls.fetch_add(1, Ordering::Relaxed) + 1;
            if call == 4 {
                StepResult::Shutdown
            } else {
                StepResult::Transient("injected".to_owned())
            }
        })
        .expect("drive retry loop");

        assert_eq!(
            waiter.waits(),
            vec![
                Duration::from_millis(10),
                Duration::from_millis(20),
                Duration::from_millis(25),
            ]
        );
    }

    #[test]
    fn active_job_cancellation_prioritizes_ownership_failures() {
        let shutdown = ShutdownToken::new();
        let cancellation = ActiveJobCancellation::new(shutdown.clone());
        shutdown.request();
        assert_eq!(cancellation.reason(), Some(CancellationReason::Shutdown));

        cancellation.mark_renewal_failed();
        assert_eq!(
            cancellation.reason(),
            Some(CancellationReason::RenewalFailure)
        );

        cancellation.mark_lease_lost();
        assert_eq!(cancellation.reason(), Some(CancellationReason::LeaseLost));
    }

    #[test]
    fn renewal_interval_is_one_third_of_lease_with_positive_floor() {
        assert_eq!(
            renewal_interval(NonZeroU64::new(30_000).expect("nonzero")),
            Duration::from_secs(10)
        );
        assert_eq!(
            renewal_interval(NonZeroU64::new(1).expect("nonzero")),
            Duration::from_millis(1)
        );
    }
}
