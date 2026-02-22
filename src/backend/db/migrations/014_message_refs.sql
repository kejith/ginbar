-- +goose Up
ALTER TABLE messages
    ADD COLUMN ref_post_id    INT DEFAULT NULL REFERENCES posts    (id) ON DELETE SET NULL,
    ADD COLUMN ref_comment_id INT DEFAULT NULL REFERENCES comments (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE messages
    DROP COLUMN IF EXISTS ref_post_id,
    DROP COLUMN IF EXISTS ref_comment_id;
