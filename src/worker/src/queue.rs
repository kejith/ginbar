//! Processing queue: polls the database for dirty posts and processes them
//! concurrently, publishing status updates to Redis.
//!
//! The queue listens for wake-up notifications on a Redis Pub/Sub channel
//! (`wallium:queue:wake`) and also polls every N seconds as a fallback.

use anyhow::Result;
use futures_util::StreamExt;
use redis::AsyncCommands;
use sqlx::PgPool;
use std::path::Path;
use std::sync::atomic::{AtomicBool, AtomicI32, Ordering};
use std::sync::Arc;
use tokio::signal;
use tokio::sync::Semaphore;
use tracing::{info, warn};

use crate::config::Config;
use crate::db::{self, DirtyPost};
use crate::processing::Directories;

const PR0GRAMM_IMG_BASE: &str = "https://img.pr0gramm.com/";
const REDIS_CHANNEL: &str = "wallium:queue:wake";
const REDIS_STATUS_KEY: &str = "wallium:queue:status";

/// Shared mutable counters for the current batch.
struct QueueState {
    pending: AtomicI32,
    active: AtomicI32,
    total: AtomicI32,
    processed: AtomicI32,
    imported: AtomicI32,
    failed: AtomicI32,
    running: AtomicBool,
}

impl QueueState {
    fn new() -> Self {
        Self {
            pending: AtomicI32::new(0),
            active: AtomicI32::new(0),
            total: AtomicI32::new(0),
            processed: AtomicI32::new(0),
            imported: AtomicI32::new(0),
            failed: AtomicI32::new(0),
            running: AtomicBool::new(false),
        }
    }
}

/// Main entry point — runs until SIGINT/SIGTERM.
pub async fn run(
    pool: PgPool,
    redis_client: redis::Client,
    dirs: Directories,
    cfg: &Config,
) -> Result<()> {
    dirs.ensure().await?;

    let state = Arc::new(QueueState::new());
    let sem = Arc::new(Semaphore::new(cfg.concurrency));
    let poll_interval = std::time::Duration::from_secs(cfg.poll_interval_secs);

    // Spawn Redis subscriber for wake-up notifications.
    let (wake_tx, mut wake_rx) = tokio::sync::mpsc::channel::<()>(16);
    {
        let client = redis_client.clone();
        let tx = wake_tx.clone();
        tokio::spawn(async move {
            if let Err(e) = subscribe_loop(client, tx).await {
                warn!("redis subscribe loop exited: {}", e);
            }
        });
    }

    info!(
        concurrency = cfg.concurrency,
        poll_secs = cfg.poll_interval_secs,
        "processing queue started"
    );

    let mut shutdown = Box::pin(signal::ctrl_c());
    let mut ticker = tokio::time::interval(poll_interval);

    // Drain immediately on startup so any dirty posts left from a previous
    // run are picked up without waiting for the first tick or wake.
    drain(&pool, &redis_client, &dirs, &sem, &state, cfg).await;

    loop {
        tokio::select! {
            _ = &mut shutdown => {
                info!("shutdown signal received, stopping queue");
                break;
            }
            _ = wake_rx.recv() => {
                // Drain any extra pending notifications.
                while wake_rx.try_recv().is_ok() {}
                drain(&pool, &redis_client, &dirs, &sem, &state, cfg).await;
            }
            _ = ticker.tick() => {
                drain(&pool, &redis_client, &dirs, &sem, &state, cfg).await;
            }
        }
    }

    // Wait for in-flight tasks to complete.
    info!("waiting for in-flight tasks to complete");
    let _ = sem
        .acquire_many(cfg.concurrency as u32)
        .await;
    info!("all tasks complete, shutting down");
    Ok(())
}

/// Subscribe to Redis Pub/Sub for queue wake-up notifications.
async fn subscribe_loop(
    client: redis::Client,
    tx: tokio::sync::mpsc::Sender<()>,
) -> Result<()> {
    let mut pubsub = client.get_async_pubsub().await?;
    pubsub.subscribe(REDIS_CHANNEL).await?;
    info!("subscribed to redis channel {}", REDIS_CHANNEL);

    let mut stream = pubsub.on_message();
    while let Some(_msg) = stream.next().await {
        let _ = tx.try_send(());
    }
    Ok(())
}

