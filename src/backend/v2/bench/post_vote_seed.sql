\set ON_ERROR_STOP on

INSERT INTO post_votes (post_id, user_id, value)
SELECT post_id,
       ((post_id + vote_offset * 137 - 1) % 1000) + 1,
       CASE WHEN (post_id + vote_offset) % 2 = 0 THEN 1 ELSE -1 END
FROM generate_series(1, 100000) AS post_id
CROSS JOIN generate_series(0, 1) AS vote_offset
ON CONFLICT DO NOTHING;

ANALYZE post_votes;
