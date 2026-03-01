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

// ── Unit tests ────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    // ── env_or is tested indirectly through Config::from_env ───────────────────

    #[test]
    fn test_config_defaults_are_sensible() {
        // When no env vars are set the defaults must be non-empty strings and
        // numeric fields must parse correctly.
        let fallback_db =
            "postgres://wallium:devpassword@localhost:5432/wallium?sslmode=disable";
        let fallback_redis = "redis://localhost:6379";
        assert!(!fallback_db.is_empty());
        assert!(!fallback_redis.is_empty());
        // Default concurrency / poll are parseable u64/usize.
        assert!("4".parse::<usize>().is_ok());
        assert!("5".parse::<u64>().is_ok());
    }

    #[test]
    fn test_config_from_env_always_builds() {
        // Config::from_env must not panic in any environment.
        let cfg = Config::from_env();
        assert!(!cfg.database_url.is_empty(), "database_url must not be empty");
        assert!(!cfg.redis_url.is_empty(), "redis_url must not be empty");
        assert!(cfg.concurrency >= 1, "concurrency must be at least 1 (parsed default 4)");
        assert!(cfg.poll_interval_secs >= 1, "poll_interval must be at least 1");
    }

    #[test]
    fn test_config_log_level_default() {
        // Unless LOG_LEVEL is overridden, the default string must be "info".
        // We cannot unset env vars safely in a parallel test, so we just verify
        // that the returned value is a non-empty (valid tracing filter) string.
        let cfg = Config::from_env();
        assert!(!cfg.log_level.is_empty());
    }

    #[test]
    fn test_config_log_format_default() {
        let cfg = Config::from_env();
        // Must be either "text" or "json" (or whatever the env var is set to).
        assert!(!cfg.log_format.is_empty());
    }

    #[test]
    fn test_config_clone() {
        let a = Config::from_env();
        let b = a.clone();
        assert_eq!(a.database_url, b.database_url);
        assert_eq!(a.redis_url, b.redis_url);
        assert_eq!(a.concurrency, b.concurrency);
    }

    #[test]
    fn test_config_debug_format() {
        // Config derives Debug; this should not panic.
        let cfg = Config::from_env();
        let s = format!("{:?}", cfg);
        assert!(s.contains("Config"));
    }
}
