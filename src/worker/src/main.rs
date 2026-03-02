use wallium_worker::{config, download, processing, queue};

use anyhow::Result;
use tracing::info;

#[tokio::main]
async fn main() -> Result<()> {
    let cfg = config::Config::from_env();
    config::init_tracing(&cfg);

    info!("wallium-worker starting");
    if let Some(f) = &cfg.log_file {
        info!(log_file = %f, "log file tee enabled");
    }

    // ── HTTP client (shared across all download tasks) ────────────────────────
    let http_client = download::build_client(cfg.download_concurrency)?;
    info!(
        download_concurrency = cfg.download_concurrency,
        processing_concurrency = cfg.concurrency,
        "http client ready"
    );

    // ── Database ──────────────────────────────────────────────────────────────
    let pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(8)
        .connect(&cfg.database_url)
        .await?;
    info!("connected to postgres");

    // ── Redis ─────────────────────────────────────────────────────────────────
    let redis = redis::Client::open(cfg.redis_url.as_str())?;
    info!("connected to redis");

    // ── Run the processing loop ───────────────────────────────────────────────
    let dirs = processing::Directories::from_cwd();

    // Spawn the regen queue alongside the main processing queue.
    let regen_pool = pool.clone();
    let regen_redis = redis.clone();
    let regen_dirs = dirs.clone();
    let regen_concurrency = cfg.concurrency;
    tokio::spawn(async move {
        if let Err(e) =
            queue::run_regen_queue(regen_pool, regen_redis, regen_dirs, regen_concurrency).await
        {
            tracing::warn!("regen queue exited: {}", e);
        }
    });

    queue::run(pool, redis, dirs, http_client, &cfg).await
}
