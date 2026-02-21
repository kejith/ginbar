-- name: UpsertCommentVote :exec
INSERT INTO comment_votes (comment_id, user_id, vote)
VALUES ($1, $2, $3)
ON CONFLICT (comment_id, user_id) DO UPDATE SET vote = EXCLUDED.vote;

-- name: DeleteCommentVote :exec
DELETE FROM comment_votes WHERE comment_id = $1 AND user_id = $2;
