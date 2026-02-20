-- +goose Up
CREATE TABLE tags (
    id         SERIAL PRIMARY KEY,
    name       VARCHAR(32) NOT NULL,
    user_level INT NOT NULL DEFAULT 0
);

CREATE UNIQUE INDEX uidx_tags_name      ON tags (name);
CREATE        INDEX idx_tags_userlevel  ON tags (user_level);

-- +goose Down
DROP TABLE IF EXISTS tags;
