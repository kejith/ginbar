package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/mediastatus"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

type apiStore struct {
	posts  []model.PostSummary
	query  feed.Query
	wait   bool
	status *mediastatus.Snapshot
}

func (s *apiStore) ListFeed(ctx context.Context, q feed.Query) ([]model.PostSummary, error) {
	s.query = q
	if s.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.posts, nil
}

func (s *apiStore) AroundPost(_ context.Context, q feed.AroundQuery) ([]model.PostSummary, error) {
	for _, post := range s.posts {
		if post.ID == q.PostID {
			return s.posts, nil
		}
	}
	return nil, nil
}

func (s *apiStore) LoadMediaStatus(ctx context.Context, postID int64) (mediastatus.Snapshot, error) {
	if s.wait {
		<-ctx.Done()
		return mediastatus.Snapshot{}, ctx.Err()
	}
	if s.status == nil || s.status.PostID != postID {
		return mediastatus.Snapshot{}, mediastatus.ErrPostNotFound
	}
	return *s.status, nil
}

func TestFeedContract(t *testing.T) {
	store := &apiStore{posts: []model.PostSummary{{ID: 42}, {ID: 41}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/feed?before=50&limit=1&q=cat+-anime+score:%3E%3D100", nil)
	res := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if store.query.Before != 50 || store.query.Limit != 1 || len(store.query.Search.IncludeTags) != 1 {
		t.Fatalf("unexpected parsed query: %#v", store.query)
	}
	var body feed.Page
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Posts) != 1 || body.NextBefore != 42 {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestFeedAcceptsHyphenatedTagSmokeQuery(t *testing.T) {
	store := &apiStore{}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/feed?before=50000&limit=60&q=tag-42%20score:%3E%3D100", nil)
	res := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if got := store.query.Search.IncludeTags; len(got) != 1 || got[0] != "tag-42" {
		t.Fatalf("unexpected include tags: %#v", got)
	}
	if store.query.Search.Score == nil || store.query.Search.Score.Value != 100 {
		t.Fatalf("unexpected score predicate: %#v", store.query.Search.Score)
	}
}

func TestFeedRejectsMalformedSearch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/feed?q=score:100", nil)
	res := httptest.NewRecorder()
	New(&apiStore{}).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestAroundReturns404WhenSelectedPostMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/posts/99/around", nil)
	res := httptest.NewRecorder()
	New(&apiStore{posts: []model.PostSummary{{ID: 98}}}).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestMediaStatusReturnsReleasedRegenerationState(t *testing.T) {
	store := &apiStore{status: &mediastatus.Snapshot{
		PostID:       42,
		ReleaseState: 1,
		MediaReady:   true,
		Job: &mediastatus.JobSnapshot{
			State:       mediastatus.JobStatePending,
			MaxAttempts: 5,
		},
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/posts/42/media-status", nil)
	res := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body mediastatus.Status
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Phase != mediastatus.PhaseWaiting || body.Operation != mediastatus.OperationRegeneration || !body.UsableMedia {
		t.Fatalf("body = %#v", body)
	}
}

func TestMediaStatusDoesNotExposeInitialIngestion(t *testing.T) {
	store := &apiStore{status: &mediastatus.Snapshot{
		PostID: 42,
		Job:    &mediastatus.JobSnapshot{State: mediastatus.JobStateFailed, MaxAttempts: 5},
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/posts/42/media-status", nil)
	res := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body errorEnvelope
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "post_not_found" {
		t.Fatalf("body = %#v", body)
	}
}

func TestRequestDeadlineCancelsStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/feed", nil)
	res := httptest.NewRecorder()
	newServer(&apiStore{wait: true}, 2*time.Millisecond).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
