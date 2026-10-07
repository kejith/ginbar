package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kejith/ginbar/backend/v2/internal/ingest"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/regenerate"
	"github.com/kejith/ginbar/backend/v2/internal/role"
)

type regenerationFixture struct {
	postID int64
	jobID  int64
	key    string
}

func regenerationActor(t *testing.T, store *Store, username string, actorRole int16) int64 {
	t.Helper()
	ctx := context.Background()
	var userID int64
	if err := store.pool.QueryRow(
		ctx,
		"INSERT INTO users (username) VALUES ($1) RETURNING id",
		username,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(
		ctx,
		"INSERT INTO user_roles (user_id, role) VALUES ($1, $2)",
		userID,
		actorRole,
	); err != nil {
		t.Fatal(err)
	}
	return userID
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
		SET state = $1::smallint,
		    attempts = CASE WHEN $1::smallint = 0 THEN 0 ELSE 1 END,
		    claimed_at = NULL,
		    claimed_by = NULL,
		    lease_expires_at = NULL,
		    last_error = CASE WHEN $1::smallint = 3 THEN 'previous failure' ELSE NULL END
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
	adminID := regenerationActor(t, store, "regen-ready-admin", role.Admin)
	ctx := context.Background()

	requested, err := store.RequestRegeneration(ctx, adminID, fixture.postID)
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
	adminID := regenerationActor(t, store, "regen-pending-admin", role.Admin)
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

	var availableBefore time.Time
	if err := store.pool.QueryRow(
		ctx,
		"SELECT available_at FROM media_jobs WHERE id=$1",
		fixture.jobID,
	).Scan(&availableBefore); err != nil {
		t.Fatal(err)
	}

	requested, err := store.RequestRegeneration(ctx, adminID, fixture.postID)
	if err != nil {
		t.Fatal(err)
	}
	if requested.JobID != fixture.jobID || requested.Outcome != regenerate.OutcomeCoalesced {
		t.Fatalf("request = %#v", requested)
	}

	var attempts int32
	var availableAfter time.Time
	var lastError *string
	var generation int64
	if err := store.pool.QueryRow(ctx,
		"SELECT attempts, available_at, last_error, lease_generation FROM media_jobs WHERE id=$1",
		fixture.jobID,
	).Scan(&attempts, &availableAfter, &lastError, &generation); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || !availableAfter.Equal(availableBefore) ||
		lastError == nil || *lastError != "retry later" || generation != 4 {
		t.Fatalf(
			"coalesced pending state changed: attempts=%d available=%v/%v error=%v generation=%d",
			attempts,
			availableBefore,
			availableAfter,
			lastError,
			generation,
		)
	}
}

func TestRequestRegenerationSupersedesRunningLeaseGeneration(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	fixture := readyRegenerationFixture(t, store, "regen-running", mediaJobStateSucceeded)
	adminID := regenerationActor(t, store, "regen-running-admin", role.Admin)
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

	requested, err := store.RequestRegeneration(ctx, adminID, fixture.postID)
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

	var completedID int64
	err = store.pool.QueryRow(ctx, `
		WITH owned AS MATERIALIZED (
			SELECT id, lease_expires_at
			FROM media_jobs
			WHERE id = $1
			  AND state = 1
			  AND claimed_by = $2
			  AND lease_generation = $3
			FOR UPDATE
		)
		UPDATE media_jobs AS job
		SET state = 2
		FROM owned
		WHERE job.id = owned.id
		  AND owned.lease_expires_at > clock_timestamp()
		RETURNING job.id
	`, fixture.jobID, "old-worker", int64(7)).Scan(&completedID)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("superseded old lease completed job id=%d err=%v", completedID, err)
	}
}

func TestRequestRegenerationRequeuesFailedJob(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	fixture := readyRegenerationFixture(t, store, "regen-failed", mediaJobStateFailed)
	adminID := regenerationActor(t, store, "regen-failed-admin", role.Admin)
	ctx := context.Background()

	requested, err := store.RequestRegeneration(ctx, adminID, fixture.postID)
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

	adminID := regenerationActor(t, store, "regen-initial-admin", role.Admin)
	_, err = store.RequestRegeneration(ctx, adminID, created.PostID)
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

func TestRequestRegenerationRequiresAdminWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		actorRole int16
	}{
		{name: "member", actorRole: role.Member},
		{name: "moderator", actorRole: role.Moderator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, cleanup := testIngestionStore(t)
			defer cleanup()
			ctx := context.Background()
			fixture := readyRegenerationFixture(t, store, "regen-auth-target-"+tc.name, mediaJobStateSucceeded)
			actorID := regenerationActor(t, store, "regen-auth-actor-"+tc.name, tc.actorRole)

			_, err := store.RequestRegeneration(ctx, actorID, fixture.postID)
			if !errors.Is(err, regenerate.ErrForbidden) {
				t.Fatalf("error=%v", err)
			}
			var state int16
			var attempts int32
			var generation int64
			if err := store.pool.QueryRow(
				ctx,
				"SELECT state, attempts, lease_generation FROM media_jobs WHERE id=$1",
				fixture.jobID,
			).Scan(&state, &attempts, &generation); err != nil {
				t.Fatal(err)
			}
			if state != mediaJobStateSucceeded || attempts != 1 || generation != 0 {
				t.Fatalf(
					"forbidden request mutated job: state=%d attempts=%d generation=%d",
					state,
					attempts,
					generation,
				)
			}
		})
	}
}

