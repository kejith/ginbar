package feed

import (
	"context"
	"errors"
	"fmt"

	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/search"
)

const (
	DefaultLimit = 60
	MaxLimit     = 120
)

var ErrPostNotFound = errors.New("post not found")

type Query struct {
	Before int64
	Limit  int
	// Filters is the allowed content-visibility set for this request, not a search/context filter.
	Filters []model.ContentFilter
	Search  search.Query
}

type AroundQuery struct {
	PostID int64
	Radius int
	// Filters is the allowed content-visibility set for this request. It applies to the selected post too.
	Filters []model.ContentFilter
	Search  search.Query
}

type Page struct {
	Posts      []model.PostSummary `json:"posts"`
	NextBefore int64               `json:"nextBefore,omitempty"`
}

type Around struct {
	Posts      []model.PostSummary `json:"posts"`
	SelectedID int64               `json:"selectedId"`
}

type Store interface {
	ListFeed(context.Context, Query) ([]model.PostSummary, error)
	AroundPost(context.Context, AroundQuery) ([]model.PostSummary, error)
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

func (s *Service) List(ctx context.Context, q Query) (Page, error) {
	q.Limit = normalizeLimit(q.Limit)
	q.Filters = normalizeFilters(q.Filters)
	rows, err := s.store.ListFeed(ctx, q)
	if err != nil {
		return Page{}, err
	}
	page := Page{Posts: rows}
	if len(page.Posts) > q.Limit {
		page.Posts = page.Posts[:q.Limit]
		page.NextBefore = page.Posts[len(page.Posts)-1].ID
	}
	return page, nil
}

func (s *Service) Around(ctx context.Context, q AroundQuery) (Around, error) {
	if q.PostID <= 0 {
		return Around{}, fmt.Errorf("post id must be positive")
	}
	if q.Radius <= 0 {
		q.Radius = 30
	}
	if q.Radius > MaxLimit/2 {
		q.Radius = MaxLimit / 2
	}
	q.Filters = normalizeFilters(q.Filters)
	posts, err := s.store.AroundPost(ctx, q)
	if err != nil {
		return Around{}, err
	}
	for _, post := range posts {
		if post.ID == q.PostID {
			return Around{Posts: posts, SelectedID: q.PostID}, nil
		}
	}
	return Around{}, ErrPostNotFound
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
