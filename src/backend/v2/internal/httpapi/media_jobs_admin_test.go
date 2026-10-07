package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/mediajobadmin"
)

func (s *apiStore) ListMediaJobs(_ context.Context, _, _ int64, _ int) ([]mediajobadmin.Record, error) {
	return []mediajobadmin.Record{}, nil
}

type mediaJobsAPIStore struct {
	*apiStore
	records []mediajobadmin.Record
	err     error
	actorID int64
	before  int64
	limit   int
	calls   int
}

func (s *mediaJobsAPIStore) ListMediaJobs(
	_ context.Context,
	actorID, before int64,
	limit int,
) ([]mediajobadmin.Record, error) {
	s.actorID = actorID
	s.before = before
	s.limit = limit
	s.calls++
	return s.records, s.err
}

func TestMediaJobsAdminRequiresAuthenticationAndModeratorRole(t *testing.T) {
	base := &apiStore{}
	store := &mediaJobsAPIStore{apiStore: base}
	server := newAuthTestServer(store)

	signedOut := httptest.NewRequest(http.MethodGet, "http://ginbar.test/api/v2/admin/media-jobs", nil)
	signedOutRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(signedOutRes, signedOut)
	if signedOutRes.Code != http.StatusUnauthorized || store.calls != 0 ||
		!strings.Contains(signedOutRes.Body.String(), `"code":"unauthenticated"`) {
		t.Fatalf("signed-out status=%d calls=%d body=%s", signedOutRes.Code, store.calls, signedOutRes.Body.String())
	}

	store.err = mediajobadmin.ErrForbidden
	forbidden := authenticatedRequest(base, http.MethodGet, "/api/v2/admin/media-jobs", "")
	forbiddenRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(forbiddenRes, forbidden)
	if forbiddenRes.Code != http.StatusForbidden || store.calls != 1 || store.actorID != 42 ||
		!strings.Contains(forbiddenRes.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("forbidden status=%d calls=%d actor=%d body=%s", forbiddenRes.Code, store.calls, store.actorID, forbiddenRes.Body.String())
	}
}

func TestMediaJobsAdminReturnsBoundedCursorPage(t *testing.T) {
	base := &apiStore{}
	now := time.Unix(200, 0).UTC()
	claimedBy := "worker-a"
	lastError := "decoder failed"
	store := &mediaJobsAPIStore{
		apiStore: base,
		records: []mediajobadmin.Record{
			{
				ID: 12, PostID: 102, Kind: 0, State: mediajobadmin.StateFailed, Attempts: 5, MaxAttempts: 5,
				AvailableAt: now, LastError: &lastError, CreatedAt: now, UpdatedAt: now,
			},
			{
				ID: 11, PostID: 101, Kind: 0, State: mediajobadmin.StateRunning, Attempts: 2, MaxAttempts: 5,
				AvailableAt: now, ClaimedAt: &now, ClaimedBy: &claimedBy, LeaseExpiresAt: &now,
				LeaseGeneration: 3, CreatedAt: now, UpdatedAt: now,
			},
			{ID: 10, PostID: 100, Kind: 0, State: mediajobadmin.StatePending, Attempts: 1, MaxAttempts: 5, AvailableAt: now, CreatedAt: now, UpdatedAt: now},
		},
	}
	res := httptest.NewRecorder()
	newAuthTestServer(store).Handler().ServeHTTP(
		res,
		authenticatedRequest(base, http.MethodGet, "/api/v2/admin/media-jobs?before=20&limit=2", ""),
	)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if store.calls != 1 || store.actorID != 42 || store.before != 20 || store.limit != 3 {
		t.Fatalf("calls=%d actor=%d before=%d limit=%d", store.calls, store.actorID, store.before, store.limit)
	}

	var page mediajobadmin.Page
	if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Jobs) != 2 || page.Jobs[0].ID != 12 || page.Jobs[0].State != "failed" ||
		page.Jobs[1].ID != 11 || page.Jobs[1].State != "running" || page.NextBefore != 11 {
		t.Fatalf("page=%#v", page)
	}
	if page.Jobs[0].LastError == nil || *page.Jobs[0].LastError != lastError ||
		page.Jobs[1].ClaimedBy == nil || *page.Jobs[1].ClaimedBy != claimedBy {
		t.Fatalf("operational metadata missing: %#v", page.Jobs)
	}
	for _, forbiddenField := range []string{"sourceUrl", "storageKey", "sha256", "credential"} {
		if strings.Contains(res.Body.String(), forbiddenField) {
			t.Fatalf("response exposed unexpected field %q: %s", forbiddenField, res.Body.String())
		}
	}
}

func TestMediaJobsAdminRejectsMalformedCursorAndLimitBeforeStoreRead(t *testing.T) {
	base := &apiStore{}
	store := &mediaJobsAPIStore{apiStore: base}
	server := newAuthTestServer(store)

	for _, path := range []string{
		"/api/v2/admin/media-jobs?before=0",
		"/api/v2/admin/media-jobs?before=nope",
		"/api/v2/admin/media-jobs?limit=0",
		"/api/v2/admin/media-jobs?limit=nope",
	} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodGet, path, ""))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	if store.calls != 0 {
		t.Fatalf("invalid requests reached store: calls=%d", store.calls)
	}
}
