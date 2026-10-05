package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/postvote"
)

func (s *Store) SetPostVote(ctx context.Context, userID, postID int64, vote model.PostVote) (postvote.Result, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return postvote.Result{}, fmt.Errorf("begin post vote transaction: %w", err)
	}
	defer rollbackWithTimeout(tx)

	var score int32
	if err := tx.QueryRow(ctx, `
		SELECT score
		FROM posts
		WHERE id = $1
		  AND release_state = 1
		  AND deleted_at IS NULL
		FOR UPDATE
	`, postID).Scan(&score); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return postvote.Result{}, postvote.ErrPostNotFound
		}
		return postvote.Result{}, fmt.Errorf("lock post for vote: %w", err)
	}

	currentVote := model.VoteNeutral
	var storedVote int16
	if err := tx.QueryRow(ctx, `
		SELECT value
		FROM post_votes
		WHERE post_id = $1 AND user_id = $2
	`, postID, userID).Scan(&storedVote); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return postvote.Result{}, fmt.Errorf("load current post vote: %w", err)
		}
	} else {
		currentVote = model.PostVote(storedVote)
	}

	if currentVote != vote {
		switch vote {
		case model.VoteNeutral:
			if _, err := tx.Exec(ctx, `
				DELETE FROM post_votes
				WHERE post_id = $1 AND user_id = $2
			`, postID, userID); err != nil {
				return postvote.Result{}, fmt.Errorf("remove post vote: %w", err)
			}
		case model.VoteDown, model.VoteUp:
			if _, err := tx.Exec(ctx, `
				INSERT INTO post_votes (post_id, user_id, value)
				VALUES ($1, $2, $3)
				ON CONFLICT (post_id, user_id) DO UPDATE
				SET value = EXCLUDED.value, updated_at = now()
			`, postID, userID, int16(vote)); err != nil {
				return postvote.Result{}, fmt.Errorf("store post vote: %w", err)
			}
		default:
			return postvote.Result{}, postvote.ErrInvalidVote
		}

		delta := int32(vote - currentVote)
		if err := tx.QueryRow(ctx, `
			UPDATE posts
			SET score = score + $1
			WHERE id = $2
			RETURNING score
		`, delta, postID).Scan(&score); err != nil {
			return postvote.Result{}, fmt.Errorf("update post score: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return postvote.Result{}, fmt.Errorf("commit post vote transaction rolled back: %w", err)
		}
		return postvote.Result{}, fmt.Errorf("%w: commit post vote transaction: %v", postvote.ErrCommitOutcomeUnknown, err)
	}
	return postvote.Result{PostID: postID, Score: score, Vote: vote}, nil
}
