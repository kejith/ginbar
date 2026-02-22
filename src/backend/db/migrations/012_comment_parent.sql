-- +goose Up
ALTER TABLE comments
    ADD COLUMN parent_id INT DEFAULT NULL
        REFERENCES comments (id) ON UPDATE CASCADE ON DELETE SET NULL;

CREATE INDEX idx_comments_parent_id ON comments (parent_id);

-- +goose Down
DROP INDEX IF EXISTS idx_comments_parent_id;
ALTER TABLE comments DROP COLUMN IF EXISTS parent_id;
