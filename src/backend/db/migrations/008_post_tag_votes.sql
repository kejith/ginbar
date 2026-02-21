-- +goose Up
CREATE TABLE post_tag_votes (
    post_tag_id INT NOT NULL,
    user_id     INT NOT NULL,
    vote        SMALLINT NOT NULL DEFAULT 0,

    PRIMARY KEY (post_tag_id, user_id),
    FOREIGN KEY (user_id)     REFERENCES users     (id) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (post_tag_id) REFERENCES post_tags (id) ON UPDATE CASCADE ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS post_tag_votes;
