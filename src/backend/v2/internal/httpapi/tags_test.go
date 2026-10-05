package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/tag"
)

// These default methods keep the shared apiStore satisfying the production Store
// interface for unrelated HTTP tests. Tag-specific tests use tagAPIStore below.
func (s *apiStore) LoadPostTags(_ context.Context, postID, _ int64) (tag.Snapshot, error) {
	return tag.Snapshot{PostID: postID, Tags: []tag.Item{}}, nil
}

func (s *apiStore) AddPostTag(_ context.Context, userID, postID int64, name tag.Name) (tag.Snapshot, error) {
	return tag.Snapshot{PostID: postID, Tags: []tag.Item{{ID: 1, Name: name.Display}}}, nil
}

func (s *apiStore) RemovePostTag(_ context.Context, userID, postID, tagID int64) (tag.Snapshot, error) {
	return tag.Snapshot{PostID: postID, Tags: []tag.Item{}}, nil
}

type tagAPIStore struct {
	*apiStore
	viewerUserID int64
	addUserID    int64
	addPostID    int64
	addedName    tag.Name
	removeUserID int64
	removePostID int64
	removeTagID  int64
	snapshot     tag.Snapshot
	listErr      error
	addErr       error
	removeErr    error
}

func (s *tagAPIStore) LoadPostTags(_ context.Context, postID, viewerUserID int64) (tag.Snapshot, error) {
	s.viewerUserID = viewerUserID
	if s.listErr != nil {
		return tag.Snapshot{}, s.listErr
	}
	result := s.snapshot
	result.PostID = postID
	if result.Tags == nil {
		result.Tags = []tag.Item{}
	}
	return result, nil
}

func (s *tagAPIStore) AddPostTag(_ context.Context, userID, postID int64, name tag.Name) (tag.Snapshot, error) {
	s.addUserID = userID
	s.addPostID = postID
	s.addedName = name
	if s.addErr != nil {
		return tag.Snapshot{}, s.addErr
	}
	result := s.snapshot
	result.PostID = postID
	if result.Tags == nil {
		result.Tags = []tag.Item{{ID: 7, Name: name.Display}}
	}
	return result, nil
}

func (s *tagAPIStore) RemovePostTag(_ context.Context, userID, postID, tagID int64) (tag.Snapshot, error) {
	s.removeUserID = userID
	s.removePostID = postID
	s.removeTagID = tagID
	if s.removeErr != nil {
		return tag.Snapshot{}, s.removeErr
	}
	result := s.snapshot
	result.PostID = postID
	if result.Tags == nil {
		result.Tags = []tag.Item{}
	}
	return result, nil
}

func TestPostTagsReadIsPublicAndUsesOptionalViewer(t *testing.T) {
	base := &apiStore{}
	store := &tagAPIStore{apiStore: base, snapshot: tag.Snapshot{Tags: []tag.Item{{ID: 7, Name: "cat"}}}}
	server := newAuthTestServer(store)

	signedOut := httptest.NewRequest(http.MethodGet, "http://ginbar.test/api/v2/posts/42/tags", nil)
	signedOutRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(signedOutRes, signedOut)
	if signedOutRes.Code != http.StatusOK || store.viewerUserID != 0 {
		t.Fatalf("status=%d viewer=%d body=%s", signedOutRes.Code, store.viewerUserID, signedOutRes.Body.String())
	}

	signedIn := authenticatedRequest(base, http.MethodGet, "/api/v2/posts/42/tags", "")
	signedInRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(signedInRes, signedIn)
	if signedInRes.Code != http.StatusOK || store.viewerUserID != 42 {
		t.Fatalf("status=%d viewer=%d body=%s", signedInRes.Code, store.viewerUserID, signedInRes.Body.String())
	}
	var snapshot tag.Snapshot
	if err := json.Unmarshal(signedInRes.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.PostID != 42 || len(snapshot.Tags) != 1 || snapshot.Tags[0].Name != "cat" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestAddPostTagRequiresAuthenticationAndNormalizesName(t *testing.T) {
	base := &apiStore{}
	store := &tagAPIStore{apiStore: base}
	server := newAuthTestServer(store)

	unauthenticated := sameOriginRequest(http.MethodPost, "/api/v2/posts/42/tags", `{"name":"cat"}`)
	unauthenticatedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthenticatedRes, unauthenticated)
	if unauthenticatedRes.Code != http.StatusUnauthorized || store.addUserID != 0 {
		t.Fatalf("status=%d addUser=%d", unauthenticatedRes.Code, store.addUserID)
	}

	authenticated := authenticatedRequest(base, http.MethodPost, "/api/v2/posts/42/tags", `{"name":"  Mixed Case  "}`)
	authenticatedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(authenticatedRes, authenticated)
	if authenticatedRes.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", authenticatedRes.Code, authenticatedRes.Body.String())
	}
	if store.addUserID != 42 || store.addPostID != 42 || store.addedName.Display != "Mixed Case" || store.addedName.Normalized != "mixed case" {
		t.Fatalf("user=%d post=%d name=%#v", store.addUserID, store.addPostID, store.addedName)
	}
}

