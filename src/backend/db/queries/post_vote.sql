-- name: UpsertPostVote :exec
INSERT INTO post_votes (post_id, user_id, vote)
VALUES ($1, $2, $3)
ON CONFLICT (post_id, user_id) DO UPDATE SET vote = EXCLUDED.vote;

-- name: DeletePostVote :exec
DELETE FROM post_votes WHERE post_id = $1 AND user_id = $2;
