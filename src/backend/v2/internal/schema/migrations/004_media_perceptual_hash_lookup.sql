BEGIN;

CREATE INDEX media_phash_post_idx
    ON media (perceptual_hash, post_id DESC)
    WHERE perceptual_hash IS NOT NULL;

DROP INDEX media_phash_idx;

COMMIT;
