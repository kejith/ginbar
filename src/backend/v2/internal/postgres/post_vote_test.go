package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/postvote"
)

func TestSetPostVoteAllTransitionsAndIdempotence(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := createVoteUser(t, store, "vote-transitions")
	postID := createReleasedVotePost(t, store, userID, 10)

	tests := []struct {
		name      string
		vote      model.PostVote
		wantScore int32
	}{
		{name: "0 to +1", vote: model.VoteUp, wantScore: 11},
		{name: "+1 idempotent", vote: model.VoteUp, wantScore: 11},
		{name: "+1 to 0", vote: model.VoteNeutral, wantScore: 10},
		{name: "0 to -1", vote: model.VoteDown, wantScore: 9},
		{name: "-1 to 0", vote: model.VoteNeutral, wantScore: 10},
		{name: "0 to +1 again", vote: model.VoteUp, wantScore: 11},
		{name: "+1 to -1", vote: model.VoteDown, wantScore: 9},
		{name: "-1 to +1", vote: model.VoteUp, wantScore: 11},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.SetPostVote(ctx, userID, postID, tt.vote)
			if err != nil {
				t.Fatal(err)
			}
			if got.PostID != postID || got.Score != tt.wantScore || got.Vote != tt.vote {
				t.Fatalf("result=%#v want score=%d vote=%d", got, tt.wantScore, tt.vote)
			}
			assertStoredVoteState(t, store, postID, userID, tt.wantScore, tt.vote)
		})
	}
}

func TestSetPostVoteRejectsMissingPost(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	userID := createVoteUser(t, store, "missing-post")
	_, err := store.SetPostVote(context.Background(), userID, 999999, model.VoteUp)
	if !errors.Is(err, postvote.ErrPostNotFound) {
		t.Fatalf("error=%v", err)
	}
}

func TestSetPostVoteTwoUsersMaintainExactScore(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	authorID := createVoteUser(t, store, "two-user-author")
	upUserID := createVoteUser(t, store, "two-user-up")
	downUserID := createVoteUser(t, store, "two-user-down")
	postID := createReleasedVotePost(t, store, authorID, 20)

	up, err := store.SetPostVote(ctx, upUserID, postID, model.VoteUp)
	if err != nil || up.Score != 21 {
		t.Fatalf("up result=%#v err=%v", up, err)
	}
	down, err := store.SetPostVote(ctx, downUserID, postID, model.VoteDown)
	if err != nil || down.Score != 20 {
		t.Fatalf("down result=%#v err=%v", down, err)
	}
	assertScoreMatchesVotes(t, store, postID, 20)
}

func TestConcurrentPostVotesKeepScoreEqualToStoredVotes(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	authorID := createVoteUser(t, store, "race-author")
	postID := createReleasedVotePost(t, store, authorID, 100)
	userIDs := make([]int64, 8)
	for i := range userIDs {
		userIDs[i] = createVoteUser(t, store, fmt.Sprintf("race-user-%d", i))
	}

	start := make(chan struct{})
	errs := make(chan error, 64)
	var wg sync.WaitGroup
	votes := []model.PostVote{model.VoteUp, model.VoteDown, model.VoteNeutral, model.VoteUp, model.VoteDown, model.VoteNeutral, model.VoteUp, model.VoteDown}
	for round := 0; round < 8; round++ {
		for index, userID := range userIDs {
			vote := votes[(round+index)%len(votes)]
			wg.Add(1)
			go func(userID int64, vote model.PostVote) {
				defer wg.Done()
				<-start
				_, err := store.SetPostVote(ctx, userID, postID, vote)
				errs <- err
			}(userID, vote)
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent vote failed: %v", err)
		}
	}
	assertScoreMatchesVotes(t, store, postID, 100)
}

