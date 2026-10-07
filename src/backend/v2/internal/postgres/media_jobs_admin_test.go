package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/mediajobadmin"
	"github.com/kejith/ginbar/backend/v2/internal/role"
)

func TestListMediaJobsRequiresModeratorOrAdminAndPagesByJobID(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	authorID := createMediaJobsAdminUser(t, store, "jobs-author")
	memberID := createMediaJobsAdminUser(t, store, "jobs-member")
	moderatorID := createMediaJobsAdminUser(t, store, "jobs-moderator")
	adminID := createMediaJobsAdminUser(t, store, "jobs-admin")
	grantRole(t, store, moderatorID, role.Moderator)
	grantRole(t, store, adminID, role.Admin)

	now := time.Now().UTC().Truncate(time.Microsecond)
	queuedID := insertMediaJobsAdminJob(t, store, authorID, mediajobadmin.StatePending, 0, now, "", "")
	retryID := insertMediaJobsAdminJob(t, store, authorID, mediajobadmin.StatePending, 2, now.Add(time.Minute), "", "temporary failure")
	runningID := insertMediaJobsAdminJob(t, store, authorID, mediajobadmin.StateRunning, 1, now, "worker-a", "previous transient")
	succeededID := insertMediaJobsAdminJob(t, store, authorID, mediajobadmin.StateSucceeded, 1, now, "", "")
	failedID := insertMediaJobsAdminJob(t, store, authorID, mediajobadmin.StateFailed, 5, now, "", "terminal failure")

	if _, err := store.ListMediaJobs(ctx, memberID, 0, 2); !errors.Is(err, mediajobadmin.ErrForbidden) {
		t.Fatalf("member error=%v", err)
	}

	service := mediajobadmin.New(store)
	first, err := service.List(ctx, mediajobadmin.Query{ActorUserID: moderatorID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Jobs) != 2 || first.Jobs[0].ID != failedID || first.Jobs[1].ID != succeededID ||
		first.NextBefore != succeededID || first.Jobs[0].State != "failed" {
		t.Fatalf("first page=%#v", first)
	}
	if first.Jobs[0].LastError == nil || *first.Jobs[0].LastError != "terminal failure" {
		t.Fatalf("failed job metadata=%#v", first.Jobs[0])
	}

	second, err := service.List(ctx, mediajobadmin.Query{ActorUserID: moderatorID, Before: first.NextBefore, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Jobs) != 2 || second.Jobs[0].ID != runningID || second.Jobs[1].ID != retryID ||
		second.NextBefore != retryID {
		t.Fatalf("second page=%#v", second)
	}
	if second.Jobs[0].State != "running" || second.Jobs[0].ClaimedBy == nil ||
		*second.Jobs[0].ClaimedBy != "worker-a" || second.Jobs[0].LeaseExpiresAt == nil {
		t.Fatalf("running metadata=%#v", second.Jobs[0])
	}
	if second.Jobs[1].State != "pending" || second.Jobs[1].Attempts != 2 ||
		second.Jobs[1].LastError == nil || *second.Jobs[1].LastError != "temporary failure" ||
		!second.Jobs[1].AvailableAt.After(now) {
		t.Fatalf("retry metadata=%#v", second.Jobs[1])
	}
	for _, job := range second.Jobs {
		if job.ID >= first.NextBefore {
			t.Fatalf("cursor overlap first=%#v second=%#v", first, second)
		}
	}

	third, err := service.List(ctx, mediajobadmin.Query{ActorUserID: adminID, Before: second.NextBefore, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Jobs) != 1 || third.Jobs[0].ID != queuedID || third.NextBefore != 0 {
		t.Fatalf("third page=%#v", third)
	}
}

func TestListMediaJobsPlanUsesBoundedPrimaryKeyCursorScan(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	actorID := createMediaJobsAdminUser(t, store, "jobs-plan-moderator")
	authorID := createMediaJobsAdminUser(t, store, "jobs-plan-author")
	grantRole(t, store, actorID, role.Moderator)

	if _, err := store.pool.Exec(ctx, `
		WITH new_posts AS (
			INSERT INTO posts (author_user_id)
			SELECT $1
			FROM generate_series(1, 5000)
			RETURNING id
		)
		INSERT INTO media_jobs (post_id, kind, state, attempts)
		SELECT id, 0, 2, 1
		FROM new_posts
	`, authorID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, "ANALYZE media_jobs, user_roles"); err != nil {
		t.Fatal(err)
	}

	rows, err := store.pool.Query(
		ctx,
		"EXPLAIN (ANALYZE, BUFFERS) "+listMediaJobsSQL,
		actorID,
		int64(0),
		51,
		role.Moderator,
		role.Admin,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := collectPlan(t, rows)
	t.Logf("media jobs admin plan:\n%s", plan)
	assertPlanContains(t, plan, "media_jobs_pkey")
	assertPlanExcludes(t, plan, "Seq Scan on media_jobs", "external merge", "Disk:")
	if strings.Contains(strings.ToUpper(listMediaJobsSQL), "OFFSET") {
		t.Fatal("media job pagination must not use OFFSET")
	}
}

func createMediaJobsAdminUser(t *testing.T, store *Store, username string) int64 {
	t.Helper()
	var userID int64
	if err := store.pool.QueryRow(
		context.Background(),
		"INSERT INTO users (username) VALUES ($1) RETURNING id",
		username,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}

func insertMediaJobsAdminJob(
	t *testing.T,
	store *Store,
	authorID int64,
	state int16,
	attempts int32,
	availableAt time.Time,
	claimedBy string,
	lastError string,
) int64 {
	t.Helper()
	ctx := context.Background()
	var postID int64
	if err := store.pool.QueryRow(
		ctx,
		"INSERT INTO posts (author_user_id) VALUES ($1) RETURNING id",
		authorID,
	).Scan(&postID); err != nil {
		t.Fatal(err)
	}

	var claimedAt any
	var claimedByValue any
	var leaseExpiresAt any
	leaseGeneration := int64(0)
	if state == mediajobadmin.StateRunning {
		claimedAt = availableAt.Add(-time.Second)
		claimedByValue = claimedBy
		leaseExpiresAt = availableAt.Add(time.Minute)
		leaseGeneration = 1
	}
	var lastErrorValue any
	if lastError != "" {
		lastErrorValue = lastError
	}

	var jobID int64
	if err := store.pool.QueryRow(ctx, `
		INSERT INTO media_jobs (
			post_id,
			kind,
			state,
			attempts,
			max_attempts,
			available_at,
			claimed_at,
			claimed_by,
			lease_expires_at,
			lease_generation,
			last_error
		)
		VALUES ($1, 0, $2, $3, 5, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`,
		postID,
		state,
		attempts,
		availableAt,
		claimedAt,
		claimedByValue,
		leaseExpiresAt,
		leaseGeneration,
		lastErrorValue,
	).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	return jobID
}
