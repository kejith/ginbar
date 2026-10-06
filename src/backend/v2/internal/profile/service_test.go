package profile

import (
	"context"
	"errors"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

type fakeStore struct {
	user      User
	posts     []model.PostSummary
	loadErr   error
	listErr   error
	userID    int64
	lastQuery Query
}

func (s *fakeStore) LoadPublicUser(_ context.Context, userID int64) (User, error) {
	s.userID = userID
	if s.loadErr != nil {
		return User{}, s.loadErr
	}
	return s.user, nil
}

func (s *fakeStore) ListProfilePosts(_ context.Context, query Query) ([]model.PostSummary, error) {
	s.lastQuery = query
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.posts, nil
}

func TestGetValidatesAndNormalizesQuery(t *testing.T) {
	store := &fakeStore{
		user:  User{ID: 42, Username: "Alice"},
		posts: []model.PostSummary{{ID: 5}, {ID: 4}, {ID: 3}},
	}
	page, err := New(store).Get(context.Background(), Query{UserID: 42, Before: 50, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if store.userID != 42 || store.lastQuery.UserID != 42 || store.lastQuery.Before != 50 || store.lastQuery.Limit != 2 {
		t.Fatalf("store calls user=%d query=%#v", store.userID, store.lastQuery)
	}
	if len(page.Posts) != 2 || page.Posts[0].ID != 5 || page.Posts[1].ID != 4 || page.NextBefore != 4 {
		t.Fatalf("page=%#v", page)
	}
}

func TestGetUsesBoundedDefaultAndEmptyArray(t *testing.T) {
	store := &fakeStore{user: User{ID: 42, Username: "Alice"}}
	page, err := New(store).Get(context.Background(), Query{UserID: 42, Limit: MaxLimit + 100})
	if err != nil {
		t.Fatal(err)
	}
	if store.lastQuery.Limit != MaxLimit {
		t.Fatalf("limit=%d", store.lastQuery.Limit)
	}
	if page.Posts == nil || len(page.Posts) != 0 || page.NextBefore != 0 {
		t.Fatalf("page=%#v", page)
	}

	if _, err := New(store).Get(context.Background(), Query{UserID: 42}); err != nil {
		t.Fatal(err)
	}
	if store.lastQuery.Limit != DefaultLimit {
		t.Fatalf("default limit=%d", store.lastQuery.Limit)
	}
}

func TestGetRejectsInvalidIdentityAndCursor(t *testing.T) {
	service := New(&fakeStore{})
	for _, query := range []Query{{UserID: 0}, {UserID: -1}, {UserID: 1, Before: -1}} {
		if _, err := service.Get(context.Background(), query); err == nil {
			t.Fatalf("query %#v unexpectedly succeeded", query)
		}
	}
}

func TestGetPropagatesNotFoundAndListErrors(t *testing.T) {
	store := &fakeStore{loadErr: ErrUserNotFound}
	if _, err := New(store).Get(context.Background(), Query{UserID: 42}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("not-found error=%v", err)
	}

	want := errors.New("list failed")
	store = &fakeStore{user: User{ID: 42}, listErr: want}
	if _, err := New(store).Get(context.Background(), Query{UserID: 42}); !errors.Is(err, want) {
		t.Fatalf("list error=%v", err)
	}
}