func TestFeedAndAroundExposeViewerVoteWithoutChangingSignedOutRead(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	authorID := createVoteUser(t, store, "read-author")
	viewerID := createVoteUser(t, store, "read-viewer")
	postID := createReleasedVotePost(t, store, authorID, 7)
	if _, err := store.SetPostVote(ctx, viewerID, postID, model.VoteDown); err != nil {
		t.Fatal(err)
	}

	signedIn, err := store.ListFeed(ctx, feed.Query{Limit: 10, ViewerUserID: viewerID})
	if err != nil {
		t.Fatal(err)
	}
	if len(signedIn) != 1 || signedIn[0].ID != postID || signedIn[0].Score != 6 || signedIn[0].UserVote != model.VoteDown {
		t.Fatalf("signed-in feed=%#v", signedIn)
	}

	signedOut, err := store.ListFeed(ctx, feed.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(signedOut) != 1 || signedOut[0].Score != 6 || signedOut[0].UserVote != model.VoteNeutral {
		t.Fatalf("signed-out feed=%#v", signedOut)
	}

	around, err := store.AroundPost(ctx, feed.AroundQuery{PostID: postID, Radius: 5, ViewerUserID: viewerID})
	if err != nil {
		t.Fatal(err)
	}
	if len(around) != 1 || around[0].ID != postID || around[0].UserVote != model.VoteDown || around[0].Score != 6 {
		t.Fatalf("signed-in around=%#v", around)
	}

	aroundSignedOut, err := store.AroundPost(ctx, feed.AroundQuery{PostID: postID, Radius: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(aroundSignedOut) != 1 || aroundSignedOut[0].UserVote != model.VoteNeutral {
		t.Fatalf("signed-out around=%#v", aroundSignedOut)
	}
}

func createVoteUser(t *testing.T, store *Store, suffix string) int64 {
	t.Helper()
	var userID int64
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	username := fmt.Sprintf("%s-%x", suffix, time.Now().UnixNano())
	if err := store.pool.QueryRow(context.Background(), "INSERT INTO users (username) VALUES ($1) RETURNING id", username).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}

func createReleasedVotePost(t *testing.T, store *Store, authorID int64, score int32) int64 {
	t.Helper()
	ctx := context.Background()
	var postID int64
	if err := store.pool.QueryRow(ctx, `
		INSERT INTO posts (author_user_id, content_filter, release_state, score, released_at)
		VALUES ($1, 0, 1, $2, now())
		RETURNING id
	`, authorID, score).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	sha := make([]byte, 32)
	sha[0] = byte(postID)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO media (
			post_id, kind, processing_state, storage_key, mime_type,
			width, height, duration_ms, byte_size, sha256
		)
		VALUES ($1, 0, 1, $2, 'image/avif', 256, 256, 0, 1, $3)
	`, postID, fmt.Sprintf("media/vote-test-%d.avif", postID), sha); err != nil {
		t.Fatal(err)
	}
	return postID
}

func assertStoredVoteState(t *testing.T, store *Store, postID, userID int64, wantScore int32, wantVote model.PostVote) {
	t.Helper()
	ctx := context.Background()
	var score int32
	if err := store.pool.QueryRow(ctx, "SELECT score FROM posts WHERE id = $1", postID).Scan(&score); err != nil {
		t.Fatal(err)
	}
	if score != wantScore {
		t.Fatalf("stored score=%d want %d", score, wantScore)
	}
	var count int
	var value int16
	if wantVote == model.VoteNeutral {
		if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM post_votes WHERE post_id = $1 AND user_id = $2", postID, userID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("neutral vote persisted rows=%d", count)
		}
		return
	}
	if err := store.pool.QueryRow(ctx, "SELECT value FROM post_votes WHERE post_id = $1 AND user_id = $2", postID, userID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if model.PostVote(value) != wantVote {
		t.Fatalf("stored vote=%d want %d", value, wantVote)
	}
}

func assertScoreMatchesVotes(t *testing.T, store *Store, postID int64, baseScore int32) {
	t.Helper()
	ctx := context.Background()
	var score int32
	var voteSum int32
	if err := store.pool.QueryRow(ctx, "SELECT score FROM posts WHERE id = $1", postID).Scan(&score); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, "SELECT COALESCE(sum(value), 0)::integer FROM post_votes WHERE post_id = $1", postID).Scan(&voteSum); err != nil {
		t.Fatal(err)
	}
	if score != baseScore+voteSum {
		t.Fatalf("score=%d base=%d voteSum=%d", score, baseScore, voteSum)
	}
}
