-- +goose Up
ALTER TABLE posts ADD COLUMN released BOOLEAN NOT NULL DEFAULT TRUE;

-- New user uploads start unreleased; only existing rows keep TRUE.
-- The released=FALSE default for new inserts is handled at the application layer:
--   CreateDirtyPost passes released=FALSE, FinalizePost leaves it untouched,
--   and the user explicitly releases after adding tags/comment.

-- +goose Down
ALTER TABLE posts DROP COLUMN released;
