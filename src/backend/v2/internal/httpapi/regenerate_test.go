package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/auth"
	"github.com/kejith/ginbar/backend/v2/internal/regenerate"
)

func (s *apiStore) RequestRegeneration(_ context.Context, _, _ int64) (regenerate.Requested, error) {
	return regenerate.Requested{}, regenerate.ErrForbidden
}

type regenerationAPIStore struct {
	*apiStore
	result      regenerate.Requested
	err         error
	actorUserID int64
	postID      int64
	calls       int
}

func (s *regenerationAPIStore) RequestRegeneration(
	_ context.Context,
	actorUserID, postID int64,
) (regenerate.Requested, error) {
	s.actorUserID = actorUserID
	s.postID = postID
	s.calls++
	return s.result, s.err
}

func TestRegenerationMutationRequiresAuthentication(t *testing.T) {
	base := &apiStore{}
	store := &regenerationAPIStore{apiStore: base}
	req := sameOriginRequest(http.MethodPost, "/api/v2/admin/posts/42/regeneration", "")
	res := httptest.NewRecorder()

	newAuthTestServer(store).Handler().ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized || store.calls != 0 ||
		!strings.Contains(res.Body.String(), `"code":"unauthenticated"`) {
		t.Fatalf("status=%d calls=%d body=%s", res.Code, store.calls, res.Body.String())
	}
}

func TestRegenerationMutationRejectsMemberAndModerator(t *testing.T) {
	for _, tc := range []struct {
		name   string
		userID int64
	}{
		{name: "member", userID: 42},
		{name: "moderator", userID: 43},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &apiStore{}
			store := &regenerationAPIStore{apiStore: base, err: regenerate.ErrForbidden}
			req := authenticatedRequest(base, http.MethodPost, "/api/v2/admin/posts/42/regeneration", "")
			base.principal = auth.Principal{UserID: tc.userID, Username: tc.name}
			res := httptest.NewRecorder()

			newAuthTestServer(store).Handler().ServeHTTP(res, req)

			if res.Code != http.StatusForbidden || store.calls != 1 || store.actorUserID != tc.userID ||
				!strings.Contains(res.Body.String(), `"code":"forbidden"`) {
				t.Fatalf(
					"status=%d calls=%d actor=%d body=%s",
					res.Code,
					store.calls,
					store.actorUserID,
					res.Body.String(),
				)
			}
		})
	}
}

func TestRegenerationMutationReturnsAuthoritativeOutcome(t *testing.T) {
	for _, outcome := range []regenerate.Outcome{
		regenerate.OutcomeQueued,
		regenerate.OutcomeCoalesced,
		regenerate.OutcomeSuperseded,
	} {
		t.Run(string(outcome), func(t *testing.T) {
			base := &apiStore{}
			store := &regenerationAPIStore{
				apiStore: base,
				result: regenerate.Requested{
					JobID:   91,
					Outcome: outcome,
				},
			}
			req := authenticatedRequest(base, http.MethodPost, "/api/v2/admin/posts/42/regeneration", "")
			res := httptest.NewRecorder()

			newAuthTestServer(store).Handler().ServeHTTP(res, req)

			if res.Code != http.StatusAccepted || store.calls != 1 ||
				store.actorUserID != 42 || store.postID != 42 {
				t.Fatalf(
					"status=%d calls=%d actor=%d post=%d body=%s",
					res.Code,
					store.calls,
					store.actorUserID,
					store.postID,
					res.Body.String(),
				)
			}
			var body regenerationResponse
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.PostID != 42 || body.JobID != 91 || body.Outcome != outcome {
				t.Fatalf("body=%#v", body)
			}
		})
	}
}

func TestRegenerationMutationRejectsCrossOriginBeforeStoreMutation(t *testing.T) {
	base := &apiStore{}
	store := &regenerationAPIStore{apiStore: base}
	req := authenticatedRequest(base, http.MethodPost, "/api/v2/admin/posts/42/regeneration", "")
	req.Header.Set("Origin", "https://evil.test")
	res := httptest.NewRecorder()

	newAuthTestServer(store).Handler().ServeHTTP(res, req)

	if res.Code != http.StatusForbidden || store.calls != 0 ||
		!strings.Contains(res.Body.String(), `"code":"origin_not_allowed"`) {
		t.Fatalf("status=%d calls=%d body=%s", res.Code, store.calls, res.Body.String())
	}
}

func TestRegenerationMutationRejectsInvalidPostIDBeforeStoreMutation(t *testing.T) {
	base := &apiStore{}
	store := &regenerationAPIStore{apiStore: base}
	for _, id := range []string{"0", "-1", "not-a-number"} {
		req := authenticatedRequest(base, http.MethodPost, "/api/v2/admin/posts/"+id+"/regeneration", "")
		res := httptest.NewRecorder()

		newAuthTestServer(store).Handler().ServeHTTP(res, req)

		if res.Code != http.StatusBadRequest ||
			!strings.Contains(res.Body.String(), `"code":"invalid_post_id"`) {
			t.Fatalf("id=%q status=%d body=%s", id, res.Code, res.Body.String())
		}
	}
	if store.calls != 0 {
		t.Fatalf("invalid post ids reached store: calls=%d", store.calls)
	}
}

func TestRegenerationMutationMapsNotRegenerableAndInternalErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "not-regenerable",
			err:        regenerate.ErrNotRegenerable,
			wantStatus: http.StatusConflict,
			wantCode:   "post_not_regenerable",
		},
		{
			name:       "internal",
			err:        errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &apiStore{}
			store := &regenerationAPIStore{apiStore: base, err: tc.err}
			req := authenticatedRequest(base, http.MethodPost, "/api/v2/admin/posts/42/regeneration", "")
			res := httptest.NewRecorder()

			newAuthTestServer(store).Handler().ServeHTTP(res, req)

			if res.Code != tc.wantStatus || store.calls != 1 ||
				!strings.Contains(res.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("status=%d calls=%d body=%s", res.Code, store.calls, res.Body.String())
			}
		})
	}
}
