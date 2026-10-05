package commentvote

import (
	"context"
	"errors"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

type fakeStore struct {
	userID    int64
	postID    int64
	commentID int64
	vote      model.PostVote
	result    Result
	err       error
}

func (s *fakeStore) SetCommentVote(_ context.Context, userID, postID, commentID int64, vote model.PostVote) (Result, error) {
	s.userID = userID
	s.postID = postID
	s.commentID = commentID
	s.vote = vote
	return s.result, s.err
}

func TestSetValidatesAndDelegates(t *testing.T) {
	store := &fakeStore{result: Result{CommentID: 9, Score: 3, Vote: model.VoteUp}}
	service := New(store)
	got, err := service.Set(context.Background(), 7, 8, 9, model.VoteUp)
	if err != nil {
		t.Fatal(err)
	}
	if got != store.result || store.userID != 7 || store.postID != 8 || store.commentID != 9 || store.vote != model.VoteUp {
		t.Fatalf("result=%#v store=%#v", got, store)
	}
}

func TestSetRejectsInvalidInput(t *testing.T) {
	service := New(&fakeStore{})
	for _, tt := range []struct {
		userID    int64
		postID    int64
		commentID int64
		vote      model.PostVote
		invalid   bool
	}{
		{0, 1, 1, model.VoteUp, false},
		{1, 0, 1, model.VoteUp, false},
		{1, 1, 0, model.VoteUp, false},
		{1, 1, 1, model.PostVote(2), true},
	} {
		_, err := service.Set(context.Background(), tt.userID, tt.postID, tt.commentID, tt.vote)
		if err == nil {
			t.Fatalf("input=%#v unexpectedly succeeded", tt)
		}
		if tt.invalid && !errors.Is(err, ErrInvalidVote) {
			t.Fatalf("input=%#v error=%v", tt, err)
		}
	}
}
