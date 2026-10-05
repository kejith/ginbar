package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/comment"
)

func TestListCommentsDistinguishesEmptyPostFromUnavailablePost(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	authorID := createVoteUser(t, store, "comment-empty")
	postID := createReleasedVotePost(t, store, authorID, 0)

	got, err := store.ListComments(ctx, comment.Query{PostID: postID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("comments=%#v", got)
	}

	if _, err := store.ListComments(ctx, comment.Query{PostID: postID + 999999, Limit: 10}); !errors.Is(err, comment.ErrPostNotFound) {
		t.Fatalf("missing post error=%v", err)
	}

	var unreleasedID int64
	if err := store.pool.QueryRow(ctx, `
		INSERT INTO posts (author_user_id, release_state)
		VALUES ($1, 0)
		RETURNING id
	`, authorID).Scan(&unreleasedID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListComments(ctx, comment.Query{PostID: unreleasedID, Limit: 10}); !errors.Is(err, comment.ErrPostNotFound) {
		t.Fatalf("unreleased post error=%v", err)
	}
}

func TestListCommentsIsIDAscendingBoundedAndPreservesDeletedParents(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	authorID := createVoteUser(t, store, "comment-list")
	postID := createReleasedVotePost(t, store, authorID, 0)

	top := insertComment(t, store, postID, authorID, nil, "top")
	child := insertComment(t, store, postID, authorID, &top, "child")
	sibling := insertComment(t, store, postID, authorID, &top, "sibling")
	grandchild := insertComment(t, store, postID, authorID, &child, "grandchild")
	if _, err := store.pool.Exec(ctx, "UPDATE comments SET deleted_at = now() WHERE id = $1", top); err != nil {
		t.Fatal(err)
	}

	service := comment.New(store)
	first, err := service.List(ctx, comment.Query{PostID: postID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Comments) != 2 || first.Comments[0].ID != top || first.Comments[1].ID != child || first.NextAfter != child {
		t.Fatalf("first page=%#v", first)
	}
	if !first.Comments[0].Deleted || first.Comments[0].Body != nil {
		t.Fatalf("deleted parent was not a tombstone: %#v", first.Comments[0])
	}
	if first.Comments[1].ParentCommentID == nil || *first.Comments[1].ParentCommentID != top {
		t.Fatalf("child parent=%#v", first.Comments[1])
	}

	second, err := service.List(ctx, comment.Query{PostID: postID, After: first.NextAfter, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Comments) != 2 || second.Comments[0].ID != sibling || second.Comments[1].ID != grandchild || second.NextAfter != 0 {
		t.Fatalf("second page=%#v", second)
	}
	if second.Comments[1].ParentCommentID == nil || *second.Comments[1].ParentCommentID != child {
		t.Fatalf("grandchild parent=%#v", second.Comments[1])
	}
}

func TestCreateCommentTopLevelAndNestedAuthoritativeResult(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	authorID := createVoteUser(t, store, "comment-create")
	postID := createReleasedVotePost(t, store, authorID, 0)
	service := comment.New(store)

	top, err := service.Create(ctx, comment.CreateRequest{PostID: postID, UserID: authorID, Body: "top level"})
	if err != nil {
		t.Fatal(err)
	}
	if top.ID <= 0 || top.PostID != postID || top.AuthorID != authorID || top.ParentCommentID != nil || top.Body == nil || *top.Body != "top level" || top.Score != 0 || top.Deleted || top.CreatedAt.IsZero() {
		t.Fatalf("top=%#v", top)
	}

	reply, err := service.Create(ctx, comment.CreateRequest{PostID: postID, UserID: authorID, ParentCommentID: &top.ID, Body: "reply"})
	if err != nil {
		t.Fatal(err)
	}
	if reply.ID <= top.ID || reply.PostID != postID || reply.AuthorID != authorID || reply.ParentCommentID == nil || *reply.ParentCommentID != top.ID || reply.Body == nil || *reply.Body != "reply" || reply.Score != 0 || reply.Deleted || reply.CreatedAt.IsZero() {
		t.Fatalf("reply=%#v", reply)
	}
}

func TestCreateCommentRejectsInvalidPostParentAndDeletedParent(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := createVoteUser(t, store, "comment-errors")
	postID := createReleasedVotePost(t, store, userID, 0)
	otherPostID := createReleasedVotePost(t, store, userID, 0)
	service := comment.New(store)

	if _, err := service.Create(ctx, comment.CreateRequest{PostID: postID + 999999, UserID: userID, Body: "missing"}); !errors.Is(err, comment.ErrPostNotFound) {
		t.Fatalf("missing post error=%v", err)
	}

	missingParent := int64(999999999)
	if _, err := service.Create(ctx, comment.CreateRequest{PostID: postID, UserID: userID, ParentCommentID: &missingParent, Body: "missing parent"}); !errors.Is(err, comment.ErrParentCommentNotFound) {
		t.Fatalf("missing parent error=%v", err)
	}

	otherParent := insertComment(t, store, otherPostID, userID, nil, "other post")
	if _, err := service.Create(ctx, comment.CreateRequest{PostID: postID, UserID: userID, ParentCommentID: &otherParent, Body: "cross post"}); !errors.Is(err, comment.ErrParentCommentNotFound) {
		t.Fatalf("cross-post parent error=%v", err)
	}

	deletedParent := insertComment(t, store, postID, userID, nil, "deleted")
	if _, err := store.pool.Exec(ctx, "UPDATE comments SET deleted_at = now() WHERE id = $1", deletedParent); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, comment.CreateRequest{PostID: postID, UserID: userID, ParentCommentID: &deletedParent, Body: "reply deleted"}); !errors.Is(err, comment.ErrParentCommentDeleted) {
		t.Fatalf("deleted parent error=%v", err)
	}
}

func TestCommentSQLPlansStayBoundedAndIndexed(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := createVoteUser(t, store, "comment-plan")

	if _, err := store.pool.Exec(ctx, `
		INSERT INTO posts (author_user_id, release_state)
		SELECT $1, 0
		FROM generate_series(1, 5000)
	`, userID); err != nil {
		t.Fatal(err)
	}
	noisePostID := createReleasedVotePost(t, store, userID, 0)
	targetPostID := createReleasedVotePost(t, store, userID, 0)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO comments (post_id, user_id, body)
		SELECT $1, $2, 'noise-' || value
		FROM generate_series(1, 20000) AS value
	`, noisePostID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO comments (post_id, user_id, body)
		SELECT $1, $2, 'target-' || value
		FROM generate_series(1, 20000) AS value
	`, targetPostID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, "ANALYZE posts, comments"); err != nil {
		t.Fatal(err)
	}

	readPlan := explainPlan(t, store, "EXPLAIN (ANALYZE, BUFFERS) "+listCommentsSQL, targetPostID, int64(0), 101)
	assertPlanContains(t, readPlan, "comments_post_idx")
	assertPlanExcludes(t, readPlan, "Seq Scan on comments", "external merge", "Disk:")

	var parentID int64
	if err := store.pool.QueryRow(ctx, "SELECT min(id) FROM comments WHERE post_id = $1", targetPostID).Scan(&parentID); err != nil {
		t.Fatal(err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+createCommentSQL, targetPostID, userID, parentID, "plan reply")
	if err != nil {
		t.Fatal(err)
	}
	createPlan := collectPlan(t, rows)
	assertPlanContains(t, createPlan, "comments_pkey")
	assertPlanContains(t, createPlan, "posts_pkey")
	assertPlanExcludes(t, createPlan, "Seq Scan on comments", "external merge", "Disk:")
}

func insertComment(t *testing.T, store *Store, postID, userID int64, parentID *int64, body string) int64 {
	t.Helper()
	var parent any
	if parentID != nil {
		parent = *parentID
	}
	var id int64
	if err := store.pool.QueryRow(context.Background(), `
		INSERT INTO comments (post_id, user_id, parent_comment_id, body)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, postID, userID, parent, body).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func explainPlan(t *testing.T, store *Store, sql string, args ...any) string {
	t.Helper()
	rows, err := store.pool.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	return collectPlan(t, rows)
}

func collectPlan(t *testing.T, rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}) string {
	t.Helper()
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func assertPlanContains(t *testing.T, plan, needle string) {
	t.Helper()
	if !strings.Contains(plan, needle) {
		t.Fatalf("plan missing %q:\n%s", needle, plan)
	}
}

func assertPlanExcludes(t *testing.T, plan string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if strings.Contains(plan, needle) {
			t.Fatalf("plan unexpectedly contains %q:\n%s", needle, plan)
		}
	}
}
