//! Processing queue: polls the database for dirty posts and processes them
//! concurrently, publishing status updates to Redis.
//!
//! The queue listens for wake-up notifications on a Redis Pub/Sub channel
//! (`wallium:queue:wake`) and also polls every N seconds as a fallback.
//!
//! Concurrency is controlled via `futures_util::StreamExt::for_each_concurrent`
//! which limits in-flight work naturally — no unbounded task spawning.

use anyhow::Result;
use dashmap::DashMap;
use futures_util::StreamExt;
use redis::AsyncCommands;
use serde::Serialize;
use sqlx::PgPool;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicI32, Ordering};
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};
use tokio_stream::wrappers::ReceiverStream;
use tracing::{debug, info, warn};

use crate::config::Config;
use crate::db::{self, DirtyPost};
use crate::processing::Directories;

const PR0GRAMM_IMG_BASE: &str = "https://img.pr0gramm.com/";
const REDIS_CHANNEL: &str = "wallium:queue:wake";
const REDIS_STATUS_KEY: &str = "wallium:queue:status";

/// The processing phase a post is currently in.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "snake_case")]
pub(crate) enum PostPhase {
    Downloading,
    Decoding,
    Encoding,
    DedupCheck,
    Finalizing,
    ProcessingVideo,
}

/// Snapshot of a single in-flight post, included in the Redis status JSON.
#[derive(Debug, Clone, Serialize)]
pub(crate) struct ActivePostInfo {
    pub post_id: i32,
    pub phase: PostPhase,
    /// Unix epoch seconds when this post entered the pipeline.
    pub started_at: u64,
}

impl ActivePostInfo {
    pub(crate) fn new(post_id: i32, phase: PostPhase) -> Self {
        let started_at = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap_or_default()
            .as_secs();
        Self { post_id, phase, started_at }
    }
}

/// Convenience alias for the shared per-post tracking map.
type ActivePosts = Arc<DashMap<i32, ActivePostInfo>>;

/// Shared counters for the current batch (all atomics, no mutex needed).
pub(crate) struct QueueState {
    pending: AtomicI32,
    active: AtomicI32,
    total: AtomicI32,
    processed: AtomicI32,
    imported: AtomicI32,
    failed: AtomicI32,
    running: AtomicBool,
    /// Per-post phase tracking — populated during download, updated through
    /// each processing substep, removed on completion.
    pub(crate) active_posts: ActivePosts,
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
            active_posts: Arc::new(DashMap::new()),
        }
    }
}

/// Main entry point — runs until SIGINT/SIGTERM.
pub async fn run(
    pool: PgPool,
    redis_client: redis::Client,
    dirs: Directories,
    http_client: reqwest::Client,
    cfg: &Config,
) -> Result<()> {
    dirs.ensure().await?;

    let state = Arc::new(QueueState::new());
    let poll_interval = std::time::Duration::from_secs(cfg.poll_interval_secs);
    let concurrency = cfg.concurrency;
    let download_concurrency = cfg.download_concurrency;

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
        concurrency,
        download_concurrency,
        poll_secs = cfg.poll_interval_secs,
        "processing queue started"
    );

    let mut shutdown = Box::pin(tokio::signal::ctrl_c());
    let mut ticker = tokio::time::interval(poll_interval);

    // Drain on startup so posts from a previous run are picked up immediately.
    drain(&pool, &redis_client, &dirs, &http_client, &state, concurrency, download_concurrency).await;

    loop {
        tokio::select! {
            _ = &mut shutdown => {
                info!("shutdown signal received, stopping queue");
                break;
            }
            _ = wake_rx.recv() => {
                // Drain any extra pending notifications.
                while wake_rx.try_recv().is_ok() {}
                drain(&pool, &redis_client, &dirs, &http_client, &state, concurrency, download_concurrency).await;
            }
            _ = ticker.tick() => {
                drain(&pool, &redis_client, &dirs, &http_client, &state, concurrency, download_concurrency).await;
            }
        }
    }

    // Give any in-flight drain a moment to finish (best-effort graceful shutdown).
    info!("queue stopped");
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

