package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/ingest"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/regenerate"
)

type regenerationFixture struct {
	postID int64
	jobID  int64
	key    string
}

func readyRegenerationFixture(t *testing.T, store *Store, username string, jobState int16) regenerationFixture {
	t.Helper()
	ctx := context.Background()
	var userID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ($1) RETURNING id", username).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	body := []byte("ready regeneration source")
	digest := sha256.Sum256(body)
	created, err := store.CreateIngestion(ctx, ingest.CreateRequest{
		AuthorUserID: userID,
		Filter:       model.FilterSFW,
		Source: ingest.SourceRecord{
			Type:       ingest.SourceUpload,
			StorageKey: "sources/ab/ab23456789abcdef0123456789abcdef",
			ByteSize:   int64(len(body)),
			SHA256:     digest,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	key := "media/01/ready-regeneration.avif"
	outputDigest := sha256.Sum256([]byte("published media"))
	if _, err := store.pool.Exec(ctx,
		"UPDATE posts SET release_state=1, released_at=clock_timestamp() WHERE id=$1",
		created.PostID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO media (
			post_id, kind, processing_state, storage_key, mime_type,
			width, height, duration_ms, byte_size, sha256, perceptual_hash
		) VALUES ($1, 0, 1, $2, 'image/avif', 640, 480, 0, 15, $3, 12345)
	`, created.PostID, key, outputDigest[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		UPDATE media_jobs
		SET state = $1,
		    attempts = CASE WHEN $1 = 0 THEN 0 ELSE 1 END,
		    claimed_at = NULL,
		    claimed_by = NULL,
		    lease_expires_at = NULL,
		    last_error = CASE WHEN $1 = 3 THEN 'previous failure' ELSE NULL END
		WHERE id = $2
	`, jobState, created.JobID); err != nil {
		t.Fatal(err)
	}
	return regenerationFixture{postID: created.PostID, jobID: created.JobID, key: key}
}

func TestRequestRegenerationQueuesReleasedReadyPostWithoutHidingCurrentMedia(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	fixture := readyRegenerationFixture(t, store, "regen-ready", mediaJobStateSucceeded)
	ctx := context.Background()

	requested, err := store.RequestRegeneration(ctx, fixture.postID)
	if err != nil {
		t.Fatal(err)
	}
	if requested.JobID != fixture.jobID || requested.Outcome != regenerate.OutcomeQueued {
		t.Fatalf("request = %#v", requested)
	}

	var releaseState, processingState, jobState int16
	var storageKey string
	var attempts int32
	var claimedBy *string
	var leaseExpiresAt *time.Time
	if err := store.pool.QueryRow(ctx, `
		SELECT post.release_state, media.processing_state, media.storage_key,
		       job.state, job.attempts, job.claimed_by, job.lease_expires_at
		FROM posts AS post
		JOIN media ON media.post_id = post.id
		JOIN media_jobs AS job ON job.id = $2
		WHERE post.id = $1
	`, fixture.postID, fixture.jobID).Scan(
		&releaseState,
		&processingState,
		&storageKey,
		&jobState,
		&attempts,
		&claimedBy,
		&leaseExpiresAt,
	); err != nil {
		t.Fatal(err)
	}
	if releaseState != 1 || processingState != 1 || storageKey != fixture.key {
		t.Fatalf("published media changed while regeneration queued: release=%d processing=%d key=%q", releaseState, processingState, storageKey)
	}
	if jobState != mediaJobStatePending || attempts != 0 || claimedBy != nil || leaseExpiresAt != nil {
		t.Fatalf("job state after request = state:%d attempts:%d claimed:%v lease:%v", jobState, attempts, claimedBy, leaseExpiresAt)
	}

	var activeCount int
	if err := store.pool.QueryRow(ctx,
		"SELECT count(*) FROM media_jobs WHERE post_id=$1 AND kind=$2 AND state IN (0,1)",
		fixture.postID, mediaJobKindInitialProcess,
	).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 {
		t.Fatalf("active jobs = %d", activeCount)
	}
}

func TestRequestRegenerationCoalescesPendingWithoutResettingRetryState(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	fixture := readyRegenerationFixture(t, store, "regen-pending", mediaJobStatePending)
	ctx := context.Background()
	if _, err := store.pool.Exec(ctx, `
		UPDATE media_jobs
		SET attempts = 2,
		    available_at = clock_timestamp() + interval '1 hour',
		    last_error = 'retry later',
		    lease_generation = 4
		WHERE id = $1
	`, fixture.jobID); err != nil {
		t.Fatal(err)
	}

	requested, err := store.RequestRegeneration(ctx, fixture.postID)
	if err != nil {
		t.Fatal(err)
	}
	if requested.JobID != fixture.jobID || requested.Outcome != regenerate.OutcomeCoalesced {
		t.Fatalf("request = %#v", requested)
	}

	var attempts int32
	var lastError *string
	var generation int64
	if err := store.pool.QueryRow(ctx,
		"SELECT attempts, last_error, lease_generation FROM media_jobs WHERE id=$1",
		fixture.jobID,
	).Scan(&attempts, &lastError, &generation); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || lastError == nil || *lastError != "retry later" || generation != 4 {
		t.Fatalf("coalesced pending state changed: attempts=%d error=%v generation=%d", attempts, lastError, generation)
	}
}

func TestRequestRegenerationSupersedesRunningLeaseGeneration(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	fixture := readyRegenerationFixture(t, store, "regen-running", mediaJobStateSucceeded)
	ctx := context.Background()
	if _, err := store.pool.Exec(ctx, `
		UPDATE media_jobs
		SET state = 1,
		    attempts = 3,
		    claimed_at = clock_timestamp(),
		    claimed_by = 'old-worker',
		    lease_expires_at = clock_timestamp() + interval '30 seconds',
		    lease_generation = 7,
		    last_error = 'old attempt'
		WHERE id = $1
	`, fixture.jobID); err != nil {
		t.Fatal(err)
	}

	requested, err := store.RequestRegeneration(ctx, fixture.postID)
	if err != nil {
		t.Fatal(err)
	}
	if requested.Outcome != regenerate.OutcomeSuperseded {
		t.Fatalf("outcome = %v", requested.Outcome)
	}

	var state int16
	var attempts int32
	var generation int64
	var claimedBy, lastError *string
	var leaseExpiresAt *time.Time
	if err := store.pool.QueryRow(ctx, `
		SELECT state, attempts, lease_generation, claimed_by, lease_expires_at, last_error
		FROM media_jobs WHERE id=$1
	`, fixture.jobID).Scan(&state, &attempts, &generation, &claimedBy, &leaseExpiresAt, &lastError); err != nil {
		t.Fatal(err)
	}
	if state != mediaJobStatePending || attempts != 0 || generation != 8 || claimedBy != nil || leaseExpiresAt != nil || lastError != nil {
		t.Fatalf("superseded state = state:%d attempts:%d generation:%d claimed:%v lease:%v error:%v", state, attempts, generation, claimedBy, leaseExpiresAt, lastError)
	}
}

func TestRequestRegenerationRequeuesFailedJob(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	fixture := readyRegenerationFixture(t, store, "regen-failed", mediaJobStateFailed)
	ctx := context.Background()

	requested, err := store.RequestRegeneration(ctx, fixture.postID)
	if err != nil {
		t.Fatal(err)
	}
	if requested.Outcome != regenerate.OutcomeQueued {
		t.Fatalf("outcome = %v", requested.Outcome)
	}
	var state int16
	var attempts int32
	var lastError *string
	if err := store.pool.QueryRow(ctx,
		"SELECT state, attempts, last_error FROM media_jobs WHERE id=$1",
		fixture.jobID,
	).Scan(&state, &attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if state != mediaJobStatePending || attempts != 0 || lastError != nil {
		t.Fatalf("requeued failed state = state:%d attempts:%d error:%v", state, attempts, lastError)
	}
}

func TestRequestRegenerationDoesNotDisturbInitialProcessingWithoutReadyMedia(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	ctx := context.Background()
	var userID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ('regen-initial') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("initial source"))
	created, err := store.CreateIngestion(ctx, ingest.CreateRequest{
		AuthorUserID: userID,
		Filter:       model.FilterSFW,
		Source: ingest.SourceRecord{
			Type:       ingest.SourceUpload,
			StorageKey: "sources/cd/cd23456789abcdef0123456789abcdef",
			ByteSize:   14,
			SHA256:     digest,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.RequestRegeneration(ctx, created.PostID)
	if !errors.Is(err, regenerate.ErrNotRegenerable) {
		t.Fatalf("error = %v", err)
	}
	var state int16
	var attempts int32
	var generation int64
	if err := store.pool.QueryRow(ctx,
		"SELECT state, attempts, lease_generation FROM media_jobs WHERE id=$1",
		created.JobID,
	).Scan(&state, &attempts, &generation); err != nil {
		t.Fatal(err)
	}
	if state != mediaJobStatePending || attempts != 0 || generation != 0 {
		t.Fatalf("initial job changed: state=%d attempts=%d generation=%d", state, attempts, generation)
	}
}
