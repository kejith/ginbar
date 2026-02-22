-- name: UpdatePostFilter :exec
UPDATE posts SET filter = sqlc.arg(filter) WHERE id = sqlc.arg(id);

-- name: GetPosts :many
SELECT *
FROM posts
WHERE deleted_at IS NULL AND dirty = FALSE AND filter = ANY(sqlc.arg(filters)::text[])
ORDER BY id DESC
LIMIT $2 OFFSET $3;

-- name: GetOlderPosts :many
SELECT *
FROM posts
WHERE deleted_at IS NULL AND dirty = FALSE AND id < $1 AND filter = ANY(sqlc.arg(filters)::text[])
ORDER BY id DESC
LIMIT $3;

-- name: GetNewerPosts :many
SELECT *
FROM posts
WHERE deleted_at IS NULL AND dirty = FALSE AND id > $1 AND filter = ANY(sqlc.arg(filters)::text[])
ORDER BY id
LIMIT $3;

-- name: GetPost :one
SELECT *
FROM posts
WHERE id = $1 AND deleted_at IS NULL AND dirty = FALSE AND filter = ANY(sqlc.arg(filters)::text[]);

-- name: GetVotedPosts :many
SELECT p.*, COALESCE(pv.vote, 0)::smallint AS vote
FROM posts p
LEFT JOIN post_votes pv ON pv.post_id = p.id AND pv.user_id = $1
WHERE p.deleted_at IS NULL AND p.dirty = FALSE AND p.filter = ANY(sqlc.arg(filters)::text[])
ORDER BY p.id DESC
LIMIT $3 OFFSET $4;

-- name: GetVotedPost :one
SELECT p.*, COALESCE(pv.vote, 0)::smallint AS vote
FROM posts p
LEFT JOIN post_votes pv ON pv.post_id = p.id AND pv.user_id = $1
WHERE p.deleted_at IS NULL AND p.dirty = FALSE AND p.id = $2 AND p.filter = ANY(sqlc.arg(filters)::text[]);

-- name: Search :many
SELECT DISTINCT p.*
FROM posts p
JOIN post_tags pt ON pt.post_id = p.id
JOIN tags t ON t.id = pt.tag_id
WHERE t.name = ANY($1::text[]) AND p.deleted_at IS NULL AND p.dirty = FALSE AND p.filter = ANY(sqlc.arg(filters)::text[])
ORDER BY p.id DESC;

-- name: SearchByUser :many
SELECT DISTINCT p.*
FROM posts p
JOIN post_tags pt ON pt.post_id = p.id
JOIN tags t ON t.id = pt.tag_id
WHERE t.name = ANY(sqlc.arg(tags)::text[]) AND p.user_name = sqlc.arg(user_name) AND p.deleted_at IS NULL AND p.dirty = FALSE AND p.filter = ANY(sqlc.arg(filters)::text[])
ORDER BY p.id DESC;

-- name: GetPostsByUser :many
SELECT *
FROM posts
WHERE user_name = $1 AND deleted_at IS NULL AND dirty = FALSE AND filter = ANY(sqlc.arg(filters)::text[])
ORDER BY id DESC;

-- name: CreatePost :one
INSERT INTO posts (
    url,
    filename,
    thumbnail_filename,
    user_name,
    content_type,
    p_hash_0,
    p_hash_1,
    p_hash_2,
    p_hash_3,
    uploaded_filename,
    width,
    height,
    filter
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING *;

-- name: UpdatePostFiles :exec
UPDATE posts
SET filename = $1, thumbnail_filename = $2
WHERE id = $3;

-- name: UpdatePostHashes :exec
UPDATE posts
SET p_hash_0 = $1, p_hash_1 = $2, p_hash_2 = $3, p_hash_3 = $4
WHERE id = $5;

-- name: DeletePost :exec
UPDATE posts
SET deleted_at = NOW()
WHERE id = $1;

-- name: CountPosts :one
SELECT COUNT(*)::int AS count
FROM posts
WHERE deleted_at IS NULL AND dirty = FALSE;

-- name: CountDirtyPosts :one
SELECT COUNT(*)::int AS count
FROM posts
WHERE deleted_at IS NULL AND dirty = TRUE;

-- name: GetPostAdmin :one
SELECT * FROM posts WHERE id = $1 AND deleted_at IS NULL;

-- name: PostURLExists :one
SELECT EXISTS(
    SELECT 1 FROM posts WHERE url = $1 AND deleted_at IS NULL
) AS exists;

-- name: GetPossibleDuplicatePosts :many
SELECT * FROM (
    SELECT *,
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
ORDER BY hamming_distance;