/// Fetch all dirty posts and process them using a two-stage pipeline:
///
/// **Stage 1 (download):** Downloads run concurrently with `download_concurrency`.
/// Completed downloads are streamed into a bounded channel.
///
/// **Stage 2 (process):** Processing consumes downloaded posts from the channel
/// with `concurrency` in-flight workers.
///
/// This keeps CPU-bound workers busy from the very first completed download
/// instead of waiting for all downloads to finish. IO is never blocked by
/// a CPU slot.
async fn drain(
    pool: &PgPool,
    redis_client: &redis::Client,
    dirs: &Directories,
    http_client: &reqwest::Client,
    state: &Arc<QueueState>,
    concurrency: usize,
    download_concurrency: usize,
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

    let drain_start = std::time::Instant::now();

    // ── Two-stage pipeline: download → process via bounded channel ──────────
    let chan_size = download_concurrency.max(concurrency) * 2;
    let (dl_tx, dl_rx) = tokio::sync::mpsc::channel::<DownloadedPost>(chan_size);

    // Stage 1: spawn download tasks — runs all downloads concurrently and
    // streams results into the channel as they complete.
    let dl_dirs = dirs.clone();
    let dl_http = http_client.clone();
    let dl_pool = pool.clone();
    let dl_active = state.active_posts.clone();
    let dl_handle = tokio::spawn(async move {
        download_stage(dirty, &dl_dirs, &dl_http, &dl_pool, download_concurrency, dl_active, dl_tx).await;
    });

    // Stage 2: process posts as they arrive from the download channel.
    // Uses `for_each_concurrent(concurrency)` on the receiver stream.
    ReceiverStream::new(dl_rx)
        .for_each_concurrent(Some(concurrency), |item| {
            let pool = pool.clone();
            let dirs = dirs.clone();
            let state = state.clone();
            let redis_client = redis_client.clone();

            async move {
                state.pending.fetch_sub(1, Ordering::Relaxed);
                state.active.fetch_add(1, Ordering::Relaxed);

                let post_id = item.post.id;
                let result = process_downloaded_post(
                    &pool,
                    &redis_client,
                    &dirs,
                    &state.active_posts,
                    item,
                ).await;

                state.active_posts.remove(&post_id);
                state.active.fetch_sub(1, Ordering::Relaxed);
                state.processed.fetch_add(1, Ordering::Relaxed);

                match &result {
                    Ok(()) => {
                        state.imported.fetch_add(1, Ordering::Relaxed);
                    }
                    Err(e) => {
                        state.failed.fetch_add(1, Ordering::Relaxed);
                        let err_chain = e
                            .chain()
                            .map(|c| c.to_string())
                            .collect::<Vec<_>>()
                            .join(": ");
                        warn!(post_id, err = err_chain, "post processing failed");
                    }
                }

                let _ = publish_status(&redis_client, &state).await;
            }
        })
        .await;

    // Ensure download stage is done (should already be — tx was dropped).
    let _ = dl_handle.await;

    state.running.store(false, Ordering::Relaxed);
    state.pending.store(0, Ordering::Relaxed);
    state.active.store(0, Ordering::Relaxed);
    let _ = publish_status(redis_client, state).await;

    info!(
        total = n,
        imported = state.imported.load(Ordering::Relaxed),
        failed = state.failed.load(Ordering::Relaxed),
        elapsed_ms = drain_start.elapsed().as_millis(),
        "batch complete"
    );
}

/// A post that has been downloaded and is ready for processing.
pub(crate) struct DownloadedPost {
    pub post: DirtyPost,
    pub path: PathBuf,
    pub is_temp: bool,
}

