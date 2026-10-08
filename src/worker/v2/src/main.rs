use ginbar_worker_v2::config::Config;
use ginbar_worker_v2::image::BoundedImageProcessor;
use ginbar_worker_v2::jobs::{retry_delay, ClaimOutcome, FailureOutcome, JobLease, JobStore};
use ginbar_worker_v2::media_executor::MediaJobExecutor;
use ginbar_worker_v2::output::OutputStore;
use ginbar_worker_v2::processing::{
    prepare_claimed_source, FailureClass, ImageProcessor, MediaRoot, MediaType, NeverCancelled,
};
use ginbar_worker_v2::publication::{publish_processed, PublishOutcome};
use ginbar_worker_v2::runner::{ConnectionFactory, RunnerSettings, ShutdownToken, WorkerRunner};
use ginbar_worker_v2::video::BoundedVideoProcessor;
use postgres::{Client, Config as PostgresConfig, NoTls};
use std::num::NonZeroU64;
use std::process::ExitCode;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};

const DEFAULT_MAX_SOURCE_BYTES: u64 = 512 * 1024 * 1024;
const RETRY_BASE: Duration = Duration::from_secs(2);
const RETRY_MAX: Duration = Duration::from_secs(30);
const DB_TIMEOUT_DIVISOR: u64 = 10;
const MAX_DB_OPERATION_TIMEOUT_MS: u64 = 3_000;
const READINESS_DB_TIMEOUT: Duration = Duration::from_secs(1);
static TERMINATION_REQUESTED: AtomicBool = AtomicBool::new(false);

fn main() -> ExitCode {
    match run() {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("{error}");
            ExitCode::FAILURE
        }
    }
}

fn run() -> Result<(), String> {
    let mut args = std::env::args();
    let program = args.next().unwrap_or_else(|| "ginbar-worker-v2".to_owned());
    let command = match (args.next(), args.next()) {
        (Some(command), None)
            if command == "claim-once"
                || command == "process-once"
                || command == "ready"
                || command == "run" =>
        {
            command
        }
        _ => {
            return Err(format!(
                "usage: {program} <claim-once|process-once|ready|run>\n\n\
                 claim-once claims at most one durable job and intentionally leaves its lease to expire.\n\
                 process-once claims and processes at most one media job; it requires GINBAR_MEDIA_ROOT.\n\
                 ready performs one bounded PostgreSQL readiness probe and exits without claiming work.\n\
                 run starts the production one-job-at-a-time polling worker with lease renewal and graceful shutdown."
            ));
        }
    };

    let config = Config::from_env()?;
    if command == "ready" {
        return ready_once(&config.database_url);
    }
    if command == "run" {
        return run_forever(&config);
    }

    let mut client =
        ProductionConnectionFactory::new(&config.database_url, config.lease_ms)?.connect()?;
    if command == "claim-once" {
        return claim_once(&mut client, &config);
    }
    process_once(&mut client, &config)
}

fn ready_once(database_url: &str) -> Result<(), String> {
    check_database_readiness(database_url)?;
    println!("ready");
    Ok(())
}

fn check_database_readiness(database_url: &str) -> Result<(), String> {
    readiness_result(|| {
        let factory = ProductionConnectionFactory::new_with_timeout(
            database_url,
            READINESS_DB_TIMEOUT,
        )?;
        let mut client = factory.connect()?;
        client
            .simple_query("SELECT 1")
            .map_err(|error| format!("probe PostgreSQL: {error}"))?;
        Ok(())
    })
}

fn readiness_result<F>(check: F) -> Result<(), String>
where
    F: FnOnce() -> Result<(), String>,
{
    check().map_err(|_| "worker not ready".to_owned())
}

