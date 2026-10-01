\set ON_ERROR_STOP on
\pset pager off

\echo '=== baseline first feed page ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT p.id, p.author_user_id, p.content_filter, p.score, p.created_at,
       m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
FROM posts p
JOIN media m ON m.post_id = p.id AND m.processing_state = 1
WHERE p.release_state = 1
  AND p.deleted_at IS NULL
  AND p.content_filter IN (0)
ORDER BY p.id DESC
LIMIT 61;

\echo '=== tuned first feed page ==='
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

\echo '=== baseline old cursor ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT p.id, p.author_user_id, p.content_filter, p.score, p.created_at,
       m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
FROM posts p
JOIN media m ON m.post_id = p.id AND m.processing_state = 1
WHERE p.release_state = 1
  AND p.deleted_at IS NULL
  AND p.content_filter IN (0)
  AND p.id < 50000
ORDER BY p.id DESC
LIMIT 61;

\echo '=== tuned old cursor ==='
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

\echo '=== baseline required tag + score ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT p.id, p.author_user_id, p.content_filter, p.score, p.created_at,
       m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
FROM posts p
JOIN media m ON m.post_id = p.id AND m.processing_state = 1
WHERE p.release_state = 1
  AND p.deleted_at IS NULL
  AND p.content_filter IN (0)
  AND p.id < 50000
  AND EXISTS (
      SELECT 1
      FROM post_tags pt
      JOIN tags t ON t.id = pt.tag_id
      WHERE pt.post_id = p.id
        AND pt.removed_at IS NULL
        AND t.normalized_name = 'tag-42'
  )
  AND p.score >= 100
ORDER BY p.id DESC
LIMIT 61;

\echo '=== tuned required tag + score ==='
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

\echo '=== baseline required + excluded tag + score ==='
EXPLAIN (ANALYZE, BUFFERS)
SELECT p.id, p.author_user_id, p.content_filter, p.score, p.created_at,
       m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
FROM posts p
JOIN media m ON m.post_id = p.id AND m.processing_state = 1
WHERE p.release_state = 1
  AND p.deleted_at IS NULL
  AND p.content_filter IN (0)
  AND p.id < 50000
  AND EXISTS (
      SELECT 1
      FROM post_tags pt
      JOIN tags t ON t.id = pt.tag_id
      WHERE pt.post_id = p.id
        AND pt.removed_at IS NULL
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

\echo '=== tuned required + excluded tag + score ==='
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