/// Stage 1: download all posts concurrently, streaming results into the channel.
///
/// Each post is downloaded (or resolved from a local upload path) and sent
/// into `tx`. Failed downloads are logged and the dirty post is deleted so
/// they don't retry forever — they never enter the processing stage.
async fn download_stage(
    posts: Vec<DirtyPost>,
    dirs: &Directories,
    http_client: &reqwest::Client,
    pool: &PgPool,
    download_concurrency: usize,
    active_posts: ActivePosts,
    tx: tokio::sync::mpsc::Sender<DownloadedPost>,
) {
    futures_util::stream::iter(posts)
        .for_each_concurrent(Some(download_concurrency), |post| {
            let dirs = dirs.clone();
            let http_client = http_client.clone();
            let pool = pool.clone();
            let tx = tx.clone();
            let active_posts = active_posts.clone();

            async move {
                active_posts.insert(post.id, ActivePostInfo::new(post.id, PostPhase::Downloading));

                let result = resolve_source(&pool, &dirs, &http_client, &post).await;
                match result {
                    Ok(item) => {
                        if tx.send(item).await.is_err() {
                            active_posts.remove(&post.id);
                            warn!(post_id = post.id, "download stage: receiver dropped");
                        }
                    }
                    Err(e) => {
                        active_posts.remove(&post.id);
                        let err_chain = e
                            .chain()
                            .map(|c| c.to_string())
                            .collect::<Vec<_>>()
                            .join(": ");
                        warn!(post_id = post.id, err = err_chain, "download failed, skipping post");
                        let _ = db::delete_dirty_post(&pool, post.id).await;
                    }
                }
            }
        })
        .await;
    // tx is dropped here, closing the channel → ReceiverStream completes.
}

/// Resolve the source file for a post: either from a local upload path or download.
async fn resolve_source(
    pool: &PgPool,
    dirs: &Directories,
    http_client: &reqwest::Client,
    post: &DirtyPost,
) -> Result<DownloadedPost> {
    if !post.uploaded_filename.is_empty() {
        return Ok(DownloadedPost {
            post: post.clone(),
            path: PathBuf::from(&post.uploaded_filename),
            is_temp: false,
        });
    }

    if !post.url.is_empty() {
        let download_start = std::time::Instant::now();
        let path = if post.url.starts_with(PR0GRAMM_IMG_BASE) {
            crate::download::download_pr0gramm_file(http_client, &post.url, &dirs.tmp).await
        } else {
            crate::download::download_file(http_client, &post.url, &dirs.tmp).await
        };
        match path {
            Ok(p) => {
                debug!(
                    post_id = post.id,
                    url = post.url,
                    elapsed_ms = download_start.elapsed().as_millis(),
                    "worker: download complete"
                );
                return Ok(DownloadedPost {
                    post: post.clone(),
                    path: p,
                    is_temp: true,
                });
            }
            Err(e) => {
                return Err(e.context("download failed"));
            }
        }
    }

    let _ = db::delete_dirty_post(pool, post.id).await;
    anyhow::bail!("dirty post {} has neither URL nor uploaded file", post.id);
}

/// Process a post that has already been downloaded.
async fn process_downloaded_post(
    pool: &PgPool,
    redis_client: &redis::Client,
    dirs: &Directories,
    active_posts: &ActivePosts,
    item: DownloadedPost,
) -> Result<()> {
    let post_start = std::time::Instant::now();

    // Transition: download complete → decoding.
    if let Some(mut entry) = active_posts.get_mut(&item.post.id) {
        entry.phase = PostPhase::Decoding;
    }

    let file_type = classify_file(&item.path);

    let proc_start = std::time::Instant::now();
    let result = match file_type {
        FileType::Image => {
            process_image_post(pool, redis_client, dirs, active_posts, &item.post, &item.path).await
        }
        FileType::Video(mime) => {
            process_video_post(pool, dirs, active_posts, &item.post, &item.path, &mime).await
        }
        FileType::Unknown(mime) => {
            let _ = db::delete_dirty_post(pool, item.post.id).await;
            anyhow::bail!("unsupported file type: {}", mime);
        }
    };

    if item.is_temp {
        let _ = tokio::fs::remove_file(&item.path).await;
    }

    if result.is_err() {
        if let Err(e) = db::delete_dirty_post(pool, item.post.id).await {
            warn!(post_id = item.post.id, err = %e, "failed to delete dirty post after processing error");
        }
    } else {
        debug!(
            post_id = item.post.id,
            proc_elapsed_ms = proc_start.elapsed().as_millis(),
            total_elapsed_ms = post_start.elapsed().as_millis(),
            "worker: post processing complete"
        );
    }

    result
}

