-- name: GetUsers :many
SELECT id, created_at, name, email
FROM users
WHERE deleted_at IS NULL
ORDER BY id;

-- name: GetUser :one
SELECT id, created_at, name, email
FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetUserByName :one
SELECT *
FROM users
WHERE name = $1 AND deleted_at IS NULL;

-- name: CreateUser :one
INSERT INTO users (name, email, password)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateUserEmail :exec
UPDATE users
SET email = $1
WHERE id = $2;

-- name: DeleteUser :exec
UPDATE users
SET deleted_at = NOW()
WHERE id = $1;

-- name: CountUsers :one
SELECT COUNT(*)::int AS count
FROM users
WHERE deleted_at IS NULL;

-- name: GetAllUsersAdmin :many
SELECT id, name, email, level, created_at
FROM users
WHERE deleted_at IS NULL
ORDER BY id;

-- name: UpdateUserLevel :one
UPDATE users
SET level = $1
WHERE id = $2 AND deleted_at IS NULL
RETURNING *;