/// Fetch all dirty posts and process them concurrently.
async fn drain(
    pool: &PgPool,
    redis_client: &redis::Client,
    dirs: &Directories,
    sem: &Arc<Semaphore>,
    state: &Arc<QueueState>,
    _cfg: &Config,
) {
    let dirty = match db::get_dirty_posts(pool).await {
        Ok(d) => d,
        Err(e) => {
            warn!("failed to fetch dirty posts: {}", e);
            return;
        }
    };

    if dirty.is_empty() {
        if state.running.load(Ordering::Relaxed) {
            state.running.store(false, Ordering::Relaxed);
            state.pending.store(0, Ordering::Relaxed);
            state.active.store(0, Ordering::Relaxed);
            let _ = publish_status(redis_client, state).await;
        }
        return;
    }

    let n = dirty.len() as i32;
    state.pending.store(n, Ordering::Relaxed);
    state.total.store(n, Ordering::Relaxed);
    state.processed.store(0, Ordering::Relaxed);
    state.imported.store(0, Ordering::Relaxed);
    state.failed.store(0, Ordering::Relaxed);
    state.active.store(0, Ordering::Relaxed);
    state.running.store(true, Ordering::Relaxed);

    let _ = publish_status(redis_client, state).await;

    let mut handles = Vec::with_capacity(dirty.len());

    for post in dirty {
        let pool = pool.clone();
        let dirs = dirs.clone();
        let sem = sem.clone();
        let state = state.clone();
        let redis_client = redis_client.clone();

        state.pending.fetch_sub(1, Ordering::Relaxed);
        state.active.fetch_add(1, Ordering::Relaxed);

        let handle = tokio::spawn(async move {
            // Acquire semaphore permit to limit concurrency.
            let _permit = sem.acquire().await.expect("semaphore closed");

            let result = process_post(&pool, &redis_client, &dirs, &post).await;

            state.active.fetch_sub(1, Ordering::Relaxed);
            state.processed.fetch_add(1, Ordering::Relaxed);

            match &result {
                Ok(()) => {
                    state.imported.fetch_add(1, Ordering::Relaxed);
                }
                Err(e) => {
                    state.failed.fetch_add(1, Ordering::Relaxed);
                    warn!(post_id = post.id, err = %e, "post processing failed");
                }
            }

            let _ = publish_status(&redis_client, &state).await;
        });

        handles.push(handle);
    }

    // Wait for all tasks in this batch.
    for h in handles {
        let _ = h.await;
    }

    state.running.store(false, Ordering::Relaxed);
    state.pending.store(0, Ordering::Relaxed);
    state.active.store(0, Ordering::Relaxed);
    let _ = publish_status(redis_client, state).await;

    info!(
        total = n,
        imported = state.imported.load(Ordering::Relaxed),
        failed = state.failed.load(Ordering::Relaxed),
        "batch complete"
    );
}

/// Process a single dirty post end-to-end.
async fn process_post(pool: &PgPool, redis_client: &redis::Client, dirs: &Directories, post: &DirtyPost) -> Result<()> {
    // Determine where the source file is.
    let (tmp_path, is_temp) = if !post.uploaded_filename.is_empty() {
        // File already uploaded by the user.
        (std::path::PathBuf::from(&post.uploaded_filename), true)
    } else if !post.url.is_empty() {
        let path = if post.url.starts_with(PR0GRAMM_IMG_BASE) {
            crate::download::download_pr0gramm_file(&post.url, &dirs.tmp).await
        } else {
            crate::download::download_file(&post.url, &dirs.tmp).await
        };
        match path {
            Ok(p) => (p, true),
            Err(e) => {
                let _ = db::delete_dirty_post(pool, post.id).await;
                return Err(e.context("download failed"));
            }
        }
    } else {
        let _ = db::delete_dirty_post(pool, post.id).await;
        anyhow::bail!("dirty post {} has neither URL nor uploaded file", post.id);
    };

    // Determine file type by extension.
    let file_type = classify_file(&tmp_path);

    let result = match file_type {
        FileType::Image => process_image_post(pool, redis_client, dirs, post, &tmp_path).await,
        FileType::Video(mime) => process_video_post(pool, dirs, post, &tmp_path, &mime).await,
        FileType::Unknown(mime) => {
            let _ = db::delete_dirty_post(pool, post.id).await;
            anyhow::bail!("unsupported file type: {}", mime);
        }
    };

    // Clean up temp file.
    if is_temp {
        let _ = tokio::fs::remove_file(&tmp_path).await;
    }

    result
}

