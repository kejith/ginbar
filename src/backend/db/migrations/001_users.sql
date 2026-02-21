-- +goose Up
CREATE TABLE users (
    id         SERIAL PRIMARY KEY,
    name       VARCHAR(255) NOT NULL,
    email      VARCHAR(255) NOT NULL,
    password   VARCHAR(255) NOT NULL,
    level      INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ DEFAULT NULL
);

CREATE UNIQUE INDEX uidx_users_name       ON users (name);
CREATE UNIQUE INDEX uidx_users_email      ON users (email);
CREATE        INDEX idx_users_level       ON users (level);
CREATE        INDEX idx_users_deleted_at  ON users (deleted_at);

-- +goose Down
DROP TABLE IF EXISTS users;
