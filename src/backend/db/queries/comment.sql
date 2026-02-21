-- name: GetComments :many
SELECT *
FROM comments
WHERE deleted_at IS NULL
ORDER BY id;

-- name: GetComment :one
SELECT *
FROM comments
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetCommentsByPost :many
SELECT *
FROM comments
WHERE post_id = $1 AND deleted_at IS NULL
ORDER BY id;

-- name: CreateComment :one
INSERT INTO comments (content, user_name, post_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: DeleteComment :exec
UPDATE comments
SET deleted_at = NOW()
WHERE id = $1;

-- name: CountComments :one
SELECT COUNT(*)::int AS count
FROM comments
WHERE deleted_at IS NULL;

-- name: GetVotedComments :many
SELECT c.*, COALESCE(cv.vote, 0)::smallint AS vote
FROM comments c
LEFT JOIN comment_votes cv ON cv.comment_id = c.id AND cv.user_id = $1
WHERE c.deleted_at IS NULL AND c.post_id = $2
ORDER BY c.id;

-- name: GetVotedComment :one
SELECT c.*, COALESCE(cv.vote, 0)::smallint AS vote
FROM comments c
LEFT JOIN comment_votes cv ON cv.comment_id = c.id AND cv.user_id = $1
WHERE c.deleted_at IS NULL AND c.id = $2;