/// Process an image post: encode, hash, dedup, thumbnail, finalize.
async fn process_image_post(
    pool: &PgPool,
    redis_client: &redis::Client,
    dirs: &Directories,
    post: &DirtyPost,
    input: &Path,
) -> Result<()> {
    let res = crate::processing::process_image(input, dirs).await?;

    // Perceptual-hash duplicate check.
    let [h0, h1, h2, h3] = res.p_hash;
    let dups = db::get_possible_duplicates(pool, h0, h1, h2, h3).await?;

    let real_dups: Vec<_> = dups
        .into_iter()
        .filter(|d| d.id != post.id && !d.dirty)
        .collect();

    if !real_dups.is_empty() {
        // Clean up already-written files.
        let _ = tokio::fs::remove_file(dirs.image.join(&res.filename)).await;
        let _ = tokio::fs::remove_file(dirs.thumbnail.join(&res.thumbnail_filename)).await;

        // Store duplicate info in Redis for the web backend to read.
        let dup_json = serde_json::to_string(&real_dups.iter().map(|d| {
            serde_json::json!({
                "id": d.id,
                "thumbnail_filename": d.thumbnail_filename,
                "hamming_distance": d.hamming_distance,
            })
        }).collect::<Vec<_>>()).unwrap_or_default();

        if let Ok(mut conn) = redis_client.get_multiplexed_async_connection().await {
            let key = format!("dup:post:{}", post.id);
            let _: Result<(), _> = conn.set_ex(&key, &dup_json, 900).await;
        }

        let _ = db::delete_dirty_post(pool, post.id).await;
        anyhow::bail!("duplicate: found {} similar post(s)", real_dups.len());
    }

    db::finalize_post(
        pool,
        &db::FinalizeParams {
            id: post.id,
            filename: res.filename,
            thumbnail_filename: res.thumbnail_filename,
            uploaded_filename: res.uploaded_filename,
            content_type: "image".to_string(),
            p_hash_0: h0,
            p_hash_1: h1,
            p_hash_2: h2,
            p_hash_3: h3,
            width: res.width,
            height: res.height,
        },
    )
    .await?;

    Ok(())
}

/// Process a video post: move, thumbnail, probe, finalize.
async fn process_video_post(
    pool: &PgPool,
    dirs: &Directories,
    post: &DirtyPost,
    input: &Path,
    mime: &str,
) -> Result<()> {
    let res = crate::processing::process_video(input, dirs).await?;

    db::finalize_post(
        pool,
        &db::FinalizeParams {
            id: post.id,
            filename: res.filename.clone(),
            thumbnail_filename: res.thumbnail_filename,
            uploaded_filename: input
                .file_name()
                .map(|f| f.to_string_lossy().to_string())
                .unwrap_or_default(),
            content_type: mime.to_string(),
            p_hash_0: 0,
            p_hash_1: 0,
            p_hash_2: 0,
            p_hash_3: 0,
            width: res.width,
            height: res.height,
        },
    )
    .await?;

    Ok(())
}

/// Publish current queue status to Redis so the web backend can read it.
async fn publish_status(
    redis_client: &redis::Client,
    state: &Arc<QueueState>,
) -> Result<()> {
    let status = serde_json::json!({
        "pending": state.pending.load(Ordering::Relaxed),
        "active": state.active.load(Ordering::Relaxed),
        "total": state.total.load(Ordering::Relaxed),
        "processed": state.processed.load(Ordering::Relaxed),
        "imported": state.imported.load(Ordering::Relaxed),
        "failed": state.failed.load(Ordering::Relaxed),
        "running": state.running.load(Ordering::Relaxed),
    });

    if let Ok(mut conn) = redis_client.get_multiplexed_async_connection().await {
        let _: Result<(), _> = conn
            .set_ex(REDIS_STATUS_KEY, status.to_string(), 60)
            .await;
    }

    Ok(())
}

// ── File classification ───────────────────────────────────────────────────────

enum FileType {
    Image,
    Video(String),
    Unknown(String),
}

fn classify_file(path: &Path) -> FileType {
    let ext = path
        .extension()
        .map(|e| e.to_string_lossy().to_lowercase())
        .unwrap_or_default();

    match ext.as_str() {
        "jpg" | "jpeg" | "png" | "gif" | "webp" | "avif" | "jxl" | "bmp" | "tiff" | "tif" => {
            FileType::Image
        }
        "mp4" => FileType::Video("video/mp4".to_string()),
        "webm" => FileType::Video("video/webm".to_string()),
        "mov" => FileType::Video("video/quicktime".to_string()),
        "avi" => FileType::Video("video/x-msvideo".to_string()),
        "mkv" => FileType::Video("video/x-matroska".to_string()),
        other => FileType::Unknown(format!("unknown/{}", other)),
    }
}
