-- name: GetTags :many
SELECT * FROM tags ORDER BY id;

-- name: GetTag :one
SELECT * FROM tags WHERE id = $1 LIMIT 1;

-- name: GetTagByName :one
SELECT * FROM tags WHERE name = $1 LIMIT 1;

-- name: CreateTag :one
INSERT INTO tags (name)
VALUES ($1)
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING *;

-- name: DeleteTag :exec
DELETE FROM tags WHERE id = $1;

-- name: DeleteTagByName :exec
DELETE FROM tags WHERE name = $1;

-- name: CountTags :one
SELECT COUNT(*)::int AS count
FROM tags;
