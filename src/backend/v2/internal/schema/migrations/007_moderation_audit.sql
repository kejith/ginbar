BEGIN;

ALTER TABLE posts
    ADD COLUMN moderated_at timestamptz,
    ADD COLUMN moderated_by_user_id bigint REFERENCES users(id),
    ADD CONSTRAINT posts_moderation_consistent CHECK (
        (moderated_at IS NULL) = (moderated_by_user_id IS NULL)
        AND (moderated_at IS NULL OR deleted_at IS NOT NULL)
    );

ALTER TABLE comments
    ADD COLUMN moderated_at timestamptz,
    ADD COLUMN moderated_by_user_id bigint REFERENCES users(id),
    ADD CONSTRAINT comments_moderation_consistent CHECK (
        (moderated_at IS NULL) = (moderated_by_user_id IS NULL)
        AND (moderated_at IS NULL OR deleted_at IS NOT NULL)
    );

COMMIT;
