-- +goose Up
CREATE TABLE messages (
    id         SERIAL PRIMARY KEY,
    kind       VARCHAR(16)  NOT NULL DEFAULT 'private',
    from_name  VARCHAR(255) DEFAULT NULL REFERENCES users (name) ON UPDATE CASCADE ON DELETE SET NULL,
    to_name    VARCHAR(255) NOT NULL   REFERENCES users (name) ON UPDATE CASCADE ON DELETE CASCADE,
    subject    TEXT         DEFAULT NULL,
    body       TEXT         NOT NULL,
    read_at    TIMESTAMPTZ  DEFAULT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_messages_to_name   ON messages (to_name, created_at DESC);
CREATE INDEX idx_messages_from_name ON messages (from_name);
CREATE INDEX idx_messages_read_at   ON messages (to_name, read_at) WHERE read_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS messages;
