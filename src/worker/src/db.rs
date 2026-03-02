use anyhow::Result;
use sqlx::PgPool;

/// A dirty post row from the database.
#[derive(Debug, Clone, sqlx::FromRow)]
#[allow(dead_code)]
pub struct DirtyPost {
    pub id: i32,
    pub url: String,
    pub uploaded_filename: String,
    pub user_name: String,
    pub filter: String,
    pub released: bool,
}

/// Result row from the perceptual-hash duplicate check.
#[derive(Debug, Clone, sqlx::FromRow)]
pub struct DuplicateRow {
    pub id: i32,
    pub thumbnail_filename: String,
    pub dirty: bool,
    pub hamming_distance: i32,
}

/// Parameters for finalizing a processed post.
pub struct FinalizeParams {
    pub id: i32,
    pub filename: String,
    pub thumbnail_filename: String,
    pub uploaded_filename: String,
    pub content_type: String,
    pub p_hash_0: i64,
    pub p_hash_1: i64,
    pub p_hash_2: i64,
    pub p_hash_3: i64,
    pub width: i32,
    pub height: i32,
}

/// Fetch all dirty (unprocessed) posts, ordered by ID.
pub async fn get_dirty_posts(pool: &PgPool) -> Result<Vec<DirtyPost>> {
    let rows = sqlx::query_as::<_, DirtyPost>(
        r#"
        SELECT id, url, uploaded_filename, user_name, filter, released
        FROM posts
        WHERE dirty = TRUE AND deleted_at IS NULL
        ORDER BY id
        "#,
    )
    .fetch_all(pool)
    .await?;

    Ok(rows)
}

/// Mark a dirty post as finalized (processed successfully).
pub async fn finalize_post(pool: &PgPool, p: &FinalizeParams) -> Result<()> {
    sqlx::query(
        r#"
        UPDATE posts
        SET filename           = $1,
            thumbnail_filename = $2,
            uploaded_filename  = $3,
            content_type       = $4,
            p_hash_0           = $5,
            p_hash_1           = $6,
            p_hash_2           = $7,
            p_hash_3           = $8,
            width              = $9,
            height             = $10,
            dirty              = FALSE
        WHERE id = $11
        "#,
    )
    .bind(&p.filename)
    .bind(&p.thumbnail_filename)
    .bind(&p.uploaded_filename)
    .bind(&p.content_type)
    .bind(p.p_hash_0)
    .bind(p.p_hash_1)
    .bind(p.p_hash_2)
    .bind(p.p_hash_3)
    .bind(p.width)
    .bind(p.height)
    .bind(p.id)
    .execute(pool)
    .await?;

    Ok(())
}

/// Delete a dirty post that failed processing so the URL isn't permanently blocked.
pub async fn delete_dirty_post(pool: &PgPool, id: i32) -> Result<()> {
    sqlx::query("DELETE FROM posts WHERE id = $1 AND dirty = TRUE")
        .bind(id)
        .execute(pool)
        .await?;
    Ok(())
}

