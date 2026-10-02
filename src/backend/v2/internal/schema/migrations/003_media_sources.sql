BEGIN;

CREATE TABLE media_sources (
    post_id bigint PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    source_type smallint NOT NULL CHECK (source_type IN (0, 1)),
    storage_key text NOT NULL UNIQUE
        CHECK (char_length(storage_key) BETWEEN 1 AND 512),
    source_url text
        CHECK (source_url IS NULL OR char_length(source_url) BETWEEN 1 AND 4096),
    original_name text
        CHECK (original_name IS NULL OR char_length(original_name) BETWEEN 1 AND 512),
    declared_mime_type text NOT NULL DEFAULT ''
        CHECK (char_length(declared_mime_type) <= 255),
    byte_size bigint NOT NULL CHECK (byte_size > 0),
    sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT media_sources_origin_check CHECK (
        (source_type = 0 AND source_url IS NULL) OR
        (source_type = 1 AND source_url IS NOT NULL)
    )
);

CREATE INDEX media_sources_sha256_idx ON media_sources (sha256);

COMMIT;
