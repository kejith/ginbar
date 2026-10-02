use ginbar_worker_v2::config::Config;
use ginbar_worker_v2::jobs::{ClaimOutcome, JobStore};
use postgres::{Client, NoTls};
use std::process::ExitCode;

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
    match (args.next().as_deref(), args.next()) {
        (Some("claim-once"), None) => {}
        _ => {
            return Err(format!(
                "usage: {program} claim-once\n\n\
                 This M3 boundary probe claims at most one job and exits without completing it. \
                 The lease is intentionally left to expire so crash/reclaim behavior can be tested."
            ));
        }
    }

    let config = Config::from_env()?;
    let mut client = Client::connect(&config.database_url, NoTls)
        .map_err(|error| format!("connect PostgreSQL: {error}"))?;
    let outcome = JobStore::new(&mut client)
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
