-- +goose Up
CREATE TABLE invitations (
    token      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    created_by INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    used_by    INT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    used_at    TIMESTAMPTZ DEFAULT NULL
);

CREATE INDEX idx_invitations_created_by ON invitations (created_by);
CREATE INDEX idx_invitations_used_at    ON invitations (used_at);

-- +goose Down
DROP TABLE IF EXISTS invitations;
