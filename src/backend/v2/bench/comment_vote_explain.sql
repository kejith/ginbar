\set ON_ERROR_STOP on
\pset pager off

\echo '--- signed-out bounded comment page ---'
EXPLAIN (ANALYZE, BUFFERS)
SELECT
    COALESCE(c.id, 0),
    COALESCE(c.user_id, 0),
    c.parent_comment_id,
    COALESCE(c.body, ''),
    COALESCE(c.score, 0),
    0::smallint,
    COALESCE(c.created_at, 'epoch'::timestamptz),
    c.deleted_at IS NOT NULL
FROM posts AS p
LEFT JOIN LATERAL (
    SELECT id, user_id, parent_comment_id, body, score, created_at, deleted_at
    FROM comments
    WHERE post_id = p.id
      AND id > 0
    ORDER BY id ASC
    LIMIT 101
) AS c ON true
WHERE p.id = 50000
  AND p.release_state = 1
  AND p.deleted_at IS NULL
ORDER BY c.id ASC NULLS LAST;

\echo '--- authenticated bounded comment page with viewer vote ---'
EXPLAIN (ANALYZE, BUFFERS)
SELECT
    COALESCE(c.id, 0),
    COALESCE(c.user_id, 0),
    c.parent_comment_id,
    COALESCE(c.body, ''),
    COALESCE(c.score, 0),
    CASE WHEN c.deleted_at IS NULL THEN COALESCE(cv.value, 0) ELSE 0 END::smallint,
    COALESCE(c.created_at, 'epoch'::timestamptz),
    c.deleted_at IS NOT NULL
FROM posts AS p
LEFT JOIN LATERAL (
    SELECT id, user_id, parent_comment_id, body, score, created_at, deleted_at
    FROM comments
    WHERE post_id = p.id
      AND id > 0
    ORDER BY id ASC
    LIMIT 101
) AS c ON true
LEFT JOIN comment_votes AS cv
  ON c.deleted_at IS NULL
 AND cv.comment_id = c.id
 AND cv.user_id = 1000
WHERE p.id = 50000
  AND p.release_state = 1
  AND p.deleted_at IS NULL
ORDER BY c.id ASC NULLS LAST;

\echo '--- comment-vote point lookups and writes (rolled back) ---'
BEGIN;
EXPLAIN (ANALYZE, BUFFERS)
WITH target_post AS MATERIALIZED (
    SELECT id
    FROM posts
    WHERE id = 50000
      AND release_state = 1
      AND deleted_at IS NULL
    FOR SHARE
)
SELECT c.score
FROM target_post AS p
JOIN comments AS c ON c.post_id = p.id
WHERE c.id = 50000
  AND c.deleted_at IS NULL
FOR UPDATE OF c;

EXPLAIN (ANALYZE, BUFFERS)
SELECT value
FROM comment_votes
WHERE comment_id = 50000 AND user_id = 1000;

EXPLAIN (ANALYZE, BUFFERS)
DELETE FROM comment_votes
WHERE comment_id = 50000 AND user_id = 1000;

EXPLAIN (ANALYZE, BUFFERS)
INSERT INTO comment_votes (comment_id, user_id, value)
VALUES (50000, 1000, 1)
ON CONFLICT (comment_id, user_id) DO UPDATE
SET value = EXCLUDED.value, updated_at = now();

EXPLAIN (ANALYZE, BUFFERS)
UPDATE comments
SET score = score
WHERE id = 50000
RETURNING score;
ROLLBACK;
