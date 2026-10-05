package postvote

import (
	"context"
	"errors"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

type testStore struct {
	userID int64
	postID int64
	vote   model.PostVote
	result Result
	err    error
	calls  int
}

func (s *testStore) SetPostVote(_ context.Context, userID, postID int64, vote model.PostVote) (Result, error) {
	s.calls++
	s.userID = userID
	s.postID = postID
	s.vote = vote
	return s.result, s.err
}

func TestServiceValidatesVoteAndDelegates(t *testing.T) {
	store := &testStore{result: Result{PostID: 9, Score: 12, Vote: model.VoteUp}}
	service := New(store)
	got, err := service.Set(context.Background(), 7, 9, model.VoteUp)
	if err != nil || got != store.result {
		t.Fatalf("result=%#v err=%v", got, err)
	}
	if store.calls != 1 || store.userID != 7 || store.postID != 9 || store.vote != model.VoteUp {
		t.Fatalf("store call=%#v", store)
	}

	_, err = service.Set(context.Background(), 7, 9, model.PostVote(2))
	if !errors.Is(err, ErrInvalidVote) || store.calls != 1 {
		t.Fatalf("invalid vote err=%v calls=%d", err, store.calls)
	}
}
