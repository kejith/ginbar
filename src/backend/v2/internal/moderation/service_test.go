package moderation

import (
	"context"
	"testing"
	"time"
)

type serviceTestStore struct {
	postResult     PostResult
	commentResult  CommentResult
	postActorID    int64
	postID         int64
	commentActorID int64
	commentPostID  int64
	commentID      int64
}

func (s *serviceTestStore) HidePost(_ context.Context, actorUserID, postID int64) (PostResult, error) {
	s.postActorID = actorUserID
	s.postID = postID
	return s.postResult, nil
}

func (s *serviceTestStore) HideComment(_ context.Context, actorUserID, postID, commentID int64) (CommentResult, error) {
	s.commentActorID = actorUserID
	s.commentPostID = postID
	s.commentID = commentID
	return s.commentResult, nil
}

func TestServiceValidatesIDsAndReturnsAuthoritativeValues(t *testing.T) {
	store := &serviceTestStore{
		postResult: PostResult{
			PostID:            9,
			Deleted:           true,
			ModeratedAt:       time.Unix(100, 0).UTC(),
			ModeratedByUserID: 7,
		},
		commentResult: CommentResult{
			PostID:            9,
			CommentID:         11,
			Deleted:           true,
			ModeratedAt:       time.Unix(101, 0).UTC(),
			ModeratedByUserID: 7,
		},
	}
	service := New(store)

	if _, err := service.HidePost(context.Background(), 0, 9); err == nil {
		t.Fatal("expected invalid actor error")
	}
	if _, err := service.HidePost(context.Background(), 7, 0); err == nil {
		t.Fatal("expected invalid post error")
	}
	gotPost, err := service.HidePost(context.Background(), 7, 9)
	if err != nil {
		t.Fatal(err)
	}
	if gotPost != store.postResult || store.postActorID != 7 || store.postID != 9 {
		t.Fatalf("result=%#v actor=%d post=%d", gotPost, store.postActorID, store.postID)
	}

	if _, err := service.HideComment(context.Background(), 7, 9, 0); err == nil {
		t.Fatal("expected invalid comment error")
	}
	gotComment, err := service.HideComment(context.Background(), 7, 9, 11)
	if err != nil {
		t.Fatal(err)
	}
	if gotComment != store.commentResult || store.commentActorID != 7 || store.commentPostID != 9 || store.commentID != 11 {
		t.Fatalf("result=%#v actor=%d post=%d comment=%d", gotComment, store.commentActorID, store.commentPostID, store.commentID)
	}
}
