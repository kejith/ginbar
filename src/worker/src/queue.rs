//! Processing queue: polls the database for dirty posts and processes them
//! concurrently, publishing status updates to Redis.
//!
//! The queue listens for wake-up notifications on a Redis Pub/Sub channel
//! (`wallium:queue:wake`) and also polls every N seconds as a fallback.
//!
//! Concurrency is controlled via `futures_util::StreamExt::for_each_concurrent`
//! which limits in-flight work naturally — no unbounded task spawning.

use anyhow::Result;
use futures_util::StreamExt;
use redis::AsyncCommands;
use sqlx::PgPool;
use std::path::Path;
use std::sync::atomic::{AtomicBool, AtomicI32, Ordering};
use std::sync::Arc;
use tracing::{info, warn};

use crate::config::Config;
use crate::db::{self, DirtyPost};
use crate::processing::Directories;

const PR0GRAMM_IMG_BASE: &str = "https://img.pr0gramm.com/";
const REDIS_CHANNEL: &str = "wallium:queue:wake";
const REDIS_STATUS_KEY: &str = "wallium:queue:status";

/// Shared counters for the current batch (all atomics, no mutex needed).
pub(crate) struct QueueState {
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
    http_client: reqwest::Client,
    cfg: &Config,
) -> Result<()> {
    dirs.ensure().await?;

    let state = Arc::new(QueueState::new());
    let poll_interval = std::time::Duration::from_secs(cfg.poll_interval_secs);
    let concurrency = cfg.concurrency;

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
        poll_secs = cfg.poll_interval_secs,
        "processing queue started"
    );

    let mut shutdown = Box::pin(tokio::signal::ctrl_c());
    let mut ticker = tokio::time::interval(poll_interval);

    // Drain on startup so posts from a previous run are picked up immediately.
    drain(&pool, &redis_client, &dirs, &http_client, &state, concurrency).await;

    loop {
        tokio::select! {
            _ = &mut shutdown => {
                info!("shutdown signal received, stopping queue");
                break;
            }
            _ = wake_rx.recv() => {
                // Drain any extra pending notifications.
                while wake_rx.try_recv().is_ok() {}
                drain(&pool, &redis_client, &dirs, &http_client, &state, concurrency).await;
            }
            _ = ticker.tick() => {
                drain(&pool, &redis_client, &dirs, &http_client, &state, concurrency).await;
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

/// Fetch all dirty posts and process them up to `concurrency` at a time.
///
/// Uses `for_each_concurrent(concurrency)` so:
/// - At most `concurrency` posts are in-flight simultaneously.
/// - No more than `len(dirty)` Tokio tasks are ever spawned.
/// - Counter updates (`pending`/`active`) happen at the correct moment.
async fn drain(
    pool: &PgPool,
    redis_client: &redis::Client,
    dirs: &Directories,
    http_client: &reqwest::Client,
    state: &Arc<QueueState>,
    concurrency: usize,
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

    futures_util::stream::iter(dirty)
        .for_each_concurrent(Some(concurrency), |post| {
            let pool = pool.clone();
            let dirs = dirs.clone();
            let state = state.clone();
            let redis_client = redis_client.clone();
            let http_client = http_client.clone();

            async move {
                // Counters updated BEFORE processing starts.
                state.pending.fetch_sub(1, Ordering::Relaxed);
                state.active.fetch_add(1, Ordering::Relaxed);

                let result = process_post(&pool, &redis_client, &dirs, &http_client, &post).await;

                state.active.fetch_sub(1, Ordering::Relaxed);
                state.processed.fetch_add(1, Ordering::Relaxed);

                match &result {
                    Ok(()) => {
                        state.imported.fetch_add(1, Ordering::Relaxed);
                    }
                    Err(e) => {
                        state.failed.fetch_add(1, Ordering::Relaxed);
                        // Build a single-line error chain so the compact tracing
                        // formatter shows all causes (not just the outermost one).
                        let err_chain = e
                            .chain()
                            .map(|c| c.to_string())
                            .collect::<Vec<_>>()
                            .join(": ");
                        warn!(post_id = post.id, err = err_chain, "post processing failed");
                    }
                }

                let _ = publish_status(&redis_client, &state).await;
            }
        })
        .await;

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
async fn process_post(
    pool: &PgPool,
    redis_client: &redis::Client,
    dirs: &Directories,
    http_client: &reqwest::Client,
    post: &DirtyPost,
) -> Result<()> {
    // Determine where the source file is.
    let (tmp_path, is_temp) = if !post.uploaded_filename.is_empty() {
        (std::path::PathBuf::from(&post.uploaded_filename), false)
    } else if !post.url.is_empty() {
        let path = if post.url.starts_with(PR0GRAMM_IMG_BASE) {
            crate::download::download_pr0gramm_file(http_client, &post.url, &dirs.tmp).await
        } else {
            crate::download::download_file(http_client, &post.url, &dirs.tmp).await
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

    let file_type = classify_file(&tmp_path);

    let result = match file_type {
        FileType::Image => {
            process_image_post(pool, redis_client, dirs, post, &tmp_path).await
        }
        FileType::Video(mime) => {
            process_video_post(pool, dirs, post, &tmp_path, &mime).await
        }
        FileType::Unknown(mime) => {
            let _ = db::delete_dirty_post(pool, post.id).await;
            anyhow::bail!("unsupported file type: {}", mime);
        }
    };

    if is_temp {
        let _ = tokio::fs::remove_file(&tmp_path).await;
    }

    // If processing failed (encode error, corrupt image, etc.) delete the
    // dirty post so it doesn't retry forever on every poll cycle.
    if result.is_err() {
        if let Err(e) = db::delete_dirty_post(pool, post.id).await {
            warn!(post_id = post.id, err = %e, "failed to delete dirty post after processing error");
        }
    }

    result
}

/// Process an image post: encode, hash, dedup, thumbnail, finalize.
pub(crate) async fn process_image_post(
    pool: &PgPool,
    redis_client: &redis::Client,
    dirs: &Directories,
    post: &DirtyPost,
    input: &Path,
) -> Result<()> {
    let res = crate::processing::process_image(input, dirs).await?;

    let [h0, h1, h2, h3] = res.p_hash;
    let dups = db::get_possible_duplicates(pool, h0, h1, h2, h3).await?;
    let real_dups: Vec<_> = dups
        .into_iter()
        .filter(|d| d.id != post.id && !d.dirty)
        .collect();

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
pub(crate) async fn process_video_post(
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

/// Publish current queue status to Redis.
pub(crate) async fn publish_status(redis_client: &redis::Client, state: &Arc<QueueState>) -> Result<()> {
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

        process_image_post(&pool, &redis_client, &dirs, &post, &img_path)
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
    }
}