func TestAddPostTagRejectsInvalidInputMissingPostAndCrossOrigin(t *testing.T) {
	base := &apiStore{}
	store := &tagAPIStore{apiStore: base}
	server := newAuthTestServer(store)

	invalid := authenticatedRequest(base, http.MethodPost, "/api/v2/posts/42/tags", `{"name":"   "}`)
	invalidRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalidRes, invalid)
	if invalidRes.Code != http.StatusBadRequest || store.addUserID != 0 {
		t.Fatalf("invalid status=%d addUser=%d body=%s", invalidRes.Code, store.addUserID, invalidRes.Body.String())
	}

	store.addErr = tag.ErrPostNotFound
	missing := authenticatedRequest(base, http.MethodPost, "/api/v2/posts/999/tags", `{"name":"cat"}`)
	missingRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingRes, missing)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("missing status=%d body=%s", missingRes.Code, missingRes.Body.String())
	}

	store.addErr = nil
	store.addUserID = 0
	crossOrigin := authenticatedRequest(base, http.MethodPost, "/api/v2/posts/42/tags", `{"name":"cat"}`)
	crossOrigin.Header.Set("Origin", "https://evil.test")
	crossOriginRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(crossOriginRes, crossOrigin)
	if crossOriginRes.Code != http.StatusForbidden || store.addUserID != 0 {
		t.Fatalf("cross-origin status=%d addUser=%d", crossOriginRes.Code, store.addUserID)
	}
}

func TestRemovePostTagAuthorizationAndIdempotentResponseContract(t *testing.T) {
	base := &apiStore{}
	store := &tagAPIStore{apiStore: base, removeErr: tag.ErrForbidden}
	server := newAuthTestServer(store)

	unauthenticated := sameOriginRequest(http.MethodDelete, "/api/v2/posts/42/tags/7", "")
	unauthenticatedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthenticatedRes, unauthenticated)
	if unauthenticatedRes.Code != http.StatusUnauthorized || store.removeUserID != 0 {
		t.Fatalf("unauthenticated status=%d removeUser=%d", unauthenticatedRes.Code, store.removeUserID)
	}

	forbidden := authenticatedRequest(base, http.MethodDelete, "/api/v2/posts/42/tags/7", "")
	forbiddenRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(forbiddenRes, forbidden)
	if forbiddenRes.Code != http.StatusForbidden || store.removeUserID != 42 || store.removeTagID != 7 {
		t.Fatalf("forbidden status=%d user=%d tag=%d body=%s", forbiddenRes.Code, store.removeUserID, store.removeTagID, forbiddenRes.Body.String())
	}

	store.removeErr = nil
	store.snapshot = tag.Snapshot{CanRemove: true, Tags: []tag.Item{}}
	allowed := authenticatedRequest(base, http.MethodDelete, "/api/v2/posts/42/tags/7", "")
	allowedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(allowedRes, allowed)
	if allowedRes.Code != http.StatusOK {
		t.Fatalf("allowed status=%d body=%s", allowedRes.Code, allowedRes.Body.String())
	}
	var snapshot tag.Snapshot
	if err := json.Unmarshal(allowedRes.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.PostID != 42 || len(snapshot.Tags) != 0 || !snapshot.CanRemove {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestRemovePostTagRejectsInvalidIDAndCrossOriginBeforeMutation(t *testing.T) {
	base := &apiStore{}
	store := &tagAPIStore{apiStore: base}
	server := newAuthTestServer(store)

	invalid := authenticatedRequest(base, http.MethodDelete, "/api/v2/posts/42/tags/0", "")
	invalidRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(invalidRes, invalid)
	if invalidRes.Code != http.StatusBadRequest || store.removeUserID != 0 {
		t.Fatalf("invalid status=%d user=%d", invalidRes.Code, store.removeUserID)
	}

	crossOrigin := authenticatedRequest(base, http.MethodDelete, "/api/v2/posts/42/tags/7", "")
	crossOrigin.Header.Set("Origin", "https://evil.test")
	crossOriginRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(crossOriginRes, crossOrigin)
	if crossOriginRes.Code != http.StatusForbidden || store.removeUserID != 0 {
		t.Fatalf("cross-origin status=%d user=%d", crossOriginRes.Code, store.removeUserID)
	}
}

func TestPostTagsMapsStoreErrors(t *testing.T) {
	base := &apiStore{}
	store := &tagAPIStore{apiStore: base, listErr: tag.ErrPostNotFound}
	res := httptest.NewRecorder()
	newAuthTestServer(store).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://ginbar.test/api/v2/posts/999/tags", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}

	store.listErr = errors.New("boom")
	res = httptest.NewRecorder()
	newAuthTestServer(store).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://ginbar.test/api/v2/posts/42/tags", nil))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