/// Process an image post: encode, hash, dedup, thumbnail, finalize.
pub(crate) async fn process_image_post(
    pool: &PgPool,
    redis_client: &redis::Client,
    dirs: &Directories,
    active_posts: &ActivePosts,
    post: &DirtyPost,
    input: &Path,
) -> Result<()> {
    // Transition: decoding → encoding (decode + encode run together inside process_image).
    if let Some(mut entry) = active_posts.get_mut(&post.id) {
        entry.phase = PostPhase::Encoding;
    }

    let proc_start = std::time::Instant::now();
    let res = crate::processing::process_image(input, dirs).await?;
    debug!(
        post_id = post.id,
        filename = res.filename,
        width = res.width,
        height = res.height,
        elapsed_ms = proc_start.elapsed().as_millis(),
        "worker: image encode complete"
    );

    // Transition: encoding done → dedup check.
    if let Some(mut entry) = active_posts.get_mut(&post.id) {
        entry.phase = PostPhase::DedupCheck;
    }

    let dedup_start = std::time::Instant::now();
    let [h0, h1, h2, h3] = res.p_hash;
    let dups = db::get_possible_duplicates(pool, h0, h1, h2, h3).await?;
    let real_dups: Vec<_> = dups
        .into_iter()
        .filter(|d| d.id != post.id && !d.dirty)
        .collect();
    debug!(
        post_id = post.id,
        dup_candidates = real_dups.len(),
        elapsed_ms = dedup_start.elapsed().as_millis(),
        "worker: phash dedup check complete"
    );

    if !real_dups.is_empty() {
        let _ = tokio::fs::remove_file(dirs.image.join(&res.filename)).await;
        let _ = tokio::fs::remove_file(dirs.thumbnail.join(&res.thumbnail_filename)).await;

        let dup_json = serde_json::to_string(
            &real_dups
                .iter()
                .map(|d| {
                    serde_json::json!({
                        "id": d.id,
                        "thumbnail_filename": d.thumbnail_filename,
                        "hamming_distance": d.hamming_distance,
                    })
                })
                .collect::<Vec<_>>(),
        )
        .unwrap_or_default();

        if let Ok(mut conn) = redis_client.get_multiplexed_async_connection().await {
            let key = format!("dup:post:{}", post.id);
            let _: Result<(), _> = conn.set_ex(&key, &dup_json, 900).await;
        }

        let _ = db::delete_dirty_post(pool, post.id).await;
        anyhow::bail!("duplicate: found {} similar post(s)", real_dups.len());
    }

    // Transition: dedup passed → writing to DB.
    if let Some(mut entry) = active_posts.get_mut(&post.id) {
        entry.phase = PostPhase::Finalizing;
    }

    let finalize_start = std::time::Instant::now();
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
    debug!(
        post_id = post.id,
        elapsed_ms = finalize_start.elapsed().as_millis(),
        "worker: post finalized in db"
    );

    Ok(())
}

