package moderation

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrForbidden       = errors.New("moderation forbidden")
	ErrPostNotFound    = errors.New("post not found")
	ErrCommentNotFound = errors.New("comment not found")
)

type PostResult struct {
	PostID            int64     `json:"postId"`
	Deleted           bool      `json:"deleted"`
	ModeratedAt       time.Time `json:"moderatedAt"`
	ModeratedByUserID int64     `json:"moderatedByUserId"`
}

type CommentResult struct {
	PostID            int64     `json:"postId"`
	CommentID         int64     `json:"commentId"`
	Deleted           bool      `json:"deleted"`
	ModeratedAt       time.Time `json:"moderatedAt"`
	ModeratedByUserID int64     `json:"moderatedByUserId"`
}

type Store interface {
	HidePost(context.Context, int64, int64) (PostResult, error)
	HideComment(context.Context, int64, int64, int64) (CommentResult, error)
}

type Service struct {
	store Store
}

func New(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) HidePost(ctx context.Context, actorUserID, postID int64) (PostResult, error) {
	if actorUserID <= 0 {
		return PostResult{}, fmt.Errorf("actor user id must be positive")
	}
	if postID <= 0 {
		return PostResult{}, fmt.Errorf("post id must be positive")
	}
	return s.store.HidePost(ctx, actorUserID, postID)
}

func (s *Service) HideComment(ctx context.Context, actorUserID, postID, commentID int64) (CommentResult, error) {
	if actorUserID <= 0 {
		return CommentResult{}, fmt.Errorf("actor user id must be positive")
	}
	if postID <= 0 {
		return CommentResult{}, fmt.Errorf("post id must be positive")
	}
	if commentID <= 0 {
		return CommentResult{}, fmt.Errorf("comment id must be positive")
	}
	return s.store.HideComment(ctx, actorUserID, postID, commentID)
}
