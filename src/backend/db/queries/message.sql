-- name: CreateMessage :one
INSERT INTO messages (kind, from_name, to_name, subject, body, ref_post_id, ref_comment_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetUnreadCount :one
SELECT COUNT(*)::int AS count
FROM messages
WHERE to_name = $1 AND read_at IS NULL;

-- name: GetNotificationsEnriched :many
SELECT
    m.id, m.kind, m.from_name, m.to_name, m.subject, m.body,
    m.read_at, m.created_at, m.ref_post_id, m.ref_comment_id,
    p.thumbnail_filename        AS ref_post_thumbnail,
    c.user_name                 AS ref_comment_user_name,
    c.score                     AS ref_comment_score,
    c.created_at                AS ref_comment_created_at,
    c.content                   AS ref_comment_content
FROM messages m
LEFT JOIN posts    p ON p.id = m.ref_post_id
LEFT JOIN comments c ON c.id = m.ref_comment_id
WHERE m.to_name = $1 AND m.kind != 'private'
ORDER BY m.created_at DESC
LIMIT $2 OFFSET $3;

-- name: GetPrivateMessages :many
SELECT *
FROM messages
WHERE (from_name = $1 OR to_name = $1) AND kind = 'private'
ORDER BY created_at DESC;

-- name: GetThread :many
SELECT *
FROM messages
WHERE ((from_name = $1 AND to_name = $2) OR (from_name = $2 AND to_name = $1))
  AND kind = 'private'
ORDER BY created_at ASC;

-- name: MarkMessageRead :exec
UPDATE messages
SET read_at = NOW()
WHERE id = $1 AND to_name = $2 AND read_at IS NULL;

-- name: MarkAllReadForUser :exec
UPDATE messages
SET read_at = NOW()
WHERE to_name = $1 AND read_at IS NULL;
