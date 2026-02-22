-- +goose Up

-- Add the content-filter label to every post row.
-- Valid values: 'sfw' | 'nsfp' | 'nsfw' | 'secret'
ALTER TABLE posts
    ADD COLUMN filter VARCHAR(10) NOT NULL DEFAULT 'sfw'
        CHECK (filter IN ('sfw', 'nsfp', 'nsfw', 'secret'));

-- Back-fill from the existing user_level column:
--   user_level = 0  → sfw      (visible to everybody)
--   user_level = 1  → nsfw     (members+; treat old "level 1" content as nsfw)
--   user_level >= 5 → secret   (secret role+)
--   anything else   → sfw      (safe default)
UPDATE posts
SET filter = CASE
    WHEN user_level >= 5 THEN 'secret'
    WHEN user_level >= 1 THEN 'nsfw'
    ELSE 'sfw'
END;

CREATE INDEX idx_posts_filter ON posts (filter);

-- +goose Down
DROP INDEX IF EXISTS idx_posts_filter;
ALTER TABLE posts DROP COLUMN IF EXISTS filter;
