package duplicate

import (
	"context"
	"fmt"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
)

type Query struct {
	PostID  int64
	Limit   int
	Filters []model.ContentFilter
}

type Candidate struct {
	PostID int64 `json:"postId"`
}

type Store interface {
	ListDuplicateCandidates(context.Context, Query) ([]Candidate, error)
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

func (s *Service) List(ctx context.Context, q Query) ([]Candidate, error) {
	if q.PostID <= 0 {
		return nil, fmt.Errorf("post id must be positive")
	}
	q.Limit = normalizeLimit(q.Limit)
	q.Filters = normalizeFilters(q.Filters)
	return s.store.ListDuplicateCandidates(ctx, q)
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

func normalizeFilters(filters []model.ContentFilter) []model.ContentFilter {
	if len(filters) == 0 {
		return []model.ContentFilter{model.FilterSFW}
	}
	out := make([]model.ContentFilter, 0, len(filters))
	for _, filter := range filters {
		if !filter.Valid() {
			continue
		}
		seen := false
		for _, existing := range out {
			if existing == filter {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, filter)
		}
	}
	if len(out) == 0 {
		return []model.ContentFilter{model.FilterSFW}
	}
	return out
}
