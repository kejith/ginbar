-- +goose Up
CREATE TABLE comments (
    id         SERIAL PRIMARY KEY,
    content    TEXT NOT NULL,
    score      INT NOT NULL DEFAULT 0,
    user_name  VARCHAR(255) NOT NULL,
    post_id    INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ DEFAULT NULL,

    FOREIGN KEY (user_name) REFERENCES users (name) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (post_id)   REFERENCES posts (id)   ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE INDEX idx_comments_deleted_at ON comments (deleted_at);

-- +goose Down
DROP TABLE IF EXISTS comments;
