package regenerate

import (
	"context"
	"errors"
)

var ErrNotRegenerable = errors.New("post has no released ready media to regenerate")

type Outcome uint8

const (
	OutcomeQueued Outcome = iota
	OutcomeCoalesced
	OutcomeSuperseded
)

type Requested struct {
	JobID   int64
	Outcome Outcome
}

type Repository interface {
	RequestRegeneration(context.Context, int64) (Requested, error)
}

type Service struct {
	repo Repository
}

func New(repo Repository) (*Service, error) {
	if repo == nil {
		return nil, errors.New("regeneration repository is required")
	}
	return &Service{repo: repo}, nil
}

func (s *Service) Request(ctx context.Context, postID int64) (Requested, error) {
	if postID <= 0 {
		return Requested{}, errors.New("post id must be positive")
	}
	return s.repo.RequestRegeneration(ctx, postID)
}
