\set ON_ERROR_STOP on
\pset pager off

\echo '--- around visible post 49999 ---'
EXPLAIN (ANALYZE, BUFFERS)
WITH newer AS (
    SELECT p.id, p.author_user_id, p.content_filter, p.score, p.created_at,
           m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM posts p
    JOIN LATERAL (
        SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
        FROM media m
        WHERE m.post_id = p.id AND m.processing_state = 1
        LIMIT 1
    ) m ON true
    WHERE p.release_state = 1
      AND p.deleted_at IS NULL
      AND p.id > 49999
      AND p.content_filter IN (0)
    ORDER BY p.id ASC
    LIMIT 30
), selected AS (
    SELECT p.id, p.author_user_id, p.content_filter, p.score, p.created_at,
           m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM posts p
    JOIN LATERAL (
        SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
        FROM media m
        WHERE m.post_id = p.id AND m.processing_state = 1
        LIMIT 1
    ) m ON true
    WHERE p.release_state = 1
      AND p.deleted_at IS NULL
      AND p.id = 49999
      AND p.content_filter IN (0)
), older AS (
    SELECT p.id, p.author_user_id, p.content_filter, p.score, p.created_at,
           m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM posts p
    JOIN LATERAL (
        SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
        FROM media m
        WHERE m.post_id = p.id AND m.processing_state = 1
        LIMIT 1
    ) m ON true
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
