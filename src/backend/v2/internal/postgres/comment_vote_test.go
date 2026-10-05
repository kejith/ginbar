package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/comment"
	"github.com/kejith/ginbar/backend/v2/internal/commentvote"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

func TestSetCommentVoteAllTransitionsAndIdempotence(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := createVoteUser(t, store, "cvt-trans")
	postID := createReleasedVotePost(t, store, userID, 0)
	commentID := insertComment(t, store, postID, userID, nil, "vote target")
	if _, err := store.pool.Exec(ctx, "UPDATE comments SET score = 10 WHERE id = $1", commentID); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name      string
		vote      model.PostVote
		wantScore int32
	}{
		{"0 to +1", model.VoteUp, 11},
		{"+1 idempotent", model.VoteUp, 11},
		{"+1 to 0", model.VoteNeutral, 10},
		{"0 idempotent", model.VoteNeutral, 10},
		{"0 to -1", model.VoteDown, 9},
		{"-1 idempotent", model.VoteDown, 9},
		{"-1 to 0", model.VoteNeutral, 10},
		{"0 to +1 again", model.VoteUp, 11},
		{"+1 to -1", model.VoteDown, 9},
		{"-1 to +1", model.VoteUp, 11},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.SetCommentVote(ctx, userID, postID, commentID, tt.vote)
			if err != nil {
				t.Fatal(err)
			}
			if got.CommentID != commentID || got.Score != tt.wantScore || got.Vote != tt.vote {
				t.Fatalf("result=%#v want score=%d vote=%d", got, tt.wantScore, tt.vote)
			}
			assertStoredCommentVoteState(t, store, commentID, userID, tt.wantScore, tt.vote)
		})
	}
}

