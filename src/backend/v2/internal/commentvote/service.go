package commentvote

import (
	"context"
	"errors"
	"fmt"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

var (
	ErrInvalidVote          = errors.New("invalid comment vote")
	ErrCommentNotFound      = errors.New("comment not found")
	ErrCommitOutcomeUnknown = errors.New("transaction commit outcome unknown")
)

type Result struct {
	CommentID int64          `json:"commentId"`
	Score     int32          `json:"score"`
	Vote      model.PostVote `json:"vote"`
}

type Store interface {
	SetCommentVote(context.Context, int64, int64, int64, model.PostVote) (Result, error)
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

func (s *Service) Set(ctx context.Context, userID, postID, commentID int64, vote model.PostVote) (Result, error) {
	if userID <= 0 {
		return Result{}, fmt.Errorf("user id must be positive")
	}
	if postID <= 0 {
		return Result{}, fmt.Errorf("post id must be positive")
	}
	if commentID <= 0 {
		return Result{}, fmt.Errorf("comment id must be positive")
	}
	if !vote.Valid() {
		return Result{}, ErrInvalidVote
	}
	return s.store.SetCommentVote(ctx, userID, postID, commentID, vote)
}