fn run_forever(config: &Config) -> Result<(), String> {
    let media_root_path = std::env::var("GINBAR_MEDIA_ROOT")
        .map_err(|_| "GINBAR_MEDIA_ROOT is required for run".to_owned())?;
    let max_source_bytes = parse_max_source_bytes()?;
    let media_root =
        MediaRoot::new(&media_root_path).map_err(|error| format!("open media root: {error}"))?;
    let executor = MediaJobExecutor::new(media_root, max_source_bytes)?;
    let settings = RunnerSettings::from_env()?;
    install_termination_handlers()?;

    let shutdown = ShutdownToken::with_external(&TERMINATION_REQUESTED);
    let factory = Arc::new(ProductionConnectionFactory::new(
        &config.database_url,
        config.lease_ms,
    )?);
    let mut runner = WorkerRunner::new(
        factory,
        config.worker_id.clone(),
        config.lease_ms,
        settings,
        shutdown,
        executor,
    )?;
    runner.run()
}

#[derive(Debug, Clone)]
struct ProductionConnectionFactory {
    config: PostgresConfig,
}

impl ProductionConnectionFactory {
    fn new(database_url: &str, lease_ms: NonZeroU64) -> Result<Self, String> {
        Self::new_with_timeout(database_url, production_db_timeout(lease_ms))
    }

    fn new_with_timeout(database_url: &str, timeout: Duration) -> Result<Self, String> {
        if timeout.is_zero() {
            return Err("PostgreSQL timeout must be positive".to_owned());
        }
        let mut config = database_url
            .parse::<PostgresConfig>()
            .map_err(|error| format!("parse PostgreSQL configuration: {error}"))?;
        let options = statement_timeout_options(config.get_options(), timeout);
        config.connect_timeout(timeout);
        config.options(&options);
        Ok(Self { config })
    }
}

impl ConnectionFactory for ProductionConnectionFactory {
    fn connect(&self) -> Result<Client, String> {
        self.config
            .connect(NoTls)
            .map_err(|error| format!("connect PostgreSQL: {error}"))
    }
}

fn production_db_timeout(lease_ms: NonZeroU64) -> Duration {
    let timeout_ms = (lease_ms.get() / DB_TIMEOUT_DIVISOR).clamp(1, MAX_DB_OPERATION_TIMEOUT_MS);
    Duration::from_millis(timeout_ms)
}

fn statement_timeout_options(existing: Option<&str>, timeout: Duration) -> String {
    let timeout_ms = timeout.as_millis();
    let statement_timeout = format!("-c statement_timeout={timeout_ms}");
    match existing.map(str::trim).filter(|value| !value.is_empty()) {
        Some(existing) => format!("{existing} {statement_timeout}"),
        None => statement_timeout,
    }
}

fn claim_once(client: &mut Client, config: &Config) -> Result<(), String> {
    let outcome = JobStore::new(client)
        .claim_one(&config.worker_id, config.lease_ms)
        .map_err(|error| format!("claim media job: {error}"))?;

    match outcome {
        ClaimOutcome::Claimed(lease) => {
            println!(
                "claimed job={} post={} kind={} attempt={}/{} worker={} generation={}",
                lease.id,
                lease.post_id,
                lease.kind,
                lease.attempt,
                lease.max_attempts,
                config.worker_id,
                lease.lease_generation
            );
        }
        ClaimOutcome::ExhaustedExpired { id } => {
            println!("finalized exhausted expired job={id}");
        }
        ClaimOutcome::None => {
            println!("no runnable media job");
        }
    }
    Ok(())
}

