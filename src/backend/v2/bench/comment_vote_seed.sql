\set ON_ERROR_STOP on

INSERT INTO comments (post_id, user_id, body, score, created_at)
SELECT ((g - 1) % 100000) + 1,
       ((g * 17 - 1) % 1000) + 1,
       'bench-comment-' || g,
       CASE WHEN g % 3 = 0 THEN -1 WHEN g % 3 = 1 THEN 0 ELSE 1 END,
       now() - (200000 - g) * interval '500 milliseconds'
FROM generate_series(1, 200000) AS g;

INSERT INTO comment_votes (comment_id, user_id, value)
SELECT id,
       ((id - 1) % 1000) + 1,
       CASE WHEN id % 2 = 0 THEN 1 ELSE -1 END
FROM comments
ON CONFLICT DO NOTHING;

ANALYZE comments;
ANALYZE comment_votes;
