BEGIN;

CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username text NOT NULL,
    status smallint NOT NULL DEFAULT 0 CHECK (status IN (0, 1)),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_username_length CHECK (char_length(username) BETWEEN 1 AND 32)
);
CREATE UNIQUE INDEX users_username_lower_uidx ON users (lower(username));

CREATE TABLE user_roles (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role smallint NOT NULL CHECK (role IN (0, 1, 2)),
    granted_by_user_id bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role)
);

CREATE TABLE user_credentials (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind smallint NOT NULL CHECK (kind IN (0)),
    secret_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, kind)
);

CREATE TABLE user_identities (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider text NOT NULL,
    subject text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, subject)
);
CREATE INDEX user_identities_user_idx ON user_identities (user_id);

CREATE TABLE invitations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_by_user_id bigint NOT NULL REFERENCES users(id),
    claimed_by_user_id bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    claimed_at timestamptz,
    revoked_at timestamptz,
    CONSTRAINT invitations_claim_consistent CHECK (
        (claimed_by_user_id IS NULL AND claimed_at IS NULL) OR
        (claimed_by_user_id IS NOT NULL AND claimed_at IS NOT NULL)
    )
);
CREATE INDEX invitations_creator_idx ON invitations (created_by_user_id, id DESC);

CREATE TABLE posts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    author_user_id bigint NOT NULL REFERENCES users(id),
    content_filter smallint NOT NULL DEFAULT 0 CHECK (content_filter IN (0, 1, 2, 3)),
    release_state smallint NOT NULL DEFAULT 0 CHECK (release_state IN (0, 1, 2, 3)),
    score integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    released_at timestamptz,
    deleted_at timestamptz,
    CONSTRAINT posts_release_consistent CHECK (
        release_state <> 1 OR released_at IS NOT NULL
    )
);
CREATE INDEX posts_feed_released_idx ON posts (id DESC)
    WHERE release_state = 1 AND deleted_at IS NULL;
CREATE INDEX posts_author_idx ON posts (author_user_id, id DESC);

CREATE TABLE media (
    post_id bigint PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    kind smallint NOT NULL CHECK (kind IN (0, 1)),
    processing_state smallint NOT NULL DEFAULT 0 CHECK (processing_state IN (0, 1, 2)),
    storage_key text NOT NULL UNIQUE,
    mime_type text NOT NULL,
    width integer NOT NULL CHECK (width > 0),
    height integer NOT NULL CHECK (height > 0),
    duration_ms bigint NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    byte_size bigint NOT NULL CHECK (byte_size >= 0),
    sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
    perceptual_hash bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX media_sha256_idx ON media (sha256);
CREATE INDEX media_phash_idx ON media (perceptual_hash) WHERE perceptual_hash IS NOT NULL;

CREATE TABLE tags (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL,
    normalized_name text NOT NULL UNIQUE,
    created_by_user_id bigint NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tags_name_length CHECK (char_length(name) BETWEEN 1 AND 80),
    CONSTRAINT tags_normalized_length CHECK (char_length(normalized_name) BETWEEN 1 AND 80)
);

CREATE TABLE post_tags (
    post_id bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id bigint NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    added_by_user_id bigint NOT NULL REFERENCES users(id),
    removed_by_user_id bigint REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    removed_at timestamptz,
    PRIMARY KEY (post_id, tag_id),
    CONSTRAINT post_tags_remove_consistent CHECK (
        (removed_by_user_id IS NULL AND removed_at IS NULL) OR
        (removed_by_user_id IS NOT NULL AND removed_at IS NOT NULL)
    )
);
CREATE INDEX post_tags_tag_post_active_idx ON post_tags (tag_id, post_id DESC)
    WHERE removed_at IS NULL;

CREATE TABLE comments (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    post_id bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id),
    parent_comment_id bigint REFERENCES comments(id),
    body text NOT NULL,
    score integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CONSTRAINT comments_body_length CHECK (char_length(body) BETWEEN 1 AND 10000)
);
CREATE INDEX comments_post_idx ON comments (post_id, id);
CREATE INDEX comments_parent_idx ON comments (parent_comment_id, id) WHERE parent_comment_id IS NOT NULL;

CREATE TABLE post_votes (
    post_id bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    value smallint NOT NULL CHECK (value IN (-1, 1)),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (post_id, user_id)
);
CREATE INDEX post_votes_user_idx ON post_votes (user_id, post_id DESC);

CREATE TABLE comment_votes (
    comment_id bigint NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    value smallint NOT NULL CHECK (value IN (-1, 1)),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (comment_id, user_id)
);
CREATE INDEX comment_votes_user_idx ON comment_votes (user_id, comment_id DESC);

CREATE TABLE media_jobs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    post_id bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    kind smallint NOT NULL CHECK (kind IN (0, 1, 2, 3)),
    state smallint NOT NULL DEFAULT 0 CHECK (state IN (0, 1, 2, 3)),
    priority smallint NOT NULL DEFAULT 0,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    claimed_by text,
    lease_expires_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX media_jobs_claim_idx ON media_jobs (priority DESC, available_at, id)
    WHERE state = 0;
CREATE UNIQUE INDEX media_jobs_one_active_kind_idx ON media_jobs (post_id, kind)
    WHERE state IN (0, 1);

COMMIT;