fn process_once(client: &mut Client, config: &Config) -> Result<(), String> {
    let media_root_path = std::env::var("GINBAR_MEDIA_ROOT")
        .map_err(|_| "GINBAR_MEDIA_ROOT is required for process-once".to_owned())?;
    let max_source_bytes = parse_max_source_bytes()?;
    let media_root =
        MediaRoot::new(&media_root_path).map_err(|error| format!("open media root: {error}"))?;
    let output_store = OutputStore::new(media_root.root())
        .map_err(|error| format!("open output store: {error}"))?;

    let lease = match JobStore::new(client)
        .claim_one(&config.worker_id, config.lease_ms)
        .map_err(|error| format!("claim media job: {error}"))?
    {
        ClaimOutcome::Claimed(lease) => lease,
        ClaimOutcome::ExhaustedExpired { id } => {
            println!("finalized exhausted expired job={id}");
            return Ok(());
        }
        ClaimOutcome::None => {
            println!("no runnable media job");
            return Ok(());
        }
    };

    let mut source = match prepare_claimed_source(
        client,
        &lease,
        &media_root,
        max_source_bytes,
        &NeverCancelled,
    ) {
        Ok(source) => source,
        Err(error) => {
            let class = error.class();
            fail_owned_job(client, config, &lease, class, &error.to_string())?;
            return Err(format!("prepare job {} source: {error}", lease.id));
        }
    };

    let started = Instant::now();
    let mut image_processor = BoundedImageProcessor::new(output_store.clone());
    let mut video_processor = BoundedVideoProcessor::new(output_store);
    let processed = match source.media_type {
        MediaType::Image(_) => image_processor.process_image(&mut source, lease.post_id),
        MediaType::Video(_) => {
            video_processor.process_video_cancellable(&mut source, lease.post_id, &NeverCancelled)
        }
    };
    let processed = match processed {
        Ok(processed) => processed,
        Err(error) => {
            fail_owned_job(client, config, &lease, error.class(), &error.to_string())?;
            return Err(format!("process job {} media: {error}", lease.id));
        }
    };
    let processing_elapsed = started.elapsed();

    match publish_processed(
        client,
        &lease,
        &config.worker_id,
        &source.sha256,
        &processed,
    )
    .map_err(|error| format!("publish job {} metadata: {error}", lease.id))?
    {
        PublishOutcome::Published => {
            println!(
                "processed job={} post={} media_ms={:.3} bytes={} storage_key={}",
                lease.id,
                lease.post_id,
                processing_elapsed.as_secs_f64() * 1000.0,
                processed.byte_size,
                processed.storage_key
            );
            Ok(())
        }
        PublishOutcome::LeaseLostOrConflict => Err(format!(
            "job {} lost its lease or source/post fence before database publication; durable output remains safe for retry",
            lease.id
        )),
    }
}

fn fail_owned_job(
    client: &mut Client,
    config: &Config,
    lease: &JobLease,
    class: FailureClass,
    message: &str,
) -> Result<(), String> {
    let retryable = class == FailureClass::Retryable;
    let retry_after = retry_delay(lease.attempt, RETRY_BASE, RETRY_MAX);
    let outcome = JobStore::new(client)
        .fail(lease, &config.worker_id, retryable, retry_after, message)
        .map_err(|error| format!("record job {} failure: {error}", lease.id))?;
    match outcome {
        FailureOutcome::RetryScheduled | FailureOutcome::Terminal => Ok(()),
        FailureOutcome::LeaseLost => Err(format!(
            "job {} lease was lost while recording processing failure",
            lease.id
        )),
    }
}

fn parse_max_source_bytes() -> Result<u64, String> {
    match std::env::var("GINBAR_WORKER_MAX_SOURCE_BYTES") {
        Ok(value) => {
            let parsed = value.parse::<u64>().map_err(|_| {
                "GINBAR_WORKER_MAX_SOURCE_BYTES must be a positive integer".to_owned()
            })?;
            NonZeroU64::new(parsed)
                .map(NonZeroU64::get)
                .ok_or_else(|| "GINBAR_WORKER_MAX_SOURCE_BYTES must be positive".to_owned())
        }
        Err(std::env::VarError::NotPresent) => Ok(DEFAULT_MAX_SOURCE_BYTES),
        Err(error) => Err(format!("read GINBAR_WORKER_MAX_SOURCE_BYTES: {error}")),
    }
}

#[cfg(unix)]
type SignalHandler = extern "C" fn(i32);

#[cfg(unix)]
unsafe extern "C" {
    fn signal(signal: i32, handler: SignalHandler) -> usize;
}

#[cfg(unix)]
extern "C" fn termination_handler(_signal: i32) {
    TERMINATION_REQUESTED.store(true, Ordering::Release);
}