func TestSetCommentVoteRejectsUnavailableTargets(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := createVoteUser(t, store, "cvt-error")
	postID := createReleasedVotePost(t, store, userID, 0)
	otherPostID := createReleasedVotePost(t, store, userID, 0)
	commentID := insertComment(t, store, postID, userID, nil, "target")
	otherCommentID := insertComment(t, store, otherPostID, userID, nil, "other")

	for _, tt := range []struct {
		postID    int64
		commentID int64
	}{
		{postID, commentID + 999999},
		{postID, otherCommentID},
	} {
		if _, err := store.SetCommentVote(ctx, userID, tt.postID, tt.commentID, model.VoteUp); !errors.Is(err, commentvote.ErrCommentNotFound) {
			t.Fatalf("target=%#v error=%v", tt, err)
		}
	}

	if _, err := store.pool.Exec(ctx, "UPDATE comments SET deleted_at = now() WHERE id = $1", commentID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCommentVote(ctx, userID, postID, commentID, model.VoteUp); !errors.Is(err, commentvote.ErrCommentNotFound) {
		t.Fatalf("deleted comment error=%v", err)
	}

	if _, err := store.pool.Exec(ctx, "UPDATE posts SET deleted_at = now() WHERE id = $1", otherPostID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCommentVote(ctx, userID, otherPostID, otherCommentID, model.VoteUp); !errors.Is(err, commentvote.ErrCommentNotFound) {
		t.Fatalf("deleted post error=%v", err)
	}
}

func TestSetCommentVoteTwoUsersMaintainExactScore(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	authorID := createVoteUser(t, store, "cvt-author")
	upUserID := createVoteUser(t, store, "cvt-up")
	downUserID := createVoteUser(t, store, "cvt-down")
	postID := createReleasedVotePost(t, store, authorID, 0)
	commentID := insertComment(t, store, postID, authorID, nil, "target")
	if _, err := store.pool.Exec(ctx, "UPDATE comments SET score = 20 WHERE id = $1", commentID); err != nil {
		t.Fatal(err)
	}

	up, err := store.SetCommentVote(ctx, upUserID, postID, commentID, model.VoteUp)
	if err != nil || up.Score != 21 {
		t.Fatalf("up result=%#v err=%v", up, err)
	}
	down, err := store.SetCommentVote(ctx, downUserID, postID, commentID, model.VoteDown)
	if err != nil || down.Score != 20 {
		t.Fatalf("down result=%#v err=%v", down, err)
	}
	assertCommentScoreMatchesVotes(t, store, commentID, 20)
}

func TestConcurrentCommentVotesKeepScoreEqualToStoredVotes(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	authorID := createVoteUser(t, store, "cvt-race")
	postID := createReleasedVotePost(t, store, authorID, 0)
	commentID := insertComment(t, store, postID, authorID, nil, "race target")
	if _, err := store.pool.Exec(ctx, "UPDATE comments SET score = 100 WHERE id = $1", commentID); err != nil {
		t.Fatal(err)
	}
	userIDs := make([]int64, 8)
	for i := range userIDs {
		userIDs[i] = createVoteUser(t, store, fmt.Sprintf("cv-race-%d", i))
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
				_, err := store.SetCommentVote(ctx, userID, postID, commentID, vote)
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
	assertCommentScoreMatchesVotes(t, store, commentID, 100)
}

func TestCommentReadsExposeViewerVoteAcrossPaginationAndNeutralTombstones(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	authorID := createVoteUser(t, store, "cvr-author")
	viewerID := createVoteUser(t, store, "cvr-viewer")
	postID := createReleasedVotePost(t, store, authorID, 0)
	firstID := insertComment(t, store, postID, authorID, nil, "first")
	secondID := insertComment(t, store, postID, authorID, nil, "second")
	thirdID := insertComment(t, store, postID, authorID, nil, "third")

	if _, err := store.SetCommentVote(ctx, viewerID, postID, firstID, model.VoteUp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetCommentVote(ctx, viewerID, postID, thirdID, model.VoteDown); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, "UPDATE comments SET deleted_at = now() WHERE id = $1", firstID); err != nil {
		t.Fatal(err)
	}

	service := comment.New(store)
	first, err := service.List(ctx, comment.Query{PostID: postID, Limit: 2, ViewerUserID: viewerID})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Comments) != 2 || first.Comments[0].ID != firstID || first.Comments[1].ID != secondID || first.NextAfter != secondID {
		t.Fatalf("first page=%#v", first)
	}
	if first.Comments[0].UserVote != model.VoteNeutral || !first.Comments[0].Deleted || first.Comments[1].UserVote != model.VoteNeutral {
		t.Fatalf("first page votes=%#v", first.Comments)
	}

	second, err := service.List(ctx, comment.Query{PostID: postID, After: first.NextAfter, Limit: 2, ViewerUserID: viewerID})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Comments) != 1 || second.Comments[0].ID != thirdID || second.Comments[0].UserVote != model.VoteDown {
		t.Fatalf("second page=%#v", second)
	}

	signedOut, err := service.List(ctx, comment.Query{PostID: postID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range signedOut.Comments {
		if entry.UserVote != model.VoteNeutral {
			t.Fatalf("signed-out comment has viewer vote: %#v", entry)
		}
	}
}

func assertStoredCommentVoteState(t *testing.T, store *Store, commentID, userID int64, wantScore int32, wantVote model.PostVote) {
	t.Helper()
	ctx := context.Background()
	var score int32
	if err := store.pool.QueryRow(ctx, "SELECT score FROM comments WHERE id = $1", commentID).Scan(&score); err != nil {
		t.Fatal(err)
	}
	if score != wantScore {
		t.Fatalf("stored score=%d want %d", score, wantScore)
	}
	if wantVote == model.VoteNeutral {
		var count int
		if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM comment_votes WHERE comment_id = $1 AND user_id = $2", commentID, userID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("neutral vote persisted rows=%d", count)
		}
		return
	}
	var value int16
	if err := store.pool.QueryRow(ctx, "SELECT value FROM comment_votes WHERE comment_id = $1 AND user_id = $2", commentID, userID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if model.PostVote(value) != wantVote {
		t.Fatalf("stored vote=%d want %d", value, wantVote)
	}
}

func assertCommentScoreMatchesVotes(t *testing.T, store *Store, commentID int64, baseScore int32) {
	t.Helper()
	ctx := context.Background()
	var score int32
	var voteSum int32
	if err := store.pool.QueryRow(ctx, "SELECT score FROM comments WHERE id = $1", commentID).Scan(&score); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, "SELECT COALESCE(sum(value), 0)::integer FROM comment_votes WHERE comment_id = $1", commentID).Scan(&voteSum); err != nil {
		t.Fatal(err)
	}
	if score != baseScore+voteSum {
		t.Fatalf("score=%d base=%d voteSum=%d", score, baseScore, voteSum)
	}
}
