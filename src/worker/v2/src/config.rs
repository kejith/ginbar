use std::env;
use std::num::NonZeroU64;

const DEFAULT_LEASE_MS: u64 = 30_000;

#[derive(Debug, Clone)]
pub struct Config {
    pub database_url: String,
    pub worker_id: String,
    pub lease_ms: NonZeroU64,
}

impl Config {
    pub fn from_env() -> Result<Self, String> {
        let database_url = env::var("DATABASE_URL")
            .map_err(|_| "DATABASE_URL is required".to_owned())?;

        let worker_id = match env::var("GINBAR_WORKER_ID") {
            Ok(value) if !value.trim().is_empty() => value.trim().to_owned(),
            _ => default_worker_id(),
        };

        let lease_ms = match env::var("GINBAR_WORKER_LEASE_MS") {
            Ok(value) => value
                .parse::<u64>()
                .map_err(|_| "GINBAR_WORKER_LEASE_MS must be a positive integer".to_owned())?,
            Err(_) => DEFAULT_LEASE_MS,
        };
        let lease_ms = NonZeroU64::new(lease_ms)
            .ok_or_else(|| "GINBAR_WORKER_LEASE_MS must be greater than zero".to_owned())?;

        Ok(Self {
            database_url,
            worker_id,
            lease_ms,
        })
    }
}

fn default_worker_id() -> String {
    let host = env::var("HOSTNAME").unwrap_or_else(|_| "worker".to_owned());
    format!("{}-{}", host, std::process::id())
}
