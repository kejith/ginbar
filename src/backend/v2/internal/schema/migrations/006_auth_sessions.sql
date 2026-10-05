BEGIN;

CREATE TABLE user_sessions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CONSTRAINT user_sessions_expiry_check CHECK (expires_at > created_at)
);

COMMIT;
