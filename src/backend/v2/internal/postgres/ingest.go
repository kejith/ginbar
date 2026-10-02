package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kejith/ginbar/backend/v2/internal/ingest"
)

const mediaJobKindInitialProcess int16 = 0

func (s *Store) CreateIngestion(ctx context.Context, request ingest.CreateRequest) (ingest.Created, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ingest.Created{}, fmt.Errorf("begin ingestion transaction: %w", err)
	}
	defer rollbackWithTimeout(tx)

	var sourceURL any
	if request.Source.Type == ingest.SourceURL {
		sourceURL = request.Source.URL
	}
	var originalName any
	if request.Source.OriginalName != "" {
		originalName = request.Source.OriginalName
	}

	var created ingest.Created
	if err := tx.QueryRow(ctx, `
		WITH new_post AS (
			INSERT INTO posts (author_user_id, content_filter, release_state)
			VALUES ($1, $2, 0)
			RETURNING id
		), new_source AS (
			INSERT INTO media_sources (
				post_id,
				source_type,
				storage_key,
				source_url,
				original_name,
				declared_mime_type,
				byte_size,
				sha256
			)
			SELECT id, $3, $4, $5, $6, $7, $8, $9
			FROM new_post
			RETURNING post_id
		), new_job AS (
			INSERT INTO media_jobs (post_id, kind)
			SELECT post_id, $10
			FROM new_source
			RETURNING id, post_id
		)
		SELECT post_id, id
		FROM new_job
	`,
		request.AuthorUserID,
		int16(request.Filter),
		int16(request.Source.Type),
		request.Source.StorageKey,
		sourceURL,
		originalName,
		request.Source.DeclaredMIME,
		request.Source.ByteSize,
		request.Source.SHA256[:],
		mediaJobKindInitialProcess,
	).Scan(&created.PostID, &created.JobID); err != nil {
		return ingest.Created{}, fmt.Errorf("create processing post/source/job: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return ingest.Created{}, fmt.Errorf("commit ingestion transaction rolled back: %w", err)
		}
		return ingest.Created{}, fmt.Errorf("%w: commit ingestion transaction: %v", ingest.ErrCommitOutcomeUnknown, err)
	}
	return created, nil
}

func rollbackWithTimeout(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
