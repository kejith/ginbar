package duplicate

import (
	"context"
	"reflect"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

type recordingStore struct{ query Query }

func (s *recordingStore) ListDuplicateCandidates(_ context.Context, q Query) ([]Candidate, error) {
	s.query = q
	return []Candidate{{PostID: 42}}, nil
}

func TestListNormalizesBoundsAndVisibility(t *testing.T) {
	store := &recordingStore{}
	service := New(store)
	got, err := service.List(context.Background(), Query{
		PostID: 7,
		Limit:  MaxLimit + 1,
		Filters: []model.ContentFilter{
			model.FilterNSFW,
			model.FilterNSFW,
			model.ContentFilter(99),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []Candidate{{PostID: 42}}) {
		t.Fatalf("candidates = %#v", got)
	}
	if store.query.Limit != MaxLimit {
		t.Fatalf("limit = %d, want %d", store.query.Limit, MaxLimit)
	}
	if !reflect.DeepEqual(store.query.Filters, []model.ContentFilter{model.FilterNSFW}) {
		t.Fatalf("filters = %#v", store.query.Filters)
	}
}

func TestListDefaultsToSFWAndRejectsInvalidPostID(t *testing.T) {
	store := &recordingStore{}
	service := New(store)
	if _, err := service.List(context.Background(), Query{PostID: 0}); err == nil {
		t.Fatal("invalid post id unexpectedly accepted")
	}
	if _, err := service.List(context.Background(), Query{PostID: 7}); err != nil {
		t.Fatal(err)
	}
	if store.query.Limit != DefaultLimit {
		t.Fatalf("limit = %d, want %d", store.query.Limit, DefaultLimit)
	}
	if !reflect.DeepEqual(store.query.Filters, []model.ContentFilter{model.FilterSFW}) {
		t.Fatalf("filters = %#v", store.query.Filters)
	}
}
