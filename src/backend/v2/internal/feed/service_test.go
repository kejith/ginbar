package feed

import (
	"context"
	"errors"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

type fakeStore struct {
	feed   []model.PostSummary
	around []model.PostSummary
	err    error
	query  Query
	aquery AroundQuery
}

func (s *fakeStore) ListFeed(_ context.Context, q Query) ([]model.PostSummary, error) {
	s.query = q
	return s.feed, s.err
}

func (s *fakeStore) AroundPost(_ context.Context, q AroundQuery) ([]model.PostSummary, error) {
	s.aquery = q
	return s.around, s.err
}

func TestListUsesLimitPlusOneContract(t *testing.T) {
	store := &fakeStore{feed: []model.PostSummary{{ID: 9}, {ID: 8}, {ID: 7}}}
	service := New(store)
	page, err := service.List(context.Background(), Query{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if store.query.Limit != 2 || len(page.Posts) != 2 || page.NextBefore != 8 {
		t.Fatalf("unexpected page/store query: %#v %#v", page, store.query)
	}
	if len(store.query.Filters) != 1 || store.query.Filters[0] != model.FilterSFW {
		t.Fatalf("default filters = %#v", store.query.Filters)
	}
}

func TestListClampsLimitAndDeduplicatesFilters(t *testing.T) {
	store := &fakeStore{}
	_, err := New(store).List(context.Background(), Query{
		Limit:   999,
		Filters: []model.ContentFilter{model.FilterNSFW, model.FilterNSFW, 99},
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.query.Limit != MaxLimit {
		t.Fatalf("limit=%d want %d", store.query.Limit, MaxLimit)
	}
	if len(store.query.Filters) != 1 || store.query.Filters[0] != model.FilterNSFW {
		t.Fatalf("filters=%#v", store.query.Filters)
	}
}

func TestAroundRequiresSelectedPost(t *testing.T) {
	store := &fakeStore{around: []model.PostSummary{{ID: 10}, {ID: 8}}}
	_, err := New(store).Around(context.Background(), AroundQuery{PostID: 9, Radius: 10})
	if !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("got %v want ErrPostNotFound", err)
	}
}
