package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/roleadmin"
)

func (s *apiStore) GetUserRoleState(_ context.Context, _, targetUserID int64) (roleadmin.State, error) {
	return roleadmin.State{UserID: targetUserID}, nil
}

func (s *apiStore) GrantModerator(_ context.Context, _, targetUserID int64) (roleadmin.State, error) {
	return roleadmin.State{UserID: targetUserID, Moderator: true}, nil
}

func (s *apiStore) RevokeModerator(_ context.Context, _, targetUserID int64) (roleadmin.State, error) {
	return roleadmin.State{UserID: targetUserID}, nil
}

type roleAdminAPIStore struct {
	*apiStore
	readState   roleadmin.State
	grantState  roleadmin.State
	revokeState roleadmin.State
	err         error
	actorID     int64
	targetID    int64
	action      string
	calls       int
}

func (s *roleAdminAPIStore) GetUserRoleState(
	_ context.Context,
	actorUserID, targetUserID int64,
) (roleadmin.State, error) {
	s.record("read", actorUserID, targetUserID)
	return s.readState, s.err
}

func (s *roleAdminAPIStore) GrantModerator(
	_ context.Context,
	actorUserID, targetUserID int64,
) (roleadmin.State, error) {
	s.record("grant", actorUserID, targetUserID)
	return s.grantState, s.err
}

func (s *roleAdminAPIStore) RevokeModerator(
	_ context.Context,
	actorUserID, targetUserID int64,
) (roleadmin.State, error) {
	s.record("revoke", actorUserID, targetUserID)
	return s.revokeState, s.err
}

func (s *roleAdminAPIStore) record(action string, actorUserID, targetUserID int64) {
	s.action = action
	s.actorID = actorUserID
	s.targetID = targetUserID
	s.calls++
}

func TestRoleAdminRoutesRequireAuthentication(t *testing.T) {
	base := &apiStore{}
	store := &roleAdminAPIStore{apiStore: base}
	server := newAuthTestServer(store)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v2/admin/users/77/roles"},
		{http.MethodPut, "/api/v2/admin/users/77/roles/moderator"},
		{http.MethodDelete, "/api/v2/admin/users/77/roles/moderator"},
	} {
		req := httptest.NewRequest(tc.method, "http://ginbar.test"+tc.path, nil)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized ||
			!strings.Contains(res.Body.String(), `"code":"unauthenticated"`) {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, res.Code, res.Body.String())
		}
	}
	if store.calls != 0 {
		t.Fatalf("signed-out requests reached role store: calls=%d", store.calls)
	}
}

func TestRoleAdminReadGrantAndRevokeContracts(t *testing.T) {
	base := &apiStore{}
	grantor := int64(42)
	adminGrantor := int64(7)
	store := &roleAdminAPIStore{
		apiStore: base,
		readState: roleadmin.State{
			UserID:                   77,
			Moderator:                true,
			ModeratorGrantedByUserID: &grantor,
			Admin:                    true,
			AdminGrantedByUserID:     &adminGrantor,
		},
		grantState: roleadmin.State{
			UserID:                   77,
			Moderator:                true,
			ModeratorGrantedByUserID: &grantor,
		},
		revokeState: roleadmin.State{UserID: 77},
	}
	server := newAuthTestServer(store)

	tests := []struct {
		method string
		path   string
		action string
		want   roleadmin.State
	}{
		{http.MethodGet, "/api/v2/admin/users/77/roles", "read", store.readState},
		{http.MethodPut, "/api/v2/admin/users/77/roles/moderator", "grant", store.grantState},
		{http.MethodDelete, "/api/v2/admin/users/77/roles/moderator", "revoke", store.revokeState},
	}
	for _, tc := range tests {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, tc.method, tc.path, ""))
		if res.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", tc.action, res.Code, res.Body.String())
		}
		var got roleadmin.State
		if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.UserID != tc.want.UserID || got.Moderator != tc.want.Moderator || got.Admin != tc.want.Admin ||
			!sameOptionalInt64(got.ModeratorGrantedByUserID, tc.want.ModeratorGrantedByUserID) ||
			!sameOptionalInt64(got.AdminGrantedByUserID, tc.want.AdminGrantedByUserID) {
			t.Fatalf("%s state=%#v want=%#v", tc.action, got, tc.want)
		}
		if store.action != tc.action || store.actorID != 42 || store.targetID != 77 {
			t.Fatalf("%s action=%s actor=%d target=%d", tc.action, store.action, store.actorID, store.targetID)
		}
	}
}

func TestRoleAdminMapsForbiddenMissingAndInvalidTarget(t *testing.T) {
	base := &apiStore{}
	store := &roleAdminAPIStore{apiStore: base}
	server := newAuthTestServer(store)

	store.err = roleadmin.ErrForbidden
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		path := "/api/v2/admin/users/77/roles"
		if method != http.MethodGet {
			path += "/moderator"
		}
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, method, path, ""))
		if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), `"code":"forbidden"`) {
			t.Fatalf("%s forbidden status=%d body=%s", method, res.Code, res.Body.String())
		}
	}

	store.err = roleadmin.ErrUserNotFound
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPut, "/api/v2/admin/users/999/roles/moderator", ""))
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"code":"user_not_found"`) {
		t.Fatalf("missing status=%d body=%s", res.Code, res.Body.String())
	}

	calls := store.calls
	for _, id := range []string{"0", "-1", "not-a-number"} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(
			res,
			authenticatedRequest(base, http.MethodGet, "/api/v2/admin/users/"+id+"/roles", ""),
		)
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"invalid_user_id"`) {
			t.Fatalf("id=%s status=%d body=%s", id, res.Code, res.Body.String())
		}
	}
	if store.calls != calls {
		t.Fatalf("invalid targets reached role store: before=%d after=%d", calls, store.calls)
	}
}

func TestRoleAdminDoesNotExposeAdminRoleMutation(t *testing.T) {
	base := &apiStore{}
	store := &roleAdminAPIStore{apiStore: base}
	res := httptest.NewRecorder()
	newAuthTestServer(store).Handler().ServeHTTP(
		res,
		authenticatedRequest(base, http.MethodPut, "/api/v2/admin/users/77/roles/admin", ""),
	)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("admin-role path reached role store: calls=%d", store.calls)
	}
}

func sameOptionalInt64(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
