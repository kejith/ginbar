use wallium_worker::{config, download, processing, queue};

use anyhow::Result;
use tracing::info;

#[tokio::main]
async fn main() -> Result<()> {
    let cfg = config::Config::from_env();
    config::init_tracing(&cfg);

    info!("wallium-worker starting");

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

    queue::run(pool, redis, dirs, http_client, &cfg).await
}
