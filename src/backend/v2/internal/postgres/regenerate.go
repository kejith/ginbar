package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kejith/ginbar/backend/v2/internal/regenerate"
	"github.com/kejith/ginbar/backend/v2/internal/role"
)

const (
	mediaJobStatePending   int16 = 0
	mediaJobStateRunning   int16 = 1
	mediaJobStateSucceeded int16 = 2
	mediaJobStateFailed    int16 = 3
)

const requestRegenerationSQL = `
WITH actor AS MATERIALIZED (
    SELECT EXISTS (
        SELECT 1
        FROM user_roles
        WHERE user_id = $1
          AND role = $4
    ) AS allowed
), target AS MATERIALIZED (
    SELECT job.id, job.state, job.lease_generation
    FROM actor
    JOIN media_jobs AS job ON true
    JOIN posts AS post ON post.id = job.post_id
    JOIN media_sources AS source ON source.post_id = job.post_id
    JOIN media AS current_media
      ON current_media.post_id = job.post_id
     AND current_media.processing_state = 1
    WHERE actor.allowed
      AND job.post_id = $2
      AND job.kind = $3
      AND post.release_state = 1
      AND post.deleted_at IS NULL
    ORDER BY (job.state IN (0, 1)) DESC, job.id DESC
    LIMIT 1
    FOR UPDATE OF job
), requeued AS (
    UPDATE media_jobs AS job
    SET state = 0,
        attempts = 0,
        available_at = clock_timestamp(),
        claimed_at = NULL,
        claimed_by = NULL,
        lease_expires_at = NULL,
        lease_generation = job.lease_generation + CASE WHEN target.state = 1 THEN 1 ELSE 0 END,
        last_error = NULL,
        updated_at = clock_timestamp()
    FROM target
    WHERE job.id = target.id
      AND target.state <> 0
    RETURNING job.id, target.state AS previous_state
), result AS MATERIALIZED (
    SELECT id, previous_state
    FROM requeued
    UNION ALL
    SELECT id, state
    FROM target
    WHERE state = 0
    LIMIT 1
)
SELECT actor.allowed, result.id, result.previous_state
FROM actor
LEFT JOIN result ON true
`

func (s *Store) RequestRegeneration(
	ctx context.Context,
	actorUserID, postID int64,
) (regenerate.Requested, error) {
	var (
		allowed       bool
		jobID         pgtype.Int8
		previousState pgtype.Int2
	)
	if err := s.pool.QueryRow(
		ctx,
		requestRegenerationSQL,
		actorUserID,
		postID,
		mediaJobKindInitialProcess,
		role.Admin,
	).Scan(&allowed, &jobID, &previousState); err != nil {
		return regenerate.Requested{}, fmt.Errorf("request media regeneration: %w", err)
	}
	if !allowed {
		return regenerate.Requested{}, regenerate.ErrForbidden
	}
	if !jobID.Valid && !previousState.Valid {
		return regenerate.Requested{}, regenerate.ErrNotRegenerable
	}
	if !jobID.Valid || !previousState.Valid {
		return regenerate.Requested{}, fmt.Errorf("request media regeneration: incomplete authoritative state")
	}

	var outcome regenerate.Outcome
	switch previousState.Int16 {
	case mediaJobStatePending:
		outcome = regenerate.OutcomeCoalesced
	case mediaJobStateRunning:
		outcome = regenerate.OutcomeSuperseded
	case mediaJobStateSucceeded, mediaJobStateFailed:
		outcome = regenerate.OutcomeQueued
	default:
		return regenerate.Requested{}, fmt.Errorf(
			"request media regeneration returned unknown prior job state %d",
			previousState.Int16,
		)
	}
	return regenerate.Requested{JobID: jobID.Int64, Outcome: outcome}, nil
}
