\set ON_ERROR_STOP on
\pset pager off

\echo '--- authenticated first feed page with viewer vote ---'
EXPLAIN (ANALYZE, BUFFERS)
SELECT p.id, p.author_user_id, p.content_filter, p.score,
       COALESCE(pv.value, 0)::smallint AS user_vote,
       p.created_at,
       m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
FROM posts p
JOIN LATERAL (
    SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM media m
    WHERE m.post_id = p.id AND m.processing_state = 1
    LIMIT 1
) m ON true
LEFT JOIN post_votes pv ON pv.post_id = p.id AND pv.user_id = 1000
WHERE p.release_state = 1
  AND p.deleted_at IS NULL
  AND p.content_filter IN (0)
ORDER BY p.id DESC
LIMIT 61;

\echo '--- authenticated old cursor feed page with viewer vote ---'
EXPLAIN (ANALYZE, BUFFERS)
SELECT p.id, p.author_user_id, p.content_filter, p.score,
       COALESCE(pv.value, 0)::smallint AS user_vote,
       p.created_at,
       m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
FROM posts p
JOIN LATERAL (
    SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM media m
    WHERE m.post_id = p.id AND m.processing_state = 1
    LIMIT 1
) m ON true
LEFT JOIN post_votes pv ON pv.post_id = p.id AND pv.user_id = 1000
WHERE p.release_state = 1
  AND p.deleted_at IS NULL
  AND p.content_filter IN (0)
  AND p.id < 50000
ORDER BY p.id DESC
LIMIT 61;

\echo '--- authenticated around post 49999 with viewer vote ---'
EXPLAIN (ANALYZE, BUFFERS)
WITH newer AS (
    SELECT p.id, p.author_user_id, p.content_filter, p.score,
           COALESCE(pv.value, 0)::smallint AS user_vote,
           p.created_at,
           m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM posts p
    JOIN LATERAL (
        SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
        FROM media m
        WHERE m.post_id = p.id AND m.processing_state = 1
        LIMIT 1
    ) m ON true
    LEFT JOIN post_votes pv ON pv.post_id = p.id AND pv.user_id = 1000
    WHERE p.release_state = 1
      AND p.deleted_at IS NULL
      AND p.id > 49999
      AND p.content_filter IN (0)
    ORDER BY p.id ASC
    LIMIT 30
), selected AS (
    SELECT p.id, p.author_user_id, p.content_filter, p.score,
           COALESCE(pv.value, 0)::smallint AS user_vote,
           p.created_at,
           m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM posts p
    JOIN LATERAL (
        SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
        FROM media m
        WHERE m.post_id = p.id AND m.processing_state = 1
        LIMIT 1
    ) m ON true
    LEFT JOIN post_votes pv ON pv.post_id = p.id AND pv.user_id = 1000
    WHERE p.release_state = 1
      AND p.deleted_at IS NULL
      AND p.id = 49999
      AND p.content_filter IN (0)
), older AS (
    SELECT p.id, p.author_user_id, p.content_filter, p.score,
           COALESCE(pv.value, 0)::smallint AS user_vote,
           p.created_at,
           m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM posts p
    JOIN LATERAL (
        SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
        FROM media m
        WHERE m.post_id = p.id AND m.processing_state = 1
        LIMIT 1
    ) m ON true
    LEFT JOIN post_votes pv ON pv.post_id = p.id AND pv.user_id = 1000
    WHERE p.release_state = 1
      AND p.deleted_at IS NULL
      AND p.id < 49999
      AND p.content_filter IN (0)
    ORDER BY p.id DESC
    LIMIT 30
)
SELECT * FROM (
    SELECT * FROM newer
    UNION ALL
    SELECT * FROM selected
    UNION ALL
    SELECT * FROM older
) combined_posts
ORDER BY id DESC;

\echo '--- post-vote point lookups and writes (rolled back) ---'
BEGIN;
EXPLAIN (ANALYZE, BUFFERS)
SELECT score
FROM posts
WHERE id = 50000
  AND release_state = 1
  AND deleted_at IS NULL
FOR UPDATE;

EXPLAIN (ANALYZE, BUFFERS)
SELECT value
FROM post_votes
WHERE post_id = 50000 AND user_id = 1000;

EXPLAIN (ANALYZE, BUFFERS)
INSERT INTO post_votes (post_id, user_id, value)
VALUES (50000, 1000, 1)
ON CONFLICT (post_id, user_id) DO UPDATE
SET value = EXCLUDED.value, updated_at = now();

EXPLAIN (ANALYZE, BUFFERS)
UPDATE posts
SET score = score
WHERE id = 50000
RETURNING score;
ROLLBACK;
