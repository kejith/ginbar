package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/moderation"
	"github.com/kejith/ginbar/backend/v2/internal/role"
)

const hidePostSQL = `
WITH actor AS MATERIALIZED (
	SELECT EXISTS (
		SELECT 1
		FROM user_roles
		WHERE user_id = $1
		  AND role IN ($3, $4)
	) AS allowed
), target AS MATERIALIZED (
	SELECT id
	FROM posts
	WHERE id = $2
), updated AS (
	UPDATE posts AS p
	SET deleted_at = COALESCE(p.deleted_at, now()),
	    moderated_at = COALESCE(p.moderated_at, now()),
	    moderated_by_user_id = COALESCE(p.moderated_by_user_id, $1)
	FROM actor, target
	WHERE actor.allowed
	  AND p.id = target.id
	RETURNING p.id, p.deleted_at, p.moderated_at, p.moderated_by_user_id
)
SELECT actor.allowed,
       target.id IS NOT NULL AS found,
       COALESCE(updated.id, target.id, 0),
       updated.deleted_at IS NOT NULL,
       COALESCE(updated.moderated_at, 'epoch'::timestamptz),
       COALESCE(updated.moderated_by_user_id, 0)
FROM actor
LEFT JOIN target ON true
LEFT JOIN updated ON true
`

const hideCommentSQL = `
WITH actor AS MATERIALIZED (
	SELECT EXISTS (
		SELECT 1
		FROM user_roles
		WHERE user_id = $1
		  AND role IN ($4, $5)
	) AS allowed
), target AS MATERIALIZED (
	SELECT id
	FROM comments
	WHERE post_id = $2
	  AND id = $3
), updated AS (
	UPDATE comments AS c
	SET deleted_at = COALESCE(c.deleted_at, now()),
	    moderated_at = COALESCE(c.moderated_at, now()),
	    moderated_by_user_id = COALESCE(c.moderated_by_user_id, $1)
	FROM actor, target
	WHERE actor.allowed
	  AND c.id = target.id
	RETURNING c.id, c.deleted_at, c.moderated_at, c.moderated_by_user_id
)
SELECT actor.allowed,
       target.id IS NOT NULL AS found,
       COALESCE(updated.id, target.id, 0),
       updated.deleted_at IS NOT NULL,
       COALESCE(updated.moderated_at, 'epoch'::timestamptz),
       COALESCE(updated.moderated_by_user_id, 0)
FROM actor
LEFT JOIN target ON true
LEFT JOIN updated ON true
`

func (s *Store) HidePost(ctx context.Context, actorUserID, postID int64) (moderation.PostResult, error) {
	var (
		allowed           bool
		found             bool
		resultPostID      int64
		deleted           bool
		moderatedAt       time.Time
		moderatedByUserID int64
	)
	if err := s.pool.QueryRow(
		ctx,
		hidePostSQL,
		actorUserID,
		postID,
		role.Moderator,
		role.Admin,
	).Scan(&allowed, &found, &resultPostID, &deleted, &moderatedAt, &moderatedByUserID); err != nil {
		return moderation.PostResult{}, fmt.Errorf("hide post: %w", err)
	}
	if !allowed {
		return moderation.PostResult{}, moderation.ErrForbidden
	}
	if !found {
		return moderation.PostResult{}, moderation.ErrPostNotFound
	}
	if resultPostID != postID || !deleted || moderatedByUserID <= 0 || moderatedAt.IsZero() {
		return moderation.PostResult{}, fmt.Errorf("hide post: incomplete authoritative state")
	}
	return moderation.PostResult{
		PostID:            resultPostID,
		Deleted:           deleted,
		ModeratedAt:       moderatedAt,
		ModeratedByUserID: moderatedByUserID,
	}, nil
}

func (s *Store) HideComment(ctx context.Context, actorUserID, postID, commentID int64) (moderation.CommentResult, error) {
	var (
		allowed           bool
		found             bool
		resultCommentID   int64
		deleted           bool
		moderatedAt       time.Time
		moderatedByUserID int64
	)
	if err := s.pool.QueryRow(
		ctx,
		hideCommentSQL,
		actorUserID,
		postID,
		commentID,
		role.Moderator,
		role.Admin,
	).Scan(&allowed, &found, &resultCommentID, &deleted, &moderatedAt, &moderatedByUserID); err != nil {
		return moderation.CommentResult{}, fmt.Errorf("hide comment: %w", err)
	}
	if !allowed {
		return moderation.CommentResult{}, moderation.ErrForbidden
	}
	if !found {
		return moderation.CommentResult{}, moderation.ErrCommentNotFound
	}
	if resultCommentID != commentID || !deleted || moderatedByUserID <= 0 || moderatedAt.IsZero() {
		return moderation.CommentResult{}, fmt.Errorf("hide comment: incomplete authoritative state")
	}
	return moderation.CommentResult{
		PostID:            postID,
		CommentID:         resultCommentID,
		Deleted:           deleted,
		ModeratedAt:       moderatedAt,
		ModeratedByUserID: moderatedByUserID,
	}, nil
}
