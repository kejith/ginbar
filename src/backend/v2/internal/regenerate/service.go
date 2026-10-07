package regenerate

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrForbidden      = errors.New("media regeneration forbidden")
	ErrNotRegenerable = errors.New("post has no released ready media to regenerate")
)

type Outcome string

const (
	OutcomeQueued     Outcome = "queued"
	OutcomeCoalesced  Outcome = "coalesced"
	OutcomeSuperseded Outcome = "superseded"
)

type Requested struct {
	JobID   int64
	Outcome Outcome
}

type Repository interface {
	RequestRegeneration(context.Context, int64, int64) (Requested, error)
}

type Service struct {
	repo Repository
}

func New(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Request(ctx context.Context, actorUserID, postID int64) (Requested, error) {
	if actorUserID <= 0 {
		return Requested{}, fmt.Errorf("actor user id must be positive")
	}
	if postID <= 0 {
		return Requested{}, fmt.Errorf("post id must be positive")
	}
	return s.repo.RequestRegeneration(ctx, actorUserID, postID)
}
