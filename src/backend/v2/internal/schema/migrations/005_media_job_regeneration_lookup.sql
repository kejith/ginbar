BEGIN;

CREATE INDEX media_jobs_post_kind_id_idx
    ON media_jobs (post_id, kind, id DESC);

COMMIT;
