package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/kejith/ginbar/backend/v2/internal/mediajobadmin"
	"github.com/kejith/ginbar/backend/v2/internal/role"
)

const listMediaJobsSQL = `
WITH actor AS MATERIALIZED (
	SELECT EXISTS (
		SELECT 1
		FROM user_roles
		WHERE user_id = $1
		  AND role IN ($4, $5)
	) AS allowed
), jobs AS MATERIALIZED (
	SELECT
		id,
		post_id,
		kind,
		state,
		priority,
		attempts,
		max_attempts,
		available_at,
		claimed_at,
		claimed_by,
		lease_expires_at,
		lease_generation,
		last_error,
		created_at,
		updated_at
	FROM media_jobs
	WHERE (SELECT allowed FROM actor)
	  AND ($2::bigint = 0 OR id < $2)
	ORDER BY id DESC
	LIMIT $3
)
SELECT
	actor.allowed,
	jobs.id,
	jobs.post_id,
	jobs.kind,
	jobs.state,
	jobs.priority,
	jobs.attempts,
	jobs.max_attempts,
	jobs.available_at,
	jobs.claimed_at,
	jobs.claimed_by,
	jobs.lease_expires_at,
	jobs.lease_generation,
	jobs.last_error,
	jobs.created_at,
	jobs.updated_at
FROM actor
LEFT JOIN jobs ON true
ORDER BY jobs.id DESC NULLS LAST
`

func (s *Store) ListMediaJobs(
	ctx context.Context,
	actorUserID, before int64,
	limit int,
) ([]mediajobadmin.Record, error) {
	rows, err := s.pool.Query(
		ctx,
		listMediaJobsSQL,
		actorUserID,
		before,
		limit,
		role.Moderator,
		role.Admin,
	)
	if err != nil {
		return nil, fmt.Errorf("list media jobs: %w", err)
	}
	defer rows.Close()

	records := make([]mediajobadmin.Record, 0, limit)
	seenAuthorization := false
	for rows.Next() {
		var allowed bool
		var (
			id              pgtype.Int8
			postID          pgtype.Int8
			kind            pgtype.Int2
			state           pgtype.Int2
			priority        pgtype.Int2
			attempts        pgtype.Int4
			maxAttempts     pgtype.Int4
			availableAt     pgtype.Timestamptz
			claimedAt       pgtype.Timestamptz
			claimedBy       pgtype.Text
			leaseExpiresAt  pgtype.Timestamptz
			leaseGeneration pgtype.Int8
			lastError       pgtype.Text
			createdAt       pgtype.Timestamptz
			updatedAt       pgtype.Timestamptz
		)
		if err := rows.Scan(
			&allowed,
			&id,
			&postID,
			&kind,
			&state,
			&priority,
			&attempts,
			&maxAttempts,
			&availableAt,
			&claimedAt,
			&claimedBy,
			&leaseExpiresAt,
			&leaseGeneration,
			&lastError,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan media job: %w", err)
		}
		seenAuthorization = true
		if !allowed {
			return nil, mediajobadmin.ErrForbidden
		}
		if !id.Valid {
			continue
		}
		if !postID.Valid || !kind.Valid || !state.Valid || !priority.Valid || !attempts.Valid ||
			!maxAttempts.Valid || !availableAt.Valid || !leaseGeneration.Valid ||
			!createdAt.Valid || !updatedAt.Valid {
			return nil, fmt.Errorf("scan media job: incomplete authoritative state")
		}

		records = append(records, mediajobadmin.Record{
			ID:              id.Int64,
			PostID:          postID.Int64,
			Kind:            kind.Int16,
			State:           state.Int16,
			Priority:        priority.Int16,
			Attempts:        attempts.Int32,
			MaxAttempts:     maxAttempts.Int32,
			AvailableAt:     availableAt.Time,
			ClaimedAt:       optionalTime(claimedAt),
			ClaimedBy:       optionalText(claimedBy),
			LeaseExpiresAt:  optionalTime(leaseExpiresAt),
			LeaseGeneration: leaseGeneration.Int64,
			LastError:       optionalText(lastError),
			CreatedAt:       createdAt.Time,
			UpdatedAt:       updatedAt.Time,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read media jobs: %w", err)
	}
	if !seenAuthorization {
		return nil, fmt.Errorf("list media jobs: missing authorization result")
	}
	return records, nil
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func optionalText(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
