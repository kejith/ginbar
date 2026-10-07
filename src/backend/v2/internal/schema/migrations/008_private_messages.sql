BEGIN;

CREATE TABLE private_messages (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sender_user_id bigint NOT NULL REFERENCES users(id),
    recipient_user_id bigint NOT NULL REFERENCES users(id),
    body text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT private_messages_distinct_users CHECK (sender_user_id <> recipient_user_id),
    CONSTRAINT private_messages_body_length CHECK (char_length(body) BETWEEN 1 AND 10000)
);

CREATE INDEX private_messages_thread_idx
    ON private_messages (sender_user_id, recipient_user_id, id DESC);

COMMIT;
