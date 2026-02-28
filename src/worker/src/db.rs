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
