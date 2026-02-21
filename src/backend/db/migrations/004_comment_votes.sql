-- +goose Up
CREATE TABLE comment_votes (
    comment_id INT NOT NULL,
    user_id    INT NOT NULL,
    vote       SMALLINT NOT NULL DEFAULT 0,

    PRIMARY KEY (comment_id, user_id),
    FOREIGN KEY (user_id)    REFERENCES users    (id) ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (comment_id) REFERENCES comments (id) ON UPDATE CASCADE ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS comment_votes;
