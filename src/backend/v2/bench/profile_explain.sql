\set ON_ERROR_STOP on

EXPLAIN (ANALYZE, BUFFERS)
SELECT id, username, created_at
FROM users
WHERE id = 500 AND status = 0;

EXPLAIN (ANALYZE, BUFFERS)
SELECT p.id,
       p.author_user_id,
       p.content_filter,
       p.score,
       0::smallint AS user_vote,
       p.created_at,
       m.kind,
       m.storage_key,
       m.mime_type,
       m.width,
       m.height,
       m.duration_ms
FROM posts p
JOIN LATERAL (
    SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM media m
    WHERE m.post_id = p.id AND m.processing_state = 1
    LIMIT 1
) m ON true
WHERE p.author_user_id = 500
  AND p.release_state = 1
  AND p.deleted_at IS NULL
  AND p.content_filter = 0
ORDER BY p.id DESC
LIMIT 34;

EXPLAIN (ANALYZE, BUFFERS)
SELECT p.id,
       p.author_user_id,
       p.content_filter,
       p.score,
       0::smallint AS user_vote,
       p.created_at,
       m.kind,
       m.storage_key,
       m.mime_type,
       m.width,
       m.height,
       m.duration_ms
FROM posts p
JOIN LATERAL (
    SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM media m
    WHERE m.post_id = p.id AND m.processing_state = 1
    LIMIT 1
) m ON true
WHERE p.author_user_id = 500
  AND p.release_state = 1
  AND p.deleted_at IS NULL
  AND p.content_filter = 0
  AND p.id < 50000
ORDER BY p.id DESC
LIMIT 34;
