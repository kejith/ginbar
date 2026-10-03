use ginbar_worker_v2::config::Config;
use ginbar_worker_v2::image::BoundedImageProcessor;
use ginbar_worker_v2::jobs::{retry_delay, ClaimOutcome, FailureOutcome, JobLease, JobStore};
use ginbar_worker_v2::output::OutputStore;
use ginbar_worker_v2::processing::{
    prepare_claimed_source, FailureClass, ImageProcessor, MediaRoot, MediaType, NeverCancelled,
};
use ginbar_worker_v2::publication::{publish_processed, PublishOutcome};
use ginbar_worker_v2::runner::{
    PostgresConnectionFactory, RunnerSettings, ShutdownToken, StillImageJobExecutor, WorkerRunner,
};
use postgres::{Client, NoTls};
use std::num::NonZeroU64;
use std::process::ExitCode;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};

const DEFAULT_MAX_SOURCE_BYTES: u64 = 512 * 1024 * 1024;
const RETRY_BASE: Duration = Duration::from_secs(2);
const RETRY_MAX: Duration = Duration::from_secs(30);
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
            if command == "claim-once" || command == "process-once" || command == "run" =>
        {
            command
        }
        _ => {
            return Err(format!(
                "usage: {program} <claim-once|process-once|run>\n\n\
                 claim-once claims at most one durable job and intentionally leaves its lease to expire.\n\
                 process-once claims and processes at most one still-image job; it requires GINBAR_MEDIA_ROOT.\n\
                 run starts the production one-job-at-a-time polling worker with lease renewal and graceful shutdown."
            ));
        }
    };

    let config = Config::from_env()?;
    if command == "run" {
        return run_forever(&config);
    }

    let mut client = Client::connect(&config.database_url, NoTls)
        .map_err(|error| format!("connect PostgreSQL: {error}"))?;
    if command == "claim-once" {
        return claim_once(&mut client, &config);
    }
    process_once(&mut client, &config)
}

fn run_forever(config: &Config) -> Result<(), String> {
    let media_root_path = std::env::var("GINBAR_MEDIA_ROOT")
        .map_err(|_| "GINBAR_MEDIA_ROOT is required for run".to_owned())?;
    let max_source_bytes = parse_max_source_bytes()?;
    let media_root =
        MediaRoot::new(&media_root_path).map_err(|error| format!("open media root: {error}"))?;
    let executor = StillImageJobExecutor::new(media_root, max_source_bytes)?;
    let settings = RunnerSettings::from_env()?;
    install_termination_handlers()?;

    let shutdown = ShutdownToken::with_external(&TERMINATION_REQUESTED);
    let factory = Arc::new(PostgresConnectionFactory::new(config.database_url.clone()));
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

    if matches!(source.media_type, MediaType::Video(_)) {
        let message = "video processing is intentionally out of scope for this M3 slice";
        fail_owned_job(client, config, &lease, FailureClass::Terminal, message)?;
        return Err(format!("job {}: {message}", lease.id));
    }

    let started = Instant::now();
    let mut processor = BoundedImageProcessor::new(output_store);
    let processed = match processor.process_image(&mut source, lease.post_id) {
        Ok(processed) => processed,
        Err(error) => {
            fail_owned_job(client, config, &lease, error.class(), &error.to_string())?;
            return Err(format!("process job {} image: {error}", lease.id));
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
                "processed job={} post={} image_ms={:.3} bytes={} storage_key={}",
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
