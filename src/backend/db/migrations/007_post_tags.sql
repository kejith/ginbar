-- +goose Up
CREATE TABLE post_tags (
    id      SERIAL PRIMARY KEY,
    post_id INT NOT NULL,
    tag_id  INT NOT NULL,
    user_id INT NOT NULL,
    score   INT NOT NULL DEFAULT 0,

    FOREIGN KEY (user_id) REFERENCES users (id)  ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (tag_id)  REFERENCES tags  (id)  ON UPDATE CASCADE ON DELETE CASCADE,
    FOREIGN KEY (post_id) REFERENCES posts (id)  ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE UNIQUE INDEX uidx_post_tags ON post_tags (tag_id, post_id);

-- +goose Down
DROP TABLE IF EXISTS post_tags;
