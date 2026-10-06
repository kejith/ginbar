package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/profile"
)

// Keep the shared apiStore satisfying the production Store interface for unrelated HTTP tests.
func (s *apiStore) LoadPublicUser(_ context.Context, _ int64) (profile.User, error) {
	return profile.User{}, profile.ErrUserNotFound
}

func (s *apiStore) ListProfilePosts(_ context.Context, _ profile.Query) ([]model.PostSummary, error) {
	return []model.PostSummary{}, nil
}

type profileAPIStore struct {
	*apiStore
	user      profile.User
	posts     []model.PostSummary
	loadErr   error
	listErr   error
	userID    int64
	lastQuery profile.Query
}

func (s *profileAPIStore) LoadPublicUser(_ context.Context, userID int64) (profile.User, error) {
	s.userID = userID
	if s.loadErr != nil {
		return profile.User{}, s.loadErr
	}
	return s.user, nil
}

func (s *profileAPIStore) ListProfilePosts(_ context.Context, query profile.Query) ([]model.PostSummary, error) {
	s.lastQuery = query
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.posts, nil
}

func TestProfileReadIsPublicAndCursorBounded(t *testing.T) {
	createdAt := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	store := &profileAPIStore{
		apiStore: &apiStore{},
		user:     profile.User{ID: 42, Username: "Alice", CreatedAt: createdAt},
		posts:    []model.PostSummary{{ID: 49, AuthorID: 42}, {ID: 48, AuthorID: 42}},
	}
	server := newAuthTestServer(store)

	request := httptest.NewRequest(http.MethodGet, "http://ginbar.test/api/v2/users/42?before=50&limit=1", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if store.userID != 42 || store.lastQuery.UserID != 42 || store.lastQuery.Before != 50 || store.lastQuery.Limit != 1 {
		t.Fatalf("user=%d query=%#v", store.userID, store.lastQuery)
	}
	var page profile.Page
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.User.ID != 42 || page.User.Username != "Alice" || len(page.Posts) != 1 || page.Posts[0].ID != 49 || page.NextBefore != 49 {
		t.Fatalf("page=%#v", page)
	}
}

func TestProfileReadHasSamePublicContractForAuthenticatedSelf(t *testing.T) {
	base := &apiStore{}
	store := &profileAPIStore{
		apiStore: base,
		user:     profile.User{ID: 42, Username: "Alice"},
	}
	server := newAuthTestServer(store)
	request := authenticatedRequest(base, http.MethodGet, "/api/v2/users/42", "")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var page profile.Page
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.User.ID != 42 || page.User.Username != "Alice" {
		t.Fatalf("page=%#v", page)
	}
}

func TestProfileReadRejectsInvalidInputAndMissingUsers(t *testing.T) {
	server := New(&profileAPIStore{apiStore: &apiStore{}, loadErr: profile.ErrUserNotFound})
	for _, path := range []string{
		"/api/v2/users/0",
		"/api/v2/users/not-a-number",
		"/api/v2/users/42?before=-1",
		"/api/v2/users/42?limit=-1",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v2/users/42", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "user_not_found" {
		t.Fatalf("body=%#v", body)
	}
}
