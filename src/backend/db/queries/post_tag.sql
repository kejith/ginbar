-- name: GetTagsByPost :many
SELECT
    pt.id,
    pt.score,
    pt.post_id,
    pt.user_id,
    t.name,
    COALESCE(ptv.vote, 0)::smallint AS vote
FROM post_tags pt
LEFT JOIN tags t ON t.id = pt.tag_id
LEFT JOIN post_tag_votes ptv ON ptv.post_tag_id = pt.id AND ptv.user_id = $1
WHERE pt.post_id = $2
ORDER BY pt.score DESC;

-- name: GetPostTag :one
SELECT * FROM post_tags WHERE id = $1;

-- name: AddTagToPost :one
INSERT INTO post_tags (tag_id, post_id, user_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: RemoveTagFromPost :exec
DELETE FROM post_tags WHERE tag_id = $1 AND post_id = $2;
