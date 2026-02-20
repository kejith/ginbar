-- +goose Up
CREATE TABLE posts (
    id                 SERIAL PRIMARY KEY,
    url                TEXT NOT NULL DEFAULT '',
    uploaded_filename  TEXT NOT NULL DEFAULT '',
    filename           VARCHAR(255) NOT NULL,
    thumbnail_filename VARCHAR(255) NOT NULL,
    content_type       VARCHAR(255) NOT NULL,
    score              INT NOT NULL DEFAULT 0,
    user_level         INT NOT NULL DEFAULT 0,
    p_hash_0           BIGINT NOT NULL DEFAULT 0,
    p_hash_1           BIGINT NOT NULL DEFAULT 0,
    p_hash_2           BIGINT NOT NULL DEFAULT 0,
    p_hash_3           BIGINT NOT NULL DEFAULT 0,
    user_name          VARCHAR(255) NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at         TIMESTAMPTZ DEFAULT NULL,

    FOREIGN KEY (user_name) REFERENCES users (name) ON UPDATE CASCADE ON DELETE RESTRICT
);

CREATE INDEX idx_posts_userlevel  ON posts (user_level);
CREATE INDEX idx_posts_deleted_at ON posts (deleted_at);

-- +goose Down
DROP TABLE IF EXISTS posts;
