\set ON_ERROR_STOP on

TRUNCATE media_jobs, comment_votes, post_votes, comments, post_tags, tags, media, posts,
         invitations, user_identities, user_credentials, user_roles, users
RESTART IDENTITY CASCADE;

INSERT INTO users (username)
SELECT 'bench-user-' || g
FROM generate_series(1, 1000) AS g;

INSERT INTO posts (author_user_id, content_filter, release_state, score, created_at, released_at)
SELECT ((g - 1) % 1000) + 1,
       CASE
           WHEN g = 50000 THEN 0
           WHEN g % 20 = 0 THEN 2
           WHEN g % 5 = 0 THEN 1
           ELSE 0
       END,
       1,
       ((g * 37) % 801) - 120,
       now() - (100000 - g) * interval '1 second',
       now() - (100000 - g) * interval '1 second'
FROM generate_series(1, 100000) AS g;

INSERT INTO media (post_id, kind, processing_state, storage_key, mime_type, width, height,
                   duration_ms, byte_size, sha256, perceptual_hash)
SELECT g,
       CASE WHEN g % 7 = 0 THEN 1 ELSE 0 END,
       1,
       'bench/' || g,
       CASE WHEN g % 7 = 0 THEN 'video/mp4' ELSE 'image/avif' END,
       CASE WHEN g % 3 = 0 THEN 1280 ELSE 1920 END,
       CASE WHEN g % 3 = 0 THEN 1920 ELSE 1080 END,
       CASE WHEN g % 7 = 0 THEN 12000 + (g % 60000) ELSE 0 END,
       100000 + (g % 5000000),
       decode(md5(g::text) || md5((g + 100000)::text), 'hex'),
       g
FROM generate_series(1, 100000) AS g;

INSERT INTO tags (name, normalized_name, created_by_user_id)
SELECT 'tag-' || g, 'tag-' || g, 1
FROM generate_series(1, 100) AS g;

INSERT INTO post_tags (post_id, tag_id, added_by_user_id)
SELECT p,
       ((p + tag_offset * 17) % 100) + 1,
       ((p - 1) % 1000) + 1
FROM generate_series(1, 100000) AS p
CROSS JOIN generate_series(0, 2) AS tag_offset
ON CONFLICT DO NOTHING;

ANALYZE users;
ANALYZE posts;
ANALYZE media;
ANALYZE tags;
ANALYZE post_tags;
