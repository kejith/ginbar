-- +goose Up
-- Recalculate each post's filter from its filter-keyword tags (nsfp, nsfw, secret).
-- Posts that have no such tags keep their existing filter value.
UPDATE posts p
SET filter = (
    SELECT CASE
        WHEN bool_or(t.name = 'secret') THEN 'secret'
        WHEN bool_or(t.name = 'nsfw')   THEN 'nsfw'
        WHEN bool_or(t.name = 'nsfp')   THEN 'nsfp'
        ELSE p.filter
    END
    FROM post_tags pt
    JOIN tags t ON t.id = pt.tag_id
    WHERE pt.post_id = p.id
    GROUP BY p.id
)
WHERE EXISTS (
    SELECT 1
    FROM post_tags pt
    JOIN tags t ON t.id = pt.tag_id
    WHERE pt.post_id = p.id
      AND t.name IN ('nsfp', 'nsfw', 'secret')
);

-- +goose Down
-- No meaningful rollback — filter values are now tag-driven.