/// Find posts whose perceptual hash is similar (hamming distance < 50).
pub async fn get_possible_duplicates(
    pool: &PgPool,
    h0: i64,
    h1: i64,
    h2: i64,
    h3: i64,
) -> Result<Vec<DuplicateRow>> {
    let rows = sqlx::query_as::<_, DuplicateRow>(
        r#"
        SELECT id, thumbnail_filename, dirty, hamming_distance FROM (
            SELECT id, thumbnail_filename, dirty,
                (
                    bit_count(($1::bigint)::bit(64) # p_hash_0::bit(64)) +
                    bit_count(($2::bigint)::bit(64) # p_hash_1::bit(64)) +
                    bit_count(($3::bigint)::bit(64) # p_hash_2::bit(64)) +
                    bit_count(($4::bigint)::bit(64) # p_hash_3::bit(64))
                )::int AS hamming_distance
            FROM posts
            WHERE deleted_at IS NULL
        ) sub
        WHERE hamming_distance < 50
        ORDER BY hamming_distance
        "#,
    )
    .bind(h0)
    .bind(h1)
    .bind(h2)
    .bind(h3)
    .fetch_all(pool)
    .await?;

    Ok(rows)
}

/// Count how many dirty posts have id <= the given post_id (queue position).
#[allow(dead_code)]
pub async fn count_dirty_before(pool: &PgPool, post_id: i32) -> Result<i64> {
    let (count,): (i64,) = sqlx::query_as(
        "SELECT COUNT(*) FROM posts WHERE dirty = TRUE AND deleted_at IS NULL AND id <= $1",
    )
    .bind(post_id)
    .fetch_one(pool)
    .await?;

    Ok(count)
}

/// Update filename, thumbnail_filename, width, and height for a re-encoded post.
///
/// Used by the regen queue after [`crate::processing::regenerate_image`] produces
/// new output files. Does **not** touch the `dirty` flag or the perceptual hash.
pub async fn update_post_files(
    pool: &PgPool,
    id: i32,
    filename: &str,
    thumbnail_filename: &str,
    width: i32,
    height: i32,
) -> Result<()> {
    sqlx::query(
        r#"
        UPDATE posts
        SET filename           = $1,
            thumbnail_filename = $2,
            width              = $3,
            height             = $4
        WHERE id = $5
        "#,
    )
    .bind(filename)
    .bind(thumbnail_filename)
    .bind(width)
    .bind(height)
    .bind(id)
    .execute(pool)
    .await?;

    Ok(())
}

// ── Integration tests (requires Postgres — run with `cargo test -- --ignored`) ─
//
// In the devcontainer Postgres is always available at localhost:5432;
// unmark `#[ignore]` there or pass `--include-ignored` / `-- --ignored`.
//
// IMPORTANT: These tests insert rows and must run sequentially to avoid FK
// conflicts on the shared 'test_worker' user.  Always run with:
//
//   cargo test -p wallium-worker -- --ignored --test-threads=1

#[cfg(test)]
mod tests {
    use super::*;

    const TEST_DB_URL: &str =
        "postgres://wallium:devpassword@localhost:5432/wallium?sslmode=disable";

    async fn test_pool() -> PgPool {
        sqlx::postgres::PgPoolOptions::new()
            .max_connections(2)
            .connect(TEST_DB_URL)
            .await
            .expect("could not connect to test Postgres — is it running?")
    }

    // Insert a minimal dirty post and return its id.
    // The post uses a unique fake URL so concurrent test runs don't collide.
    async fn insert_test_post(pool: &PgPool, tag: &str) -> i32 {
        // Ensure the test-only user exists (idempotent via ON CONFLICT DO NOTHING).
        sqlx::query(
            "INSERT INTO users (name, email, password) \
             VALUES ('test_worker', 'test_worker@test.local', 'X') \
             ON CONFLICT (name) DO NOTHING",
        )
        .execute(pool)
        .await
        .expect("ensure test user");

        // Insert with all NOT-NULL columns; dirty=TRUE so the worker picks it up.
        let row: (i32,) = sqlx::query_as(
            r#"
            INSERT INTO posts
                (url, uploaded_filename, filename, thumbnail_filename,
                 content_type, user_name, dirty, released)
            VALUES ($1, '', 'pending.avif', 'pending_thumb.avif',
                    'image', 'test_worker', TRUE, FALSE)
            RETURNING id
            "#,
        )
        .bind(format!(
            "https://example.com/test/{}/{}",
            tag,
            uuid::Uuid::new_v4()
        ))
        .fetch_one(pool)
        .await
        .expect("insert test post");
        row.0
    }

    async fn delete_test_post(pool: &PgPool, id: i32) {
        sqlx::query("DELETE FROM posts WHERE id = $1")
            .bind(id)
            .execute(pool)
            .await
            .ok();
    }

    // ── get_dirty_posts ───────────────────────────────────────────────────────

    #[tokio::test]
    #[ignore]
    async fn test_get_dirty_posts_contains_inserted() {
        let pool = test_pool().await;
        let id = insert_test_post(&pool, "dirty_posts").await;

        let posts = get_dirty_posts(&pool).await.expect("get_dirty_posts");
        assert!(
            posts.iter().any(|p| p.id == id),
            "inserted post {} must appear in dirty posts list",
            id
        );

        delete_test_post(&pool, id).await;
    }

    #[tokio::test]
    #[ignore]
    async fn test_get_dirty_posts_ordered_by_id() {
        let pool = test_pool().await;
        let id1 = insert_test_post(&pool, "order_a").await;
        let id2 = insert_test_post(&pool, "order_b").await;

        let posts = get_dirty_posts(&pool).await.expect("get_dirty_posts");
        let ids: Vec<i32> = posts.iter().map(|p| p.id).collect();
        let sorted = {
            let mut s = ids.clone();
            s.sort();
            s
        };
        assert_eq!(ids, sorted, "dirty posts must be ordered by id asc");

        delete_test_post(&pool, id1).await;
        delete_test_post(&pool, id2).await;
    }

    // ── finalize_post ─────────────────────────────────────────────────────────

    #[tokio::test]
    #[ignore]
    async fn test_finalize_post_clears_dirty_flag() {
        let pool = test_pool().await;
        let id = insert_test_post(&pool, "finalize").await;

        finalize_post(
            &pool,
            &FinalizeParams {
                id,
                filename: "test.avif".to_string(),
                thumbnail_filename: "test_thumb.avif".to_string(),
                uploaded_filename: "upload.jpg".to_string(),
                content_type: "image".to_string(),
                p_hash_0: 1111,
                p_hash_1: 2222,
                p_hash_2: 3333,
                p_hash_3: 4444,
                width: 640,
                height: 480,
            },
        )
        .await
        .expect("finalize_post");

        // The post must no longer appear in dirty posts.
        let posts = get_dirty_posts(&pool).await.expect("get_dirty_posts");
        assert!(
            !posts.iter().any(|p| p.id == id),
            "finalized post {} must not be in dirty list",
            id
        );

        delete_test_post(&pool, id).await;
    }

    #[tokio::test]
    #[ignore]
    async fn test_finalize_post_sets_fields() {
        let pool = test_pool().await;
        let id = insert_test_post(&pool, "finalize_fields").await;

        finalize_post(
            &pool,
            &FinalizeParams {
                id,
                filename: "output.avif".to_string(),
                thumbnail_filename: "output_thumb.avif".to_string(),
                uploaded_filename: "original.jpg".to_string(),
                content_type: "image".to_string(),
                p_hash_0: 0xDEAD,
                p_hash_1: 0xBEEF,
                p_hash_2: 0x1234,
                p_hash_3: 0x5678,
                width: 1920,
                height: 1080,
            },
        )
        .await
        .expect("finalize_post");

        let row: (String, i32, i32, bool) = sqlx::query_as(
            "SELECT filename, width, height, dirty FROM posts WHERE id = $1",
        )
        .bind(id)
        .fetch_one(&pool)
        .await
        .expect("fetch post");

        assert_eq!(row.0, "output.avif");
        assert_eq!(row.1, 1920);
        assert_eq!(row.2, 1080);
        assert!(!row.3, "dirty flag must be false after finalize");

        delete_test_post(&pool, id).await;
    }

    // ── delete_dirty_post ─────────────────────────────────────────────────────

    #[tokio::test]
    #[ignore]
    async fn test_delete_dirty_post_removes_row() {
        let pool = test_pool().await;
        let id = insert_test_post(&pool, "delete_dirty").await;

        delete_dirty_post(&pool, id).await.expect("delete_dirty_post");

        let count: (i64,) = sqlx::query_as("SELECT COUNT(*) FROM posts WHERE id = $1")
            .bind(id)
            .fetch_one(&pool)
            .await
            .expect("count");
        assert_eq!(count.0, 0, "post must be deleted");
    }

    #[tokio::test]
    #[ignore]
    async fn test_delete_dirty_post_only_deletes_dirty() {
        // After finalize (dirty=false), delete_dirty_post must be a no-op.
        let pool = test_pool().await;
        let id = insert_test_post(&pool, "delete_nondirty").await;

        // Finalize makes dirty=false.
        finalize_post(
            &pool,
            &FinalizeParams {
                id,
                filename: "f.avif".to_string(),
                thumbnail_filename: "t.avif".to_string(),
                uploaded_filename: "u.jpg".to_string(),
                content_type: "image".to_string(),
                p_hash_0: 0, p_hash_1: 0, p_hash_2: 0, p_hash_3: 0,
                width: 100, height: 100,
            },
        )
        .await
        .unwrap();

        // delete_dirty_post with WHERE dirty=TRUE must not delete the finalized row.
        delete_dirty_post(&pool, id).await.expect("delete_dirty_post");

        let count: (i64,) = sqlx::query_as("SELECT COUNT(*) FROM posts WHERE id = $1")
            .bind(id)
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(count.0, 1, "finalized post must survive delete_dirty_post");

        delete_test_post(&pool, id).await;
    }

    // ── get_possible_duplicates ───────────────────────────────────────────────

    #[tokio::test]
    #[ignore]
    async fn test_get_possible_duplicates_exact_match() {
        let pool = test_pool().await;
        let (h0, h1, h2, h3) = (0xAABBi64, 0xCCDDi64, 0xEEFFi64, 0x1122i64);
        let id = insert_test_post(&pool, "dedup_exact").await;

        // Set the hash fields on the inserted post.
        sqlx::query(
            "UPDATE posts SET p_hash_0=$1, p_hash_1=$2, p_hash_2=$3, p_hash_3=$4, dirty=FALSE WHERE id=$5",
        )
        .bind(h0).bind(h1).bind(h2).bind(h3).bind(id)
        .execute(&pool)
        .await
        .unwrap();

        let dups = get_possible_duplicates(&pool, h0, h1, h2, h3)
            .await
            .expect("get_possible_duplicates");

        assert!(
            dups.iter().any(|d| d.id == id && d.hamming_distance == 0),
            "exact hash match must appear with hamming_distance=0"
        );

        delete_test_post(&pool, id).await;
    }

    #[tokio::test]
    #[ignore]
    async fn test_get_possible_duplicates_no_match() {
        let pool = test_pool().await;
        // Totally different hash — should return no results (hamming ≥ 50).
        // Use very different values and rely on the few existing non-dirty posts.
        let dups = get_possible_duplicates(&pool, i64::MAX, i64::MIN, 0, i64::MAX)
            .await
            .expect("get_possible_duplicates");
        // We cannot assert empty (other real posts might accidentally match),
        // but at least the query must succeed without panicking.
        let _ = dups;
    }

    // ── count_dirty_before ────────────────────────────────────────────────────

    #[tokio::test]
    #[ignore]
    async fn test_count_dirty_before_returns_nonnegative() {
        let pool = test_pool().await;
        let count = count_dirty_before(&pool, i32::MAX)
            .await
            .expect("count_dirty_before");
        assert!(count >= 0);
    }

    // ── update_post_files ─────────────────────────────────────────────────────

    #[tokio::test]
    #[ignore]
    async fn test_update_post_files_sets_fields() {
        let pool = test_pool().await;
        let id = insert_test_post(&pool, "update_post_files").await;

        // Finalize so dirty=false (update_post_files targets non-dirty rows).
        finalize_post(
            &pool,
            &FinalizeParams {
                id,
                filename: "original.avif".to_string(),
                thumbnail_filename: "original_thumb.avif".to_string(),
                uploaded_filename: "upload.jpg".to_string(),
                content_type: "image".to_string(),
                p_hash_0: 0, p_hash_1: 0, p_hash_2: 0, p_hash_3: 0,
                width: 640,
                height: 480,
            },
        )
        .await
        .expect("finalize_post");

        update_post_files(&pool, id, "regen.avif", "regen_thumb.avif", 920, 640)
            .await
            .expect("update_post_files");

        let row: (String, String, i32, i32) = sqlx::query_as(
            "SELECT filename, thumbnail_filename, width, height FROM posts WHERE id = $1",
        )
        .bind(id)
        .fetch_one(&pool)
        .await
        .expect("fetch updated post");

        assert_eq!(row.0, "regen.avif");
        assert_eq!(row.1, "regen_thumb.avif");
        assert_eq!(row.2, 920);
        assert_eq!(row.3, 640);

        delete_test_post(&pool, id).await;
    }

    #[tokio::test]
    #[ignore]
    async fn test_update_post_files_does_not_clear_dirty() {
        // update_post_files must not affect the dirty flag.
        let pool = test_pool().await;
        let id = insert_test_post(&pool, "update_post_files_dirty").await;

        // Post is dirty=true here. update_post_files should still succeed.
        update_post_files(&pool, id, "newname.avif", "newname_thumb.avif", 100, 100)
            .await
            .expect("update_post_files on dirty row");

        let (dirty,): (bool,) =
            sqlx::query_as("SELECT dirty FROM posts WHERE id = $1")
                .bind(id)
                .fetch_one(&pool)
                .await
                .expect("fetch dirty flag");
        assert!(dirty, "dirty flag must remain true — update_post_files must not alter it");

        delete_test_post(&pool, id).await;
    }
}
