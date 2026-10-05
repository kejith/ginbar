package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/ingest"
	"github.com/kejith/ginbar/backend/v2/internal/mediastatus"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

func TestLoadMediaStatusReadsAuthoritativeInitialIngestion(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()

	ctx := context.Background()
	var userID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ('status-ingest-user') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("status-source"))
	created, err := store.CreateIngestion(ctx, ingest.CreateRequest{
		AuthorUserID: userID,
		Filter:       model.FilterSFW,
		Source: ingest.SourceRecord{
			Type:       ingest.SourceUpload,
			StorageKey: "sources/aa/11111111111111111111111111111111",
			ByteSize:   int64(len("status-source")),
			SHA256:     hash,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.LoadMediaStatus(ctx, created.PostID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ReleaseState != 0 || snapshot.Deleted || snapshot.MediaReady || snapshot.Job == nil {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Job.State != mediastatus.JobStatePending || snapshot.Job.Attempts != 0 || snapshot.Job.MaxAttempts != 5 {
		t.Fatalf("job = %#v", snapshot.Job)
	}
	status, err := mediastatus.New(store).Get(ctx, created.PostID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != mediastatus.PhaseWaiting || status.Operation != mediastatus.OperationInitial || status.UsableMedia {
		t.Fatalf("status = %#v", status)
	}
	if _, err := mediastatus.New(store).Public(ctx, created.PostID); !errors.Is(err, mediastatus.ErrPostNotFound) {
		t.Fatalf("public initial-ingestion error = %v", err)
	}
}

func TestLoadMediaStatusUsesLatestKindZeroJobAndDoesNotTouchLease(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()

	ctx := context.Background()
	var userID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ('status-released-user') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var postID int64
	if err := store.pool.QueryRow(ctx, `
		INSERT INTO posts (author_user_id, release_state, released_at)
		VALUES ($1, 1, clock_timestamp())
		RETURNING id
	`, userID).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	mediaHash := sha256.Sum256([]byte("ready-media"))
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO media (
			post_id, kind, processing_state, storage_key, mime_type,
			width, height, byte_size, sha256
		) VALUES ($1, 0, 1, $2, 'image/avif', 64, 64, 11, $3)
	`, postID, "processed/aa/status.avif", mediaHash[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO media_jobs (post_id, kind, state, attempts, max_attempts)
		VALUES ($1, 0, 2, 1, 5), ($1, 0, 3, 2, 5)
	`, postID); err != nil {
		t.Fatal(err)
	}
	leaseExpiry := time.Now().Add(30 * time.Second).UTC().Truncate(time.Microsecond)
	var runningJobID int64
	if err := store.pool.QueryRow(ctx, `
		INSERT INTO media_jobs (
			post_id, kind, state, attempts, max_attempts,
			claimed_at, claimed_by, lease_expires_at, lease_generation
		)
		VALUES ($1, 0, 1, 3, 5, clock_timestamp(), 'status-worker', $2, 9)
		RETURNING id
	`, postID, leaseExpiry).Scan(&runningJobID); err != nil {
		t.Fatal(err)
	}

	var updatedBefore time.Time
	if err := store.pool.QueryRow(ctx, "SELECT updated_at FROM media_jobs WHERE id = $1", runningJobID).Scan(&updatedBefore); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.LoadMediaStatus(ctx, postID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.MediaReady || snapshot.Job == nil || snapshot.Job.State != mediastatus.JobStateRunning || snapshot.Job.Attempts != 3 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Job.LeaseExpiresAt == nil || !snapshot.Job.LeaseExpiresAt.Equal(leaseExpiry) {
		t.Fatalf("lease expiry = %v, want %v", snapshot.Job.LeaseExpiresAt, leaseExpiry)
	}
	var updatedAfter time.Time
	if err := store.pool.QueryRow(ctx, "SELECT updated_at FROM media_jobs WHERE id = $1", runningJobID).Scan(&updatedAfter); err != nil {
		t.Fatal(err)
	}
	if !updatedAfter.Equal(updatedBefore) {
		t.Fatalf("status read changed updated_at: before=%v after=%v", updatedBefore, updatedAfter)
	}

	status, err := mediastatus.New(store).Public(ctx, postID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != mediastatus.PhaseProcessing || status.Operation != mediastatus.OperationRegeneration || !status.UsableMedia {
		t.Fatalf("status = %#v", status)
	}
}
