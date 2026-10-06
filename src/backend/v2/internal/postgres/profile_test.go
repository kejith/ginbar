package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/profile"
)

func TestProfileReadIsPublicMetadataOnlyAndPostPageIsBounded(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	createdAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Microsecond)
	var userID, otherUserID int64
	if err := store.pool.QueryRow(ctx, `
		INSERT INTO users (username, created_at, updated_at)
		VALUES ('ProfileOwner', $1, $1)
		RETURNING id
	`, createdAt).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ('OtherProfileUser') RETURNING id").Scan(&otherUserID); err != nil {
		t.Fatal(err)
	}

	insertProfilePost := func(authorID int64, filter, releaseState int16, deleted bool, mediaState int16) int64 {
		t.Helper()
		var postID int64
		var deletedAt any
		if deleted {
			deletedAt = time.Now()
		}
		if err := store.pool.QueryRow(ctx, `
			INSERT INTO posts (author_user_id, content_filter, release_state, released_at, deleted_at)
			VALUES ($1, $2, $3, CASE WHEN $3 = 1 THEN now() ELSE NULL END, $4)
			RETURNING id
		`, authorID, filter, releaseState, deletedAt).Scan(&postID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO media (
				post_id, kind, processing_state, storage_key, mime_type,
				width, height, byte_size, sha256
			)
			VALUES ($1, 0, $2, $3, 'image/avif', 640, 480, 100, decode(repeat('ab', 32), 'hex'))
		`, postID, mediaState, fmt.Sprintf("media/profile/%d", postID)); err != nil {
			t.Fatal(err)
		}
		return postID
	}

	oldest := insertProfilePost(userID, 0, 1, false, 1)
	middle := insertProfilePost(userID, 0, 1, false, 1)
	newest := insertProfilePost(userID, 0, 1, false, 1)
	_ = insertProfilePost(userID, 2, 1, false, 1)
	_ = insertProfilePost(userID, 0, 0, false, 1)
	_ = insertProfilePost(userID, 0, 1, true, 1)
	_ = insertProfilePost(userID, 0, 1, false, 0)
	_ = insertProfilePost(otherUserID, 0, 1, false, 1)

	service := profile.New(store)
	page, err := service.Get(ctx, profile.Query{UserID: userID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if page.User.ID != userID || page.User.Username != "ProfileOwner" || !page.User.CreatedAt.Equal(createdAt) {
		t.Fatalf("user=%#v", page.User)
	}
	if len(page.Posts) != 2 || page.Posts[0].ID != newest || page.Posts[1].ID != middle || page.NextBefore != middle {
		t.Fatalf("page=%#v oldest=%d", page, oldest)
	}
	for _, post := range page.Posts {
		if post.AuthorID != userID || post.Filter != 0 || post.UserVote != 0 {
			t.Fatalf("unexpected post=%#v", post)
		}
	}

	older, err := service.Get(ctx, profile.Query{UserID: userID, Before: page.NextBefore, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Posts) != 1 || older.Posts[0].ID != oldest || older.NextBefore != 0 {
		t.Fatalf("older=%#v", older)
	}
}

func TestProfileReadHidesMissingAndInactiveUsers(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	var inactiveID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username, status) VALUES ('InactiveProfile', 1) RETURNING id").Scan(&inactiveID); err != nil {
		t.Fatal(err)
	}
	for _, userID := range []int64{inactiveID, inactiveID + 1000} {
		if _, err := profile.New(store).Get(ctx, profile.Query{UserID: userID}); !errors.Is(err, profile.ErrUserNotFound) {
			t.Fatalf("user=%d error=%v", userID, err)
		}
	}
}
