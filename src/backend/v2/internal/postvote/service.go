package postvote

import (
	"context"
	"errors"
	"fmt"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

var (
	ErrInvalidVote          = errors.New("invalid post vote")
	ErrPostNotFound         = errors.New("post not found")
	ErrCommitOutcomeUnknown = errors.New("transaction commit outcome unknown")
)

type Result struct {
	PostID int64          `json:"postId"`
	Score  int32          `json:"score"`
	Vote   model.PostVote `json:"vote"`
}

type Store interface {
	SetPostVote(context.Context, int64, int64, model.PostVote) (Result, error)
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

func (s *Service) Set(ctx context.Context, userID, postID int64, vote model.PostVote) (Result, error) {
	if userID <= 0 {
		return Result{}, fmt.Errorf("user id must be positive")
	}
	if postID <= 0 {
		return Result{}, fmt.Errorf("post id must be positive")
	}
	if !vote.Valid() {
		return Result{}, ErrInvalidVote
	}
	return s.store.SetPostVote(ctx, userID, postID, vote)
}
