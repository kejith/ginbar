BEGIN;

CREATE TABLE private_message_conversations (
    user_low_id bigint NOT NULL REFERENCES users(id),
    user_high_id bigint NOT NULL REFERENCES users(id),
    latest_message_id bigint NOT NULL REFERENCES private_messages(id),
    PRIMARY KEY (user_low_id, user_high_id),
    CONSTRAINT private_message_conversations_distinct_ordered_users CHECK (user_low_id < user_high_id)
);

INSERT INTO private_message_conversations (user_low_id, user_high_id, latest_message_id)
SELECT
    LEAST(sender_user_id, recipient_user_id),
    GREATEST(sender_user_id, recipient_user_id),
    max(id)
FROM private_messages
GROUP BY
    LEAST(sender_user_id, recipient_user_id),
    GREATEST(sender_user_id, recipient_user_id);

CREATE INDEX private_message_conversations_low_latest_idx
    ON private_message_conversations (user_low_id, latest_message_id DESC, user_high_id);

CREATE INDEX private_message_conversations_high_latest_idx
    ON private_message_conversations (user_high_id, latest_message_id DESC, user_low_id);

COMMIT;