/// Process a video post: move, thumbnail, probe, finalize.
pub(crate) async fn process_video_post(
    pool: &PgPool,
    dirs: &Directories,
    active_posts: &ActivePosts,
    post: &DirtyPost,
    input: &Path,
    mime: &str,
) -> Result<()> {
    // Transition: decoding → video processing (ffmpeg extract + thumbnail).
    if let Some(mut entry) = active_posts.get_mut(&post.id) {
        entry.phase = PostPhase::ProcessingVideo;
    }

    let proc_start = std::time::Instant::now();
    let res = crate::processing::process_video(input, dirs).await?;
    debug!(
        post_id = post.id,
        filename = res.filename,
        width = res.width,
        height = res.height,
        elapsed_ms = proc_start.elapsed().as_millis(),
        "worker: video processing complete"
    );

    // Transition: video processed → writing to DB.
    if let Some(mut entry) = active_posts.get_mut(&post.id) {
        entry.phase = PostPhase::Finalizing;
    }

    let finalize_start = std::time::Instant::now();

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
    debug!(
        post_id = post.id,
        elapsed_ms = finalize_start.elapsed().as_millis(),
        "worker: video post finalized in db"
    );

    Ok(())
}

/// Publish current queue status to Redis.
pub(crate) async fn publish_status(redis_client: &redis::Client, state: &Arc<QueueState>) -> Result<()> {
    // Collect a snapshot of per-post phase info (cheap — reads from DashMap shards).
    let now_secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs();
    let active_posts_vec: Vec<serde_json::Value> = state
        .active_posts
        .iter()
        .map(|entry| {
            let info = entry.value();
            let elapsed = now_secs.saturating_sub(info.started_at);
            serde_json::json!({
                "post_id": info.post_id,
                "phase": info.phase,
                "elapsed_sec": elapsed,
            })
        })
        .collect();

    let status = serde_json::json!({
        "pending": state.pending.load(Ordering::Relaxed),
        "active": state.active.load(Ordering::Relaxed),
        "total": state.total.load(Ordering::Relaxed),
        "processed": state.processed.load(Ordering::Relaxed),
        "imported": state.imported.load(Ordering::Relaxed),
        "failed": state.failed.load(Ordering::Relaxed),
        "running": state.running.load(Ordering::Relaxed),
        "active_posts": active_posts_vec,
    });

    if let Ok(mut conn) = redis_client.get_multiplexed_async_connection().await {
        let _: Result<(), _> = conn.set_ex(REDIS_STATUS_KEY, status.to_string(), 60).await;
    }

    Ok(())
}

// ── File classification ───────────────────────────────────────────────────────

#[derive(Debug, PartialEq)]
pub(crate) enum FileType {
    Image,
    Video(String),
    Unknown(String),
}