func TestRequestRegenerationMissingPostIsStableNotRegenerable(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	adminID := regenerationActor(t, store, "regen-missing-admin", role.Admin)

	_, err := store.RequestRegeneration(context.Background(), adminID, 9_999_999)
	if !errors.Is(err, regenerate.ErrNotRegenerable) {
		t.Fatalf("error=%v", err)
	}
}

func TestRequestRegenerationConcurrentRequestsCoalesceToOneActiveJob(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	ctx := context.Background()
	fixture := readyRegenerationFixture(t, store, "regen-concurrent", mediaJobStateSucceeded)
	adminID := regenerationActor(t, store, "regen-concurrent-admin", role.Admin)

	const requestCount = 12
	start := make(chan struct{})
	results := make(chan struct {
		requested regenerate.Requested
		err       error
	}, requestCount)
	var wg sync.WaitGroup
	for range requestCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			requested, err := store.RequestRegeneration(ctx, adminID, fixture.postID)
			results <- struct {
				requested regenerate.Requested
				err       error
			}{requested: requested, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	queued := 0
	coalesced := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent request error=%v", result.err)
		}
		if result.requested.JobID != fixture.jobID {
			t.Fatalf("job id=%d want=%d", result.requested.JobID, fixture.jobID)
		}
		switch result.requested.Outcome {
		case regenerate.OutcomeQueued:
			queued++
		case regenerate.OutcomeCoalesced:
			coalesced++
		default:
			t.Fatalf("unexpected concurrent outcome=%q", result.requested.Outcome)
		}
	}
	if queued != 1 || coalesced != requestCount-1 {
		t.Fatalf("queued=%d coalesced=%d", queued, coalesced)
	}

	var activeCount int
	if err := store.pool.QueryRow(
		ctx,
		"SELECT count(*) FROM media_jobs WHERE post_id=$1 AND kind=$2 AND state IN (0,1)",
		fixture.postID,
		mediaJobKindInitialProcess,
	).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 {
		t.Fatalf("active jobs=%d", activeCount)
	}

	var releaseState, processingState int16
	var storageKey string
	if err := store.pool.QueryRow(ctx, `
		SELECT post.release_state, media.processing_state, media.storage_key
		FROM posts AS post
		JOIN media ON media.post_id = post.id
		WHERE post.id = $1
	`, fixture.postID).Scan(&releaseState, &processingState, &storageKey); err != nil {
		t.Fatal(err)
	}
	if releaseState != 1 || processingState != 1 || storageKey != fixture.key {
		t.Fatalf(
			"published media changed under concurrent requests: release=%d processing=%d key=%q",
			releaseState,
			processingState,
			storageKey,
		)
	}
}

func TestRequestRegenerationPlanIsBoundedAndIndexBacked(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	ctx := context.Background()
	fixture := readyRegenerationFixture(t, store, "regen-plan-target", mediaJobStateSucceeded)
	adminID := regenerationActor(t, store, "regen-plan-admin", role.Admin)
	authorID := regenerationActor(t, store, "regen-plan-author", role.Member)

	if _, err := store.pool.Exec(ctx, `
		WITH inserted AS (
			INSERT INTO users (username)
			SELECT 'regen-plan-role-' || g::text
			FROM generate_series(1, 5000) AS g
			RETURNING id
		)
		INSERT INTO user_roles (user_id, role)
		SELECT id, CASE WHEN id % 2 = 0 THEN $1::smallint ELSE $2::smallint END
		FROM inserted
	`, role.Member, role.Moderator); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		WITH new_posts AS (
			INSERT INTO posts (author_user_id, release_state, released_at)
			SELECT $1, 1, clock_timestamp()
			FROM generate_series(1, 5000)
			RETURNING id
		), sources AS (
			INSERT INTO media_sources (
				post_id, source_type, storage_key, byte_size, sha256
			)
			SELECT
				id,
				0,
				'sources/regen-plan/' || id::text,
				1,
				decode(repeat('11', 32), 'hex')
			FROM new_posts
			RETURNING post_id
		), ready_media AS (
			INSERT INTO media (
				post_id, kind, processing_state, storage_key, mime_type,
				width, height, duration_ms, byte_size, sha256
			)
			SELECT
				post_id,
				0,
				1,
				'media/regen-plan/' || post_id::text || '.avif',
				'image/avif',
				64,
				64,
				0,
				1,
				decode(repeat('22', 32), 'hex')
			FROM sources
			RETURNING post_id
		)
		INSERT INTO media_jobs (post_id, kind, state, attempts)
		SELECT post_id, 0, 2, 1
		FROM ready_media
	`, authorID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(
		ctx,
		"ANALYZE user_roles, posts, media_sources, media, media_jobs",
	); err != nil {
		t.Fatal(err)
	}

	rows, err := store.pool.Query(
		ctx,
		"EXPLAIN (ANALYZE, BUFFERS) "+requestRegenerationSQL,
		adminID,
		fixture.postID,
		mediaJobKindInitialProcess,
		role.Admin,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := collectPlan(t, rows)
	t.Logf("authorized regeneration plan:\n%s", plan)
	for _, indexName := range []string{
		"user_roles_pkey",
		"media_jobs_post_kind_id_idx",
		"posts_pkey",
		"media_sources_pkey",
		"media_pkey",
	} {
		assertPlanContains(t, plan, indexName)
	}
	assertPlanExcludes(
		t,
		plan,
		"Seq Scan on user_roles",
		"Seq Scan on media_jobs",
		"Seq Scan on posts",
		"Seq Scan on media_sources",
		"Seq Scan on media",
		"external merge",
		"Disk:",
	)
	if strings.Contains(strings.ToUpper(requestRegenerationSQL), "OFFSET") {
		t.Fatal("regeneration mutation must not use OFFSET")
	}
}
