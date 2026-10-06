package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/comment"
	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/moderation"
	"github.com/kejith/ginbar/backend/v2/internal/role"
)

func grantRole(t *testing.T, store *Store, userID int64, value int16) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO user_roles (user_id, role)
		VALUES ($1, $2)
	`, userID, value); err != nil {
		t.Fatal(err)
	}
}

func TestHidePostRequiresModeratorOrAdminAndImmediatelyLeavesPublicFeed(t *testing.T) {
	for _, roleValue := range []int16{role.Moderator, role.Admin} {
		t.Run(fmt.Sprintf("role-%d", roleValue), func(t *testing.T) {
			store, cleanup := testAuthStore(t)
			defer cleanup()
			ctx := context.Background()

			authorID := createVoteUser(t, store, "moderation-author")
			postID := createReleasedVotePost(t, store, authorID, 0)
			ordinaryID := createVoteUser(t, store, "moderation-ordinary")
			if _, err := store.HidePost(ctx, ordinaryID, postID); !errors.Is(err, moderation.ErrForbidden) {
				t.Fatalf("ordinary error=%v", err)
			}

			actorID := createVoteUser(t, store, "moderation-actor")
			grantRole(t, store, actorID, roleValue)
			first, err := store.HidePost(ctx, actorID, postID)
			if err != nil {
				t.Fatal(err)
			}
			second, err := store.HidePost(ctx, actorID, postID)
			if err != nil {
				t.Fatal(err)
			}
			if first.PostID != postID || !first.Deleted || first.ModeratedByUserID != actorID || first.ModeratedAt.IsZero() {
				t.Fatalf("first=%#v", first)
			}
			if second.ModeratedByUserID != first.ModeratedByUserID || !second.ModeratedAt.Equal(first.ModeratedAt) {
				t.Fatalf("idempotent result first=%#v second=%#v", first, second)
			}

			posts, err := store.ListFeed(ctx, feed.Query{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			for _, post := range posts {
				if post.ID == postID {
					t.Fatalf("moderated post remained in public feed: %#v", posts)
				}
			}
			if _, err := feed.New(store).Around(ctx, feed.AroundQuery{PostID: postID, Radius: 3}); !errors.Is(err, feed.ErrPostNotFound) {
				t.Fatalf("around moderated post error=%v", err)
			}
		})
	}
}

func TestHidePostConcurrentRepeatsPreserveFirstAuditIdentity(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	authorID := createVoteUser(t, store, "moderation-race-author")
	postID := createReleasedVotePost(t, store, authorID, 0)
	actorID := createVoteUser(t, store, "moderation-race-actor")
	grantRole(t, store, actorID, role.Moderator)

	const workers = 12
	start := make(chan struct{})
	results := make(chan moderation.PostResult, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := store.HidePost(ctx, actorID, postID)
			results <- result
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent hide failed: %v", err)
		}
	}
	var first moderation.PostResult
	for result := range results {
		if first.PostID == 0 {
			first = result
			continue
		}
		if result.ModeratedByUserID != first.ModeratedByUserID || !result.ModeratedAt.Equal(first.ModeratedAt) {
			t.Fatalf("audit state diverged: first=%#v result=%#v", first, result)
		}
	}
}

func TestHideCommentCreatesStructuralTombstoneAndRejectsFurtherReplies(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	authorID := createVoteUser(t, store, "comment-mod-author")
	postID := createReleasedVotePost(t, store, authorID, 0)
	parentID := insertComment(t, store, postID, authorID, nil, "parent")
	childID := insertComment(t, store, postID, authorID, &parentID, "child")
	actorID := createVoteUser(t, store, "comment-mod-actor")
	grantRole(t, store, actorID, role.Admin)

	first, err := store.HideComment(ctx, actorID, postID, parentID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.HideComment(ctx, actorID, postID, parentID)
	if err != nil {
		t.Fatal(err)
	}
	if first.CommentID != parentID || !first.Deleted || first.ModeratedByUserID != actorID || first.ModeratedAt.IsZero() {
		t.Fatalf("first=%#v", first)
	}
	if second.ModeratedByUserID != first.ModeratedByUserID || !second.ModeratedAt.Equal(first.ModeratedAt) {
		t.Fatalf("idempotent result first=%#v second=%#v", first, second)
	}

	page, err := comment.New(store).List(ctx, comment.Query{PostID: postID, ViewerUserID: actorID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !page.CanModerate || len(page.Comments) != 2 {
		t.Fatalf("page=%#v", page)
	}
	if page.Comments[0].ID != parentID || !page.Comments[0].Deleted || page.Comments[0].Body != nil {
		t.Fatalf("parent tombstone=%#v", page.Comments[0])
	}
	if page.Comments[1].ID != childID || page.Comments[1].ParentCommentID == nil || *page.Comments[1].ParentCommentID != parentID || page.Comments[1].Deleted {
		t.Fatalf("child=%#v", page.Comments[1])
	}
	if _, err := comment.New(store).Create(ctx, comment.CreateRequest{
		PostID: postID, UserID: authorID, ParentCommentID: &parentID, Body: "late reply",
	}); !errors.Is(err, comment.ErrParentCommentDeleted) {
		t.Fatalf("reply to moderated parent error=%v", err)
	}
}

func TestHideModerationMissingTargetsAndSQLPlansStayBounded(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	actorID := createVoteUser(t, store, "moderation-plan-actor")
	grantRole(t, store, actorID, role.Moderator)
	if _, err := store.HidePost(ctx, actorID, 999999999); !errors.Is(err, moderation.ErrPostNotFound) {
		t.Fatalf("missing post error=%v", err)
	}
	if _, err := store.HideComment(ctx, actorID, 1, 999999999); !errors.Is(err, moderation.ErrCommentNotFound) {
		t.Fatalf("missing comment error=%v", err)
	}

	authorID := createVoteUser(t, store, "moderation-plan-author")
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO posts (author_user_id, release_state)
		SELECT $1, 0
		FROM generate_series(1, 5000)
	`, authorID); err != nil {
		t.Fatal(err)
	}
	postID := createReleasedVotePost(t, store, authorID, 0)
	commentID := insertComment(t, store, postID, authorID, nil, "plan target")
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO comments (post_id, user_id, body)
		SELECT $1, $2, 'noise-' || value
		FROM generate_series(1, 5000) AS value
	`, postID, authorID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, "ANALYZE posts, comments, user_roles"); err != nil {
		t.Fatal(err)
	}

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rows, err := tx.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+hidePostSQL, actorID, postID, role.Moderator, role.Admin)
	if err != nil {
		t.Fatal(err)
	}
	postPlan := collectPlan(t, rows)
	t.Logf("post moderation plan:\n%s", postPlan)
	assertPlanContains(t, postPlan, "posts_pkey")
	assertPlanExcludes(t, postPlan, "Seq Scan on posts", "external merge", "Disk:")

	rows, err = tx.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+hideCommentSQL, actorID, postID, commentID, role.Moderator, role.Admin)
	if err != nil {
		t.Fatal(err)
	}
	commentPlan := collectPlan(t, rows)
	t.Logf("comment moderation plan:\n%s", commentPlan)
	assertPlanContains(t, commentPlan, "comments_pkey")
	assertPlanExcludes(t, commentPlan, "Seq Scan on comments", "external merge", "Disk:")
}
