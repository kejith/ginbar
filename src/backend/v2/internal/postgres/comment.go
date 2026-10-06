package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kejith/ginbar/backend/v2/internal/comment"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/role"
)

const listCommentsSQL = `
	SELECT
		COALESCE(c.id, 0),
		COALESCE(c.user_id, 0),
		c.parent_comment_id,
		COALESCE(c.body, ''),
		COALESCE(c.score, 0),
		0::smallint,
		COALESCE(c.created_at, 'epoch'::timestamptz),
		c.deleted_at IS NOT NULL,
		false AS can_moderate
	FROM posts AS p
	LEFT JOIN LATERAL (
		SELECT id, user_id, parent_comment_id, body, score, created_at, deleted_at
		FROM comments
		WHERE post_id = p.id
		  AND id > $2
		ORDER BY id ASC
		LIMIT $3
	) AS c ON true
	WHERE p.id = $1
	  AND p.release_state = 1
	  AND p.deleted_at IS NULL
	ORDER BY c.id ASC NULLS LAST
`

const listCommentsWithViewerSQL = `
	SELECT
		COALESCE(c.id, 0),
		COALESCE(c.user_id, 0),
		c.parent_comment_id,
		COALESCE(c.body, ''),
		COALESCE(c.score, 0),
		CASE WHEN c.deleted_at IS NULL THEN COALESCE(cv.value, 0) ELSE 0 END::smallint,
		COALESCE(c.created_at, 'epoch'::timestamptz),
		c.deleted_at IS NOT NULL,
		EXISTS (
			SELECT 1
			FROM user_roles AS ur
			WHERE ur.user_id = $4
			  AND ur.role IN ($5, $6)
		) AS can_moderate
	FROM posts AS p
	LEFT JOIN LATERAL (
		SELECT id, user_id, parent_comment_id, body, score, created_at, deleted_at
		FROM comments
		WHERE post_id = p.id
		  AND id > $2
		ORDER BY id ASC
		LIMIT $3
	) AS c ON true
	LEFT JOIN comment_votes AS cv
	  ON c.deleted_at IS NULL
	 AND cv.comment_id = c.id
	 AND cv.user_id = $4
	WHERE p.id = $1
	  AND p.release_state = 1
	  AND p.deleted_at IS NULL
	ORDER BY c.id ASC NULLS LAST
`

const createCommentSQL = `
	WITH target_post AS MATERIALIZED (
		SELECT id
		FROM posts
		WHERE id = $1
		  AND release_state = 1
		  AND deleted_at IS NULL
		FOR SHARE
	), parent AS MATERIALIZED (
		SELECT id, post_id, deleted_at
		FROM comments
		WHERE id = $3
		FOR SHARE
	), validated AS (
		SELECT
			p.id AS post_id,
			CASE
				WHEN $3::bigint IS NULL THEN 0
				WHEN parent.id IS NULL OR parent.post_id <> p.id THEN 1
				WHEN parent.deleted_at IS NOT NULL THEN 2
				ELSE 0
			END AS parent_status
		FROM target_post AS p
		LEFT JOIN parent ON true
	), inserted AS (
		INSERT INTO comments (post_id, user_id, parent_comment_id, body)
		SELECT post_id, $2, $3, $4
		FROM validated
		WHERE parent_status = 0
		RETURNING id, post_id, user_id, parent_comment_id, body, score, created_at
	)
	SELECT
		validated.parent_status,
		COALESCE(inserted.id, 0),
		COALESCE(inserted.post_id, 0),
		COALESCE(inserted.user_id, 0),
		COALESCE(inserted.parent_comment_id, 0),
		COALESCE(inserted.body, ''),
		COALESCE(inserted.score, 0),
		COALESCE(inserted.created_at, 'epoch'::timestamptz)
	FROM validated
	LEFT JOIN inserted ON true
`

func (s *Store) ListComments(ctx context.Context, query comment.Query) (comment.ListResult, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if query.ViewerUserID > 0 {
		rows, err = s.pool.Query(
			ctx,
			listCommentsWithViewerSQL,
			query.PostID,
			query.After,
			query.Limit+1,
			query.ViewerUserID,
			role.Moderator,
			role.Admin,
		)
	} else {
		rows, err = s.pool.Query(ctx, listCommentsSQL, query.PostID, query.After, query.Limit+1)
	}
	if err != nil {
		return comment.ListResult{}, fmt.Errorf("query comments: %w", err)
	}
	defer rows.Close()

	comments := make([]comment.Comment, 0, query.Limit+1)
	postFound := false
	canModerate := false
	for rows.Next() {
		postFound = true
		var (
			id          int64
			authorID    int64
			parentID    pgtype.Int8
			body        string
			score       int32
			userVote    int16
			createdAt   time.Time
			deleted     bool
			rowModerate bool
		)
		if err := rows.Scan(
			&id,
			&authorID,
			&parentID,
			&body,
			&score,
			&userVote,
			&createdAt,
			&deleted,
			&rowModerate,
		); err != nil {
			return comment.ListResult{}, fmt.Errorf("scan comment row: %w", err)
		}
		if rowModerate {
			canModerate = true
		}
		if id == 0 {
			continue
		}
		entry := comment.Comment{
			ID:        id,
			PostID:    query.PostID,
			AuthorID:  authorID,
			Score:     score,
			UserVote:  model.PostVote(userVote),
			CreatedAt: createdAt,
			Deleted:   deleted,
		}
		if parentID.Valid {
			value := parentID.Int64
			entry.ParentCommentID = &value
		}
		if !deleted {
			value := body
			entry.Body = &value
		}
		comments = append(comments, entry)
	}
	if err := rows.Err(); err != nil {
		return comment.ListResult{}, fmt.Errorf("read comment rows: %w", err)
	}
	if !postFound {
		return comment.ListResult{}, comment.ErrPostNotFound
	}
	return comment.ListResult{Comments: comments, CanModerate: canModerate}, nil
}

func (s *Store) CreateComment(ctx context.Context, request comment.CreateRequest) (comment.Comment, error) {
	var parentArg any
	if request.ParentCommentID != nil {
		parentArg = *request.ParentCommentID
	}

	var (
		parentStatus int32
		id           int64
		postID       int64
		authorID     int64
		parentID     int64
		body         string
		score        int32
		createdAt    time.Time
	)
	err := s.pool.QueryRow(
		ctx,
		createCommentSQL,
		request.PostID,
		request.UserID,
		parentArg,
		request.Body,
	).Scan(&parentStatus, &id, &postID, &authorID, &parentID, &body, &score, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return comment.Comment{}, comment.ErrPostNotFound
	}
	if err != nil {
		return comment.Comment{}, fmt.Errorf("create comment: %w", err)
	}
	switch parentStatus {
	case 1:
		return comment.Comment{}, comment.ErrParentCommentNotFound
	case 2:
		return comment.Comment{}, comment.ErrParentCommentDeleted
	case 0:
	default:
		return comment.Comment{}, fmt.Errorf("create comment: unexpected parent status %d", parentStatus)
	}
	if id == 0 {
		return comment.Comment{}, fmt.Errorf("create comment: insert returned no row")
	}

	created := comment.Comment{
		ID:        id,
		PostID:    postID,
		AuthorID:  authorID,
		Body:      &body,
		Score:     score,
		UserVote:  model.VoteNeutral,
		CreatedAt: createdAt,
	}
	if parentID != 0 {
		value := parentID
		created.ParentCommentID = &value
	}
	return created, nil
}
