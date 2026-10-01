\set ON_ERROR_STOP on
\pset pager off

\echo '--- first feed page ---'
EXPLAIN (ANALYZE, BUFFERS)
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
  AND p.content_filter IN (0)
ORDER BY p.id DESC
LIMIT 61;

\echo '--- old cursor feed page ---'
EXPLAIN (ANALYZE, BUFFERS)
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
  AND p.content_filter IN (0)
  AND p.id < 50000
ORDER BY p.id DESC
LIMIT 61;

\echo '--- required tag + score ---'
EXPLAIN (ANALYZE, BUFFERS)
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
  AND p.content_filter IN (0)
  AND p.id < 50000
  AND p.id IN (
      SELECT pt.post_id
      FROM post_tags pt
      JOIN tags t ON t.id = pt.tag_id
      WHERE pt.removed_at IS NULL
        AND t.normalized_name = 'tag-42'
  )
  AND p.score >= 100
ORDER BY p.id DESC
LIMIT 61;

\echo '--- required + excluded tag + score ---'
EXPLAIN (ANALYZE, BUFFERS)
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
  AND p.content_filter IN (0)
  AND p.id < 50000
  AND p.id IN (
      SELECT pt.post_id
      FROM post_tags pt
      JOIN tags t ON t.id = pt.tag_id
      WHERE pt.removed_at IS NULL
        AND t.normalized_name = 'tag-42'
  )
  AND NOT EXISTS (
      SELECT 1
      FROM post_tags pt
      JOIN tags t ON t.id = pt.tag_id
      WHERE pt.post_id = p.id
        AND pt.removed_at IS NULL
        AND t.normalized_name = ANY(ARRAY['tag-77']::text[])
  )
  AND p.score >= 100
ORDER BY p.id DESC
LIMIT 61;

\echo '--- around post 50000 ---'
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
      AND p.id > 50000
      AND p.content_filter IN (0)
    ORDER BY p.id ASC
    LIMIT 30
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
      AND p.id <= 50000
      AND p.content_filter IN (0)
    ORDER BY p.id DESC
    LIMIT 31
)
SELECT * FROM (
    SELECT * FROM newer
    UNION ALL
    SELECT * FROM older
) combined_posts
ORDER BY id DESC;
