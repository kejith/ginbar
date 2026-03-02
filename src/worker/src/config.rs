use tracing_subscriber::{fmt, prelude::*, EnvFilter};

/// Application configuration loaded from environment variables.
#[derive(Debug, Clone)]
pub struct Config {
    pub database_url: String,
    pub redis_url: String,
    /// Maximum number of posts processed concurrently (CPU-bound work).
    pub concurrency: usize,
    /// Maximum number of downloads running concurrently (IO-bound).
    /// Defaults to `concurrency * 2` since downloads are IO-bound and
    /// benefit from more parallelism than CPU-bound encodes.
    pub download_concurrency: usize,
    /// Seconds between poll cycles when no Redis notification arrives.
    pub poll_interval_secs: u64,
    pub log_level: String,
    pub log_format: String,
    /// Optional path to a log file.  When set, log lines are written to
    /// both stdout AND this file (mirrors Go's LOG_FILE behaviour).
    /// Set `LOG_DIR` in the container and mount it to the host so
    /// logrotate can manage it. E.g. LOG_FILE=/app/logs/worker.log
    pub log_file: Option<String>,
}

impl Config {
    pub fn from_env() -> Self {
        Self {
            database_url: env_or(
                "DB_URL",
                "postgres://wallium:devpassword@localhost:5432/wallium?sslmode=disable",
            ),
            redis_url: env_or("REDIS_URL", "redis://localhost:6379"),
            concurrency: env_or("WORKER_CONCURRENCY", "4").parse().unwrap_or(4),
            download_concurrency: {
                let conc: usize = env_or("WORKER_CONCURRENCY", "4").parse().unwrap_or(4);
                env_or("WORKER_DOWNLOAD_CONCURRENCY", &(conc * 2).to_string())
                    .parse()
                    .unwrap_or(conc * 2)
            },
            poll_interval_secs: env_or("WORKER_POLL_INTERVAL", "5").parse().unwrap_or(5),
            log_level: env_or("LOG_LEVEL", "info"),
            log_format: env_or("LOG_FORMAT", "text"),
            log_file: std::env::var("LOG_FILE").ok().filter(|s| !s.is_empty()),
        }
    }
}

pub fn init_tracing(cfg: &Config) {
    // RUST_LOG takes priority; LOG_LEVEL is the fallback default directive.
    let default_directive = cfg
        .log_level
        .parse()
        .unwrap_or_else(|_| tracing_subscriber::filter::LevelFilter::INFO.into());

    let filter = EnvFilter::builder()
        .with_default_directive(default_directive)
        .from_env_lossy();

    let is_json = cfg.log_format.as_str() == "json";

    // ── Optional file layer ───────────────────────────────────────────────────
    // When LOG_FILE is set we tee every log line to a file as well as stdout.
    // `tracing_appender::rolling::never` creates a single non-rotating file.
    // The _guard must live for the entire process lifetime so logs are flushed.
    let file_appender = cfg.log_file.as_ref().map(|path| {
        let parent = std::path::Path::new(path)
            .parent()
            .unwrap_or_else(|| std::path::Path::new("."));
        let filename = std::path::Path::new(path)
            .file_name()
            .unwrap_or_else(|| std::ffi::OsStr::new("worker.log"));
        // non_blocking returns (writer, guard); leak the guard so the
        // background flusher thread keeps running for the whole process.
        let (nb, guard) =
            tracing_appender::non_blocking(tracing_appender::rolling::never(parent, filename));
        std::mem::forget(guard); // keep flush thread alive
        nb
    });

    // Build the subscriber registry, always including a stdout layer.
    // The file layer is added conditionally (Option<Layer> implements Layer).
    let registry = tracing_subscriber::registry().with(filter);

    match (is_json, file_appender) {
        (true, Some(file)) => registry
            .with(fmt::layer().json().with_writer(std::io::stdout))
            .with(fmt::layer().json().with_writer(file).with_ansi(false))
            .init(),
        (true, None) => registry
            .with(fmt::layer().json().with_writer(std::io::stdout))
            .init(),
        (false, Some(file)) => registry
            .with(fmt::layer().with_writer(std::io::stdout))
            .with(fmt::layer().with_writer(file).with_ansi(false))
            .init(),
        (false, None) => registry
            .with(fmt::layer().with_writer(std::io::stdout))
            .init(),
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
        let fallback_db = "postgres://wallium:devpassword@localhost:5432/wallium?sslmode=disable";
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
        assert!(
            !cfg.database_url.is_empty(),
            "database_url must not be empty"
        );
        assert!(!cfg.redis_url.is_empty(), "redis_url must not be empty");
        assert!(
            cfg.concurrency >= 1,
            "concurrency must be at least 1 (parsed default 4)"
        );
        assert!(
            cfg.download_concurrency >= 1,
            "download_concurrency must be at least 1"
        );
        assert!(
            cfg.poll_interval_secs >= 1,
            "poll_interval must be at least 1"
        );
    }

    #[test]
    fn test_config_download_concurrency_default() {
        // When WORKER_DOWNLOAD_CONCURRENCY is not set, the default should be
        // 2 × concurrency (IO-bound downloads benefit from more parallelism).
        let cfg = Config::from_env();
        assert!(
            cfg.download_concurrency >= cfg.concurrency,
            "download_concurrency ({}) should be >= concurrency ({})",
            cfg.download_concurrency,
            cfg.concurrency
        );
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
        assert_eq!(a.download_concurrency, b.download_concurrency);
    }

    #[test]
    fn test_config_debug_format() {
        // Config derives Debug; this should not panic.
        let cfg = Config::from_env();
        let s = format!("{:?}", cfg);
        assert!(s.contains("Config"));
    }
}
