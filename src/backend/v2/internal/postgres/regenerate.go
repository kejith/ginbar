package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kejith/ginbar/backend/v2/internal/regenerate"
)

const (
	mediaJobStatePending   int16 = 0
	mediaJobStateRunning   int16 = 1
	mediaJobStateSucceeded int16 = 2
	mediaJobStateFailed    int16 = 3
)

const requestRegenerationSQL = `
WITH target AS MATERIALIZED (
    SELECT job.id, job.state, job.lease_generation
    FROM media_jobs AS job
    JOIN posts AS post ON post.id = job.post_id
    JOIN media_sources AS source ON source.post_id = job.post_id
    JOIN media AS current_media
      ON current_media.post_id = job.post_id
     AND current_media.processing_state = 1
    WHERE job.post_id = $1
      AND job.kind = $2
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
)
SELECT id, previous_state
FROM requeued
UNION ALL
SELECT id, state
FROM target
WHERE state = 0
LIMIT 1
`

func (s *Store) RequestRegeneration(ctx context.Context, postID int64) (regenerate.Requested, error) {
	var jobID int64
	var previousState int16
	if err := s.pool.QueryRow(
		ctx,
		requestRegenerationSQL,
		postID,
		mediaJobKindInitialProcess,
	).Scan(&jobID, &previousState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return regenerate.Requested{}, regenerate.ErrNotRegenerable
		}
		return regenerate.Requested{}, fmt.Errorf("request media regeneration: %w", err)
	}

	var outcome regenerate.Outcome
	switch previousState {
	case mediaJobStatePending:
		outcome = regenerate.OutcomeCoalesced
	case mediaJobStateRunning:
		outcome = regenerate.OutcomeSuperseded
	case mediaJobStateSucceeded, mediaJobStateFailed:
		outcome = regenerate.OutcomeQueued
	default:
		return regenerate.Requested{}, fmt.Errorf("request media regeneration returned unknown prior job state %d", previousState)
	}
	return regenerate.Requested{JobID: jobID, Outcome: outcome}, nil
}
