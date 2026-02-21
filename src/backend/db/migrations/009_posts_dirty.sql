-- +goose Up
ALTER TABLE posts ADD COLUMN dirty BOOLEAN NOT NULL DEFAULT FALSE;

-- Index to help the import worker quickly find unfinished posts.
CREATE INDEX idx_posts_dirty ON posts (dirty) WHERE dirty = TRUE;

-- +goose Down
DROP INDEX IF EXISTS idx_posts_dirty;
ALTER TABLE posts DROP COLUMN IF EXISTS dirty;
