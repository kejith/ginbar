package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kejith/ginbar/backend/v2/internal/mediastatus"
)

const loadMediaStatusSQL = `
SELECT
    post.id,
    post.release_state,
    post.deleted_at IS NOT NULL,
    EXISTS (
        SELECT 1
        FROM media
        WHERE media.post_id = post.id
          AND media.processing_state = 1
    ) AS media_ready,
    job.state,
    job.attempts,
    job.max_attempts,
    job.available_at,
    job.lease_expires_at
FROM posts AS post
LEFT JOIN LATERAL (
    SELECT state, attempts, max_attempts, available_at, lease_expires_at
    FROM media_jobs
    WHERE post_id = post.id
      AND kind = $2
    ORDER BY id DESC
    LIMIT 1
) AS job ON true
WHERE post.id = $1
`

func (s *Store) LoadMediaStatus(ctx context.Context, postID int64) (mediastatus.Snapshot, error) {
	var snapshot mediastatus.Snapshot
	var state pgtype.Int2
	var attempts pgtype.Int4
	var maxAttempts pgtype.Int4
	var availableAt pgtype.Timestamptz
	var leaseExpiresAt pgtype.Timestamptz

	err := s.pool.QueryRow(ctx, loadMediaStatusSQL, postID, mediaJobKindInitialProcess).Scan(
		&snapshot.PostID,
		&snapshot.ReleaseState,
		&snapshot.Deleted,
		&snapshot.MediaReady,
		&state,
		&attempts,
		&maxAttempts,
		&availableAt,
		&leaseExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return mediastatus.Snapshot{}, mediastatus.ErrPostNotFound
	}
	if err != nil {
		return mediastatus.Snapshot{}, fmt.Errorf("load media status: %w", err)
	}
	if !state.Valid {
		return snapshot, nil
	}
	if !attempts.Valid || !maxAttempts.Valid || !availableAt.Valid {
		return mediastatus.Snapshot{}, fmt.Errorf("load media status: incomplete job state")
	}

	job := &mediastatus.JobSnapshot{
		State:       state.Int16,
		Attempts:    attempts.Int32,
		MaxAttempts: maxAttempts.Int32,
		AvailableAt: availableAt.Time,
	}
	if leaseExpiresAt.Valid {
		leaseExpiry := leaseExpiresAt.Time
		job.LeaseExpiresAt = &leaseExpiry
	}
	snapshot.Job = job
	return snapshot, nil
}