pub(crate) fn classify_file(path: &Path) -> FileType {
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

// ── Unit tests ────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::Path;

    // ── PostPhase serialization ────────────────────────────────────────────────

    #[test]
    fn test_post_phase_serializes_snake_case() {
        // All phase variants must serialize to lowercase snake_case strings
        // so the frontend can match them literally.
        let cases = [
            (PostPhase::Downloading,     "\"downloading\""),
            (PostPhase::Decoding,        "\"decoding\""),
            (PostPhase::Encoding,        "\"encoding\""),
            (PostPhase::DedupCheck,      "\"dedup_check\""),
            (PostPhase::Finalizing,      "\"finalizing\""),
            (PostPhase::ProcessingVideo, "\"processing_video\""),
        ];
        for (phase, expected) in &cases {
            let got = serde_json::to_string(phase).expect("serialize PostPhase");
            assert_eq!(&got, expected, "unexpected serialization for {:?}", phase);
        }
    }

    // ── ActivePostInfo ────────────────────────────────────────────────────────

    #[test]
    fn test_active_post_info_new_fields() {
        let info = ActivePostInfo::new(42, PostPhase::Downloading);
        assert_eq!(info.post_id, 42);
        assert_eq!(info.phase, PostPhase::Downloading);
        // started_at must be a reasonable epoch second (after 2020-01-01).
        assert!(info.started_at > 1_577_836_800, "started_at should be a real timestamp");
    }

    #[test]
    fn test_active_post_info_serializes_to_json() {
        let info = ActivePostInfo::new(7, PostPhase::Encoding);
        let json = serde_json::to_value(&info).expect("serialize ActivePostInfo");
        assert_eq!(json["post_id"], 7);
        assert_eq!(json["phase"], "encoding");
        assert!(json["started_at"].is_number());
    }

    // ── active_posts DashMap tracking ─────────────────────────────────────────

    #[test]
    fn test_active_posts_insert_update_remove() {
        let active: ActivePosts = Arc::new(DashMap::new());

        // Insert a post in the Downloading phase.
        active.insert(100, ActivePostInfo::new(100, PostPhase::Downloading));
        assert!(active.contains_key(&100));
        assert_eq!(active.get(&100).unwrap().phase, PostPhase::Downloading);

        // Transition to Encoding.
        if let Some(mut entry) = active.get_mut(&100) {
            entry.phase = PostPhase::Encoding;
        }
        assert_eq!(active.get(&100).unwrap().phase, PostPhase::Encoding);

        // Remove on completion.
        active.remove(&100);
        assert!(!active.contains_key(&100));
    }

    #[test]
    fn test_active_posts_concurrent_inserts() {
        // Verify DashMap handles multiple concurrent keys without collision.
        let active: ActivePosts = Arc::new(DashMap::new());
        for id in 0..20i32 {
            active.insert(id, ActivePostInfo::new(id, PostPhase::Downloading));
        }
        assert_eq!(active.len(), 20);
        for id in 0..20i32 {
            active.remove(&id);
        }
        assert!(active.is_empty());
    }

    #[test]
    fn test_queue_state_includes_active_posts() {
        // QueueState must expose active_posts and start empty.
        let s = QueueState::new();
        assert!(s.active_posts.is_empty(), "active_posts must start empty");

        s.active_posts.insert(5, ActivePostInfo::new(5, PostPhase::Finalizing));
        assert_eq!(s.active_posts.len(), 1);
    }

    // ── QueueState ────────────────────────────────────────────────────────────

    #[test]
    fn test_queue_state_new_all_zero() {
        let s = QueueState::new();
        assert_eq!(s.pending.load(Ordering::Relaxed), 0);
        assert_eq!(s.active.load(Ordering::Relaxed), 0);
        assert_eq!(s.total.load(Ordering::Relaxed), 0);
        assert_eq!(s.processed.load(Ordering::Relaxed), 0);
        assert_eq!(s.imported.load(Ordering::Relaxed), 0);
        assert_eq!(s.failed.load(Ordering::Relaxed), 0);
        assert!(!s.running.load(Ordering::Relaxed));
    }

    #[test]
    fn test_queue_state_atomic_increments() {
        let s = QueueState::new();
        s.pending.fetch_add(5, Ordering::Relaxed);
        s.active.fetch_add(2, Ordering::Relaxed);
        s.total.fetch_add(10, Ordering::Relaxed);
        assert_eq!(s.pending.load(Ordering::Relaxed), 5);
        assert_eq!(s.active.load(Ordering::Relaxed), 2);
        assert_eq!(s.total.load(Ordering::Relaxed), 10);
    }

    #[test]
    fn test_queue_state_running_flag() {
        let s = QueueState::new();
        s.running.store(true, Ordering::Relaxed);
        assert!(s.running.load(Ordering::Relaxed));
        s.running.store(false, Ordering::Relaxed);
        assert!(!s.running.load(Ordering::Relaxed));
    }

    // ── classify_file ─────────────────────────────────────────────────────────

    #[test]
    fn test_classify_jpeg_is_image() {
        assert_eq!(classify_file(Path::new("photo.jpg")), FileType::Image);
        assert_eq!(classify_file(Path::new("photo.jpeg")), FileType::Image);
    }

    #[test]
    fn test_classify_image_extensions() {
        for ext in &["jpg", "jpeg", "png", "gif", "webp", "avif", "jxl", "bmp", "tiff", "tif"] {
            let p = std::path::PathBuf::from("file").with_extension(ext);
            assert_eq!(
                classify_file(&p),
                FileType::Image,
                "expected Image for .{}",
                ext
            );
        }
    }

    #[test]
    fn test_classify_mp4_is_video() {
        assert_eq!(
            classify_file(Path::new("clip.mp4")),
            FileType::Video("video/mp4".to_string())
        );
    }

    #[test]
    fn test_classify_video_extensions() {
        let expected = [
            ("mp4", "video/mp4"),
            ("webm", "video/webm"),
            ("mov", "video/quicktime"),
            ("avi", "video/x-msvideo"),
            ("mkv", "video/x-matroska"),
        ];
        for (ext, mime) in &expected {
            let p = std::path::PathBuf::from("file").with_extension(ext);
            assert_eq!(
                classify_file(&p),
                FileType::Video(mime.to_string()),
                "wrong mime for .{}",
                ext
            );
        }
    }

    #[test]
    fn test_classify_unknown_extension() {
        assert_eq!(
            classify_file(Path::new("archive.tar")),
            FileType::Unknown("unknown/tar".to_string())
        );
    }

    #[test]
    fn test_classify_no_extension() {
        assert_eq!(
            classify_file(Path::new("no_extension")),
            FileType::Unknown("unknown/".to_string())
        );
    }

    #[test]
    fn test_classify_uppercase_extension() {
        // Extensions must be matched case-insensitively.
        assert_eq!(classify_file(Path::new("photo.JPG")), FileType::Image);
        assert_eq!(classify_file(Path::new("clip.MP4")), FileType::Video("video/mp4".to_string()));
    }

    #[test]
    fn test_classify_path_with_dirs() {
        assert_eq!(
            classify_file(Path::new("/some/deep/path/photo.png")),
            FileType::Image
        );
    }

    // ── Integration tests (require Postgres + Redis) ───────────────────────────
    //
    // Run with:
    //   cargo test -p wallium-worker -- --ignored --test-threads=1

    const TEST_DB_URL: &str =
        "postgres://wallium:devpassword@localhost:5432/wallium?sslmode=disable";
    const TEST_REDIS_URL: &str = "redis://localhost:6379";

    async fn queue_test_pool() -> sqlx::PgPool {
        sqlx::postgres::PgPoolOptions::new()
            .max_connections(2)
            .connect(TEST_DB_URL)
            .await
            .expect("postgres must be running — use the devcontainer")
    }

    fn queue_test_redis() -> redis::Client {
        redis::Client::open(TEST_REDIS_URL).expect("redis must be running")
    }

    fn make_queue_dirs() -> (crate::processing::Directories, tempfile::TempDir) {
        let tmp = tempfile::TempDir::new().unwrap();
        let r = tmp.path();
        let dirs = crate::processing::Directories {
            image: r.join("images"),
            thumbnail: r.join("thumbnails"),
            video: r.join("videos"),
            tmp: r.join("tmp"),
            upload: r.join("upload"),
        };
        for d in [
            &dirs.image, &dirs.thumbnail, &dirs.video,
            &dirs.tmp, &dirs.upload, &dirs.tmp.join("thumbnails"),
        ] {
            std::fs::create_dir_all(d).unwrap();
        }
        (dirs, tmp)
    }

    async fn insert_queue_test_post(
        pool: &sqlx::PgPool,
        url: &str,
        uploaded_filename: &str,
    ) -> i32 {
        sqlx::query(
            "INSERT INTO users (name, email, password) \
             VALUES ('test_worker_q', 'test_worker_q@test.local', 'X') \
             ON CONFLICT (name) DO NOTHING",
        )
        .execute(pool)
        .await
        .expect("ensure test user");

        let row: (i32,) = sqlx::query_as(
            r#"
            INSERT INTO posts
                (url, uploaded_filename, filename, thumbnail_filename,
                 content_type, user_name, dirty, released)
            VALUES ($1, $2, 'pending.avif', 'pending_thumb.avif',
                    'image', 'test_worker_q', TRUE, FALSE)
            RETURNING id
            "#,
        )
        .bind(url)
        .bind(uploaded_filename)
        .fetch_one(pool)
        .await
        .expect("insert test dirty post");
        row.0
    }

    async fn cleanup_queue_test_post(pool: &sqlx::PgPool, id: i32) {
        sqlx::query("DELETE FROM posts WHERE id = $1")
            .bind(id)
            .execute(pool)
            .await
            .ok();
    }

    /// Full image pipeline: place a real JPEG, call process_image_post, verify DB finalization.
    #[tokio::test]
    #[ignore] // requires Postgres at localhost:5432
    async fn test_process_image_post_e2e() {
        let pool = queue_test_pool().await;
        let redis_client = queue_test_redis();
        let (dirs, _tmp) = make_queue_dirs();

        // Write a synthetic JPEG to the upload directory.
        let img_path = dirs.upload.join("upload_e2e.jpg");
        let img = image::DynamicImage::ImageRgb8(
            image::ImageBuffer::from_pixel(256u32, 256u32, image::Rgb([100u8, 150, 200]))
        );
        img.save(&img_path).unwrap();

        let unique_url = format!("https://test-queue/{}", uuid::Uuid::new_v4());
        let id = insert_queue_test_post(
            &pool, &unique_url, img_path.to_str().unwrap(),
        ).await;

        let post = crate::db::DirtyPost {
            id,
            url: unique_url,
            uploaded_filename: img_path.to_string_lossy().to_string(),
            user_name: "test_worker_q".to_string(),
            filter: "sfw".to_string(),
            released: false,
        };

        process_image_post(&pool, &redis_client, &dirs, &Arc::new(DashMap::new()), &post, &img_path)
            .await
            .expect("process_image_post must succeed");

        // Verify finalization.
        let row: (String, bool, i32, i32) = sqlx::query_as(
            "SELECT filename, dirty, width, height FROM posts WHERE id = $1",
        )
        .bind(id)
        .fetch_one(&pool)
        .await
        .expect("fetch finalized post");

        assert!(!row.0.is_empty(), "filename must be set after finalize");
        assert!(!row.1, "dirty flag must be false after successful processing");
        assert!(row.2 > 0, "width must be > 0");
        assert!(row.3 > 0, "height must be > 0");
        assert!(
            dirs.image.join(&row.0).exists(),
            "AVIF output file must exist in image dir"
        );

        cleanup_queue_test_post(&pool, id).await;
    }

    /// Verify publish_status writes a well-formed JSON status to Redis.
    #[tokio::test]
    #[ignore] // requires Redis at localhost:6379
    async fn test_publish_status_writes_redis() {
        let redis_client = queue_test_redis();
        let state = Arc::new(QueueState::new());
        state.pending.store(5, Ordering::Relaxed);
        state.total.store(10, Ordering::Relaxed);
        state.running.store(true, Ordering::Relaxed);

        publish_status(&redis_client, &state)
            .await
            .expect("publish_status must succeed");

        let mut conn = redis_client
            .get_multiplexed_async_connection()
            .await
            .expect("redis connection");
        let val: Option<String> = redis::cmd("GET")
            .arg(REDIS_STATUS_KEY)
            .query_async(&mut conn)
            .await
            .expect("GET status key");

        let val = val.expect("status key must be set after publish_status");
        let json: serde_json::Value = serde_json::from_str(&val).expect("valid JSON");

        assert_eq!(json["pending"], 5, "pending counter mismatch");
        assert_eq!(json["total"], 10, "total counter mismatch");
        assert_eq!(json["running"], true, "running flag mismatch");
        assert!(json["active_posts"].is_array(), "active_posts must be a JSON array");
    }
}
