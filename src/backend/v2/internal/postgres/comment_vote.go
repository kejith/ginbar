package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kejith/ginbar/backend/v2/internal/commentvote"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

const lockCommentForVoteSQL = `
	WITH target_post AS MATERIALIZED (
		SELECT id
		FROM posts
		WHERE id = $1
		  AND release_state = 1
		  AND deleted_at IS NULL
		FOR SHARE
	)
	SELECT c.score
	FROM target_post AS p
	JOIN comments AS c ON c.post_id = p.id
	WHERE c.id = $2
	  AND c.deleted_at IS NULL
	FOR UPDATE OF c
`

func (s *Store) SetCommentVote(ctx context.Context, userID, postID, commentID int64, vote model.PostVote) (commentvote.Result, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return commentvote.Result{}, fmt.Errorf("begin comment vote transaction: %w", err)
	}
	defer rollbackWithTimeout(tx)

	var score int32
	if err := tx.QueryRow(ctx, lockCommentForVoteSQL, postID, commentID).Scan(&score); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commentvote.Result{}, commentvote.ErrCommentNotFound
		}
		return commentvote.Result{}, fmt.Errorf("lock comment for vote: %w", err)
	}

	currentVote := model.VoteNeutral
	var storedVote int16
	if err := tx.QueryRow(ctx, `
		SELECT value
		FROM comment_votes
		WHERE comment_id = $1 AND user_id = $2
	`, commentID, userID).Scan(&storedVote); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return commentvote.Result{}, fmt.Errorf("load current comment vote: %w", err)
		}
	} else {
		currentVote = model.PostVote(storedVote)
	}

	if currentVote != vote {
		switch vote {
		case model.VoteNeutral:
			if _, err := tx.Exec(ctx, `
				DELETE FROM comment_votes
				WHERE comment_id = $1 AND user_id = $2
			`, commentID, userID); err != nil {
				return commentvote.Result{}, fmt.Errorf("remove comment vote: %w", err)
			}
		case model.VoteDown, model.VoteUp:
			if _, err := tx.Exec(ctx, `
				INSERT INTO comment_votes (comment_id, user_id, value)
				VALUES ($1, $2, $3)
				ON CONFLICT (comment_id, user_id) DO UPDATE
				SET value = EXCLUDED.value, updated_at = now()
			`, commentID, userID, int16(vote)); err != nil {
				return commentvote.Result{}, fmt.Errorf("store comment vote: %w", err)
			}
		default:
			return commentvote.Result{}, commentvote.ErrInvalidVote
		}

		delta := int32(vote - currentVote)
		if err := tx.QueryRow(ctx, `
			UPDATE comments
			SET score = score + $1
			WHERE id = $2
			RETURNING score
		`, delta, commentID).Scan(&score); err != nil {
			return commentvote.Result{}, fmt.Errorf("update comment score: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return commentvote.Result{}, fmt.Errorf("commit comment vote transaction rolled back: %w", err)
		}
		return commentvote.Result{}, fmt.Errorf("%w: commit comment vote transaction: %v", commentvote.ErrCommitOutcomeUnknown, err)
	}
	return commentvote.Result{CommentID: commentID, Score: score, Vote: vote}, nil
}