#[cfg(unix)]
fn install_termination_handlers() -> Result<(), String> {
    const SIGINT: i32 = 2;
    const SIGTERM: i32 = 15;
    const SIG_ERR: usize = usize::MAX;

    // The handler performs only one atomic store. The production target is Linux,
    // and no allocation, locking, I/O, or non-signal-safe library call occurs here.
    let int_result = unsafe { signal(SIGINT, termination_handler) };
    if int_result == SIG_ERR {
        return Err("install SIGINT handler".to_owned());
    }
    let term_result = unsafe { signal(SIGTERM, termination_handler) };
    if term_result == SIG_ERR {
        return Err("install SIGTERM handler".to_owned());
    }
    Ok(())
}

#[cfg(not(unix))]
fn install_termination_handlers() -> Result<(), String> {
    Err("the production worker signal handler currently requires a Unix target".to_owned())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn production_db_timeout_scales_with_lease_and_caps() {
        assert_eq!(
            production_db_timeout(NonZeroU64::new(1).expect("nonzero")),
            Duration::from_millis(1)
        );
        assert_eq!(
            production_db_timeout(NonZeroU64::new(1_000).expect("nonzero")),
            Duration::from_millis(100)
        );
        assert_eq!(
            production_db_timeout(NonZeroU64::new(30_000).expect("nonzero")),
            Duration::from_secs(3)
        );
        assert_eq!(
            production_db_timeout(NonZeroU64::new(300_000).expect("nonzero")),
            Duration::from_secs(3)
        );
    }

    #[test]
    fn production_connection_factory_bounds_connect_and_statement_time() {
        let factory = ProductionConnectionFactory::new(
            "postgresql://localhost/ginbar",
            NonZeroU64::new(30_000).expect("nonzero"),
        )
        .expect("parse production database configuration");

        assert_eq!(
            factory.config.get_connect_timeout(),
            Some(&Duration::from_secs(3))
        );
        assert_eq!(
            factory.config.get_options(),
            Some("-c statement_timeout=3000")
        );
    }

    #[test]
    fn statement_timeout_preserves_existing_server_options() {
        assert_eq!(
            statement_timeout_options(Some("-c lock_timeout=1000"), Duration::from_millis(250)),
            "-c lock_timeout=1000 -c statement_timeout=250"
        );
    }

    #[test]
    fn readiness_connection_factory_is_strictly_bounded() {
        let factory = ProductionConnectionFactory::new_with_timeout(
            "postgresql://localhost/ginbar",
            READINESS_DB_TIMEOUT,
        )
        .expect("parse readiness PostgreSQL configuration");

        assert_eq!(
            factory.config.get_connect_timeout(),
            Some(&READINESS_DB_TIMEOUT)
        );
        assert_eq!(
            factory.config.get_options(),
            Some("-c statement_timeout=1000")
        );
    }

    #[test]
    fn readiness_failure_is_generic_and_recovery_is_observable() {
        let injected = "postgresql://operator:super-secret@db.invalid/ginbar";
        let failed = readiness_result(|| Err(injected.to_owned())).expect_err("failure expected");
        assert_eq!(failed, "worker not ready");
        assert!(!failed.contains("super-secret"));

        let mut dependency_ready = false;
        let first = readiness_result(|| {
            if dependency_ready {
                Ok(())
            } else {
                Err("database unavailable".to_owned())
            }
        });
        assert_eq!(first.expect_err("dependency should be unavailable"), "worker not ready");

        dependency_ready = true;
        let second = readiness_result(|| {
            if dependency_ready {
                Ok(())
            } else {
                Err("database unavailable".to_owned())
            }
        });
        assert!(second.is_ok(), "readiness did not recover: {second:?}");
    }

    #[test]
    fn readiness_probe_accepts_available_test_database() {
        let url = match std::env::var("GINBAR_TEST_DATABASE_URL") {
            Ok(url) => url,
            Err(_) => {
                eprintln!("skipping: GINBAR_TEST_DATABASE_URL is not set");
                return;
            }
        };
        check_database_readiness(&url).expect("test PostgreSQL should be ready");
    }
}
