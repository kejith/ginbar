-- +goose Up
CREATE TABLE post_votes (
    post_id INT NOT NULL,
    user_id INT NOT NULL,
    vote    SMALLINT NOT NULL DEFAULT 0,

    PRIMARY KEY (post_id, user_id),
    FOREIGN KEY (user_id) REFERENCES users (id)  ON UPDATE CASCADE ON DELETE RESTRICT,
    FOREIGN KEY (post_id) REFERENCES posts (id)  ON UPDATE CASCADE ON DELETE RESTRICT
);

-- +goose Down
DROP TABLE IF EXISTS post_votes;
