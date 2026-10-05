package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/search"
	"github.com/kejith/ginbar/backend/v2/internal/tag"
)

func TestAddPostTagIsIdempotentAndReactivatesRemovedRelation(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := createVoteUser(t, store, "tag-add")
	postID := createReleasedVotePost(t, store, userID, 0)
	name := tag.Name{Display: "Cat Photo", Normalized: "cat photo"}

	first, err := store.AddPostTag(ctx, userID, postID, name)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AddPostTag(ctx, userID, postID, name)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tags) != 1 || len(second.Tags) != 1 || first.Tags[0].ID != second.Tags[0].ID {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	assertTagRowCounts(t, store, postID, name.Normalized, 1, 1)

	moderatorID := createVoteUser(t, store, "tag-mod")
	grantTagRole(t, store, moderatorID, tag.RoleModerator)
	removed, err := store.RemovePostTag(ctx, moderatorID, postID, first.Tags[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Tags) != 0 || !removed.CanRemove {
		t.Fatalf("removed=%#v", removed)
	}
	assertTagRowCounts(t, store, postID, name.Normalized, 1, 0)

	readded, err := store.AddPostTag(ctx, userID, postID, tag.Name{Display: "CAT PHOTO", Normalized: "cat photo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(readded.Tags) != 1 || readded.Tags[0].ID != first.Tags[0].ID || readded.Tags[0].Name != "Cat Photo" {
		t.Fatalf("readded=%#v", readded)
	}
	assertTagRowCounts(t, store, postID, name.Normalized, 1, 1)
}

func TestTagRemovalRequiresDatabaseModeratorOrAdminRole(t *testing.T) {
	for _, role := range []int16{tag.RoleModerator, tag.RoleAdmin} {
		t.Run(fmt.Sprintf("role-%d", role), func(t *testing.T) {
			store, cleanup := testAuthStore(t)
			defer cleanup()
			ctx := context.Background()
			authorID := createVoteUser(t, store, "role-auth")
			postID := createReleasedVotePost(t, store, authorID, 0)
			added, err := store.AddPostTag(ctx, authorID, postID, tag.Name{Display: "tag", Normalized: "tag"})
			if err != nil {
				t.Fatal(err)
			}

			ordinaryID := createVoteUser(t, store, "ordinary")
			if _, err := store.RemovePostTag(ctx, ordinaryID, postID, added.Tags[0].ID); !errors.Is(err, tag.ErrForbidden) {
				t.Fatalf("ordinary removal error=%v", err)
			}
			assertTagRowCounts(t, store, postID, "tag", 1, 1)

			elevatedID := createVoteUser(t, store, "elevated")
			grantTagRole(t, store, elevatedID, role)
			removed, err := store.RemovePostTag(ctx, elevatedID, postID, added.Tags[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(removed.Tags) != 0 || !removed.CanRemove {
				t.Fatalf("removed=%#v", removed)
			}
		})
	}
}

func TestRemoveAbsentPostTagIsIdempotent(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	userID := createVoteUser(t, store, "absent-mod")
	grantTagRole(t, store, userID, tag.RoleModerator)
	postID := createReleasedVotePost(t, store, userID, 0)

	first, err := store.RemovePostTag(context.Background(), userID, postID, 999999)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.RemovePostTag(context.Background(), userID, postID, 999999)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Tags) != 0 || len(second.Tags) != 0 || !first.CanRemove || !second.CanRemove {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
}

func TestTagMutationRejectsUnavailablePostBeforeCreatingRelation(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := createVoteUser(t, store, "tag-miss")
	name := tag.Name{Display: "orphan-check", Normalized: "orphan-check"}

	if _, err := store.AddPostTag(ctx, userID, 999999, name); !errors.Is(err, tag.ErrPostNotFound) {
		t.Fatalf("missing add error=%v", err)
	}
	var tagCount int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM tags WHERE normalized_name = $1", name.Normalized).Scan(&tagCount); err != nil {
		t.Fatal(err)
	}
	if tagCount != 0 {
		t.Fatalf("missing post created tags=%d", tagCount)
	}

	postID := createReleasedVotePost(t, store, userID, 0)
	if _, err := store.pool.Exec(ctx, "UPDATE posts SET deleted_at = now() WHERE id = $1", postID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddPostTag(ctx, userID, postID, name); !errors.Is(err, tag.ErrPostNotFound) {
		t.Fatalf("deleted add error=%v", err)
	}
}

func TestConcurrentAddPostTagCreatesSingleTagAndRelation(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	authorID := createVoteUser(t, store, "race-tag")
	postID := createReleasedVotePost(t, store, authorID, 0)
	users := make([]int64, 12)
	for i := range users {
		users[i] = createVoteUser(t, store, fmt.Sprintf("tagu-%d", i))
	}

	start := make(chan struct{})
	errs := make(chan error, len(users))
	var wg sync.WaitGroup
	for _, userID := range users {
		wg.Add(1)
		go func(userID int64) {
			defer wg.Done()
			<-start
			_, err := store.AddPostTag(ctx, userID, postID, tag.Name{Display: "Race Tag", Normalized: "race tag"})
			errs <- err
		}(userID)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent add failed: %v", err)
		}
	}
	assertTagRowCounts(t, store, postID, "race tag", 1, 1)
}

func TestConcurrentTagAddRemovePreservesSingleConsistentRelation(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	memberID := createVoteUser(t, store, "race-add")
	moderatorID := createVoteUser(t, store, "race-rem")
	grantTagRole(t, store, moderatorID, tag.RoleModerator)
	postID := createReleasedVotePost(t, store, memberID, 0)
	initial, err := store.AddPostTag(ctx, memberID, postID, tag.Name{Display: "toggle", Normalized: "toggle"})
	if err != nil {
		t.Fatal(err)
	}
	tagID := initial.Tags[0].ID

	start := make(chan struct{})
	errs := make(chan error, 40)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.AddPostTag(ctx, memberID, postID, tag.Name{Display: "toggle", Normalized: "toggle"})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := store.RemovePostTag(ctx, moderatorID, postID, tagID)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent mutation failed: %v", err)
		}
	}

	var relationCount int
	var consistent bool
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*), bool_and((removed_at IS NULL) = (removed_by_user_id IS NULL))
		FROM post_tags
		WHERE post_id = $1 AND tag_id = $2
	`, postID, tagID).Scan(&relationCount, &consistent); err != nil {
		t.Fatal(err)
	}
	if relationCount != 1 || !consistent {
		t.Fatalf("relation count=%d consistent=%v", relationCount, consistent)
	}
}

func TestTagMutationImmediatelyAffectsExistingSearchSemantics(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	memberID := createVoteUser(t, store, "tag-search")
	moderatorID := createVoteUser(t, store, "tag-smod")
	grantTagRole(t, store, moderatorID, tag.RoleModerator)
	postID := createReleasedVotePost(t, store, memberID, 0)

	added, err := store.AddPostTag(ctx, memberID, postID, tag.Name{Display: "Searchable", Normalized: "searchable"})
	if err != nil {
		t.Fatal(err)
	}
	posts, err := store.ListFeed(ctx, feed.Query{Limit: 10, Search: search.Query{IncludeTags: []string{"searchable"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 1 || posts[0].ID != postID {
		t.Fatalf("search after add=%#v", posts)
	}

	if _, err := store.RemovePostTag(ctx, moderatorID, postID, added.Tags[0].ID); err != nil {
		t.Fatal(err)
	}
	posts, err = store.ListFeed(ctx, feed.Query{Limit: 10, Search: search.Query{IncludeTags: []string{"searchable"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 0 {
		t.Fatalf("search after remove=%#v", posts)
	}
}

func TestLoadPostTagsReportsRemovalCapabilityFromDatabaseRole(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	memberID := createVoteUser(t, store, "tag-view")
	moderatorID := createVoteUser(t, store, "tag-vmod")
	grantTagRole(t, store, moderatorID, tag.RoleModerator)
	postID := createReleasedVotePost(t, store, memberID, 0)
	if _, err := store.AddPostTag(ctx, memberID, postID, tag.Name{Display: "view", Normalized: "view"}); err != nil {
		t.Fatal(err)
	}

	signedOut, err := store.LoadPostTags(ctx, postID, 0)
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := store.LoadPostTags(ctx, postID, memberID)
	if err != nil {
		t.Fatal(err)
	}
	moderator, err := store.LoadPostTags(ctx, postID, moderatorID)
	if err != nil {
		t.Fatal(err)
	}
	if signedOut.CanRemove || ordinary.CanRemove || !moderator.CanRemove {
		t.Fatalf("signedOut=%v ordinary=%v moderator=%v", signedOut.CanRemove, ordinary.CanRemove, moderator.CanRemove)
	}
}

func grantTagRole(t *testing.T, store *Store, userID int64, role int16) {
	t.Helper()
	if _, err := store.pool.Exec(context.Background(), `
		INSERT INTO user_roles (user_id, role)
		VALUES ($1, $2)
	`, userID, role); err != nil {
		t.Fatal(err)
	}
}

func assertTagRowCounts(t *testing.T, store *Store, postID int64, normalizedName string, wantTags, wantActiveRelations int) {
	t.Helper()
	ctx := context.Background()
	var tagCount int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM tags WHERE normalized_name = $1", normalizedName).Scan(&tagCount); err != nil {
		t.Fatal(err)
	}
	var activeCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM post_tags pt
		JOIN tags t ON t.id = pt.tag_id
		WHERE pt.post_id = $1
		  AND t.normalized_name = $2
		  AND pt.removed_at IS NULL
	`, postID, normalizedName).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if tagCount != wantTags || activeCount != wantActiveRelations {
		t.Fatalf("tag rows=%d active relations=%d want %d/%d", tagCount, activeCount, wantTags, wantActiveRelations)
	}
}
