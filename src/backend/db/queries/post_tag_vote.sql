-- name: UpsertPostTagVote :exec
INSERT INTO post_tag_votes (post_tag_id, user_id, vote)
VALUES ($1, $2, $3)
ON CONFLICT (post_tag_id, user_id) DO UPDATE SET vote = EXCLUDED.vote;

-- name: DeletePostTagVote :exec
DELETE FROM post_tag_votes WHERE post_tag_id = $1 AND user_id = $2;
