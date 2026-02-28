use tracing_subscriber::{fmt, EnvFilter};

/// Application configuration loaded from environment variables.
#[derive(Debug, Clone)]
pub struct Config {
    pub database_url: String,
    pub redis_url: String,
    /// Maximum number of posts processed concurrently.
    pub concurrency: usize,
    /// Seconds between poll cycles when no Redis notification arrives.
    pub poll_interval_secs: u64,
    pub log_level: String,
    pub log_format: String,
}

impl Config {
    pub fn from_env() -> Self {
        Self {
            database_url: env_or(
                "DB_URL",
                "postgres://wallium:devpassword@localhost:5432/wallium?sslmode=disable",
            ),
            redis_url: env_or("REDIS_URL", "redis://localhost:6379"),
            concurrency: env_or("WORKER_CONCURRENCY", "4")
                .parse()
                .unwrap_or(4),
            poll_interval_secs: env_or("WORKER_POLL_INTERVAL", "5")
                .parse()
                .unwrap_or(5),
            log_level: env_or("LOG_LEVEL", "info"),
            log_format: env_or("LOG_FORMAT", "text"),
        }
    }
}

pub fn init_tracing(cfg: &Config) {
    let filter = EnvFilter::try_new(&cfg.log_level).unwrap_or_else(|_| EnvFilter::new("info"));

    match cfg.log_format.as_str() {
        "json" => {
            fmt().json().with_env_filter(filter).init();
        }
        _ => {
            fmt().with_env_filter(filter).init();
        }
    }
}

fn env_or(key: &str, fallback: &str) -> String {
    std::env::var(key).unwrap_or_else(|_| fallback.to_string())
}
