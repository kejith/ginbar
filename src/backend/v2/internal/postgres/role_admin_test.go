package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/httpapi"
	"github.com/kejith/ginbar/backend/v2/internal/moderation"
	"github.com/kejith/ginbar/backend/v2/internal/role"
	"github.com/kejith/ginbar/backend/v2/internal/roleadmin"
)

func TestHTTPRoleAdminAuthorizationIdempotenceAndImmediateModeration(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	memberID := createRoleAdminUser(t, store, "role-member")
	moderatorID := createRoleAdminUser(t, store, "role-moderator")
	adminID := createRoleAdminUser(t, store, "role-admin")
	targetID := createRoleAdminUser(t, store, "role-target")
	grantRole(t, store, moderatorID, role.Moderator)
	grantRole(t, store, adminID, role.Admin)

	memberToken := createRoleAdminSession(t, store, memberID)
	moderatorToken := createRoleAdminSession(t, store, moderatorID)
	adminToken := createRoleAdminSession(t, store, adminID)

	cfg := httpapi.DefaultConfig()
	cfg.CookieSecure = false
	server := httpapi.NewWithConfig(store, cfg)

	res := serveRoleAdminRequest(server, http.MethodGet, "/api/v2/admin/users/"+fmt.Sprint(targetID)+"/roles", "")
	assertRoleAdminError(t, res, http.StatusUnauthorized, "unauthenticated")

	for _, token := range []string{memberToken, moderatorToken} {
		for _, request := range []struct {
			method string
			path   string
		}{
			{http.MethodGet, "/api/v2/admin/users/" + fmt.Sprint(targetID) + "/roles"},
			{http.MethodPut, "/api/v2/admin/users/" + fmt.Sprint(targetID) + "/roles/moderator"},
			{http.MethodDelete, "/api/v2/admin/users/" + fmt.Sprint(targetID) + "/roles/moderator"},
		} {
			res = serveRoleAdminRequest(server, request.method, request.path, token)
			assertRoleAdminError(t, res, http.StatusForbidden, "forbidden")
		}
	}

	res = serveRoleAdminRequest(server, http.MethodGet, "/api/v2/admin/users/"+fmt.Sprint(targetID)+"/roles", adminToken)
	state := decodeRoleAdminState(t, res)
	if state.UserID != targetID || state.Moderator || state.Admin {
		t.Fatalf("initial state=%#v", state)
	}

	res = serveRoleAdminRequest(server, http.MethodGet, "/api/v2/admin/users/999999999/roles", adminToken)
	assertRoleAdminError(t, res, http.StatusNotFound, "user_not_found")

	res = serveRoleAdminRequest(server, http.MethodGet, "/api/v2/admin/users/not-a-number/roles", adminToken)
	assertRoleAdminError(t, res, http.StatusBadRequest, "invalid_user_id")

	res = serveRoleAdminRequest(server, http.MethodPut, "/api/v2/admin/users/"+fmt.Sprint(targetID)+"/roles/admin", adminToken)
	if res.Code != http.StatusNotFound {
		t.Fatalf("admin-role mutation status=%d body=%s", res.Code, res.Body.String())
	}

	noOriginReq := httptest.NewRequest(
		http.MethodPut,
		"http://ginbar.test/api/v2/admin/users/"+fmt.Sprint(targetID)+"/roles/moderator",
		nil,
	)
	noOriginReq.AddCookie(&http.Cookie{Name: "ginbar_session", Value: adminToken})
	noOriginRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(noOriginRes, noOriginReq)
	assertRoleAdminError(t, noOriginRes, http.StatusForbidden, "origin_not_allowed")

	for i := 0; i < 2; i++ {
		res = serveRoleAdminRequest(
			server,
			http.MethodPut,
			"/api/v2/admin/users/"+fmt.Sprint(targetID)+"/roles/moderator",
			adminToken,
		)
		state = decodeRoleAdminState(t, res)
		if !state.Moderator || state.ModeratorGrantedByUserID == nil ||
			*state.ModeratorGrantedByUserID != adminID {
			t.Fatalf("grant %d state=%#v", i, state)
		}
	}
	var roleCount int
	var grantedByUserID int64
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*), min(granted_by_user_id)
		FROM user_roles
		WHERE user_id = $1
		  AND role = $2
	`, targetID, role.Moderator).Scan(&roleCount, &grantedByUserID); err != nil {
		t.Fatal(err)
	}
	if roleCount != 1 || grantedByUserID != adminID {
		t.Fatalf("moderator rows=%d grantor=%d", roleCount, grantedByUserID)
	}

	postID := createRoleAdminPost(t, store, memberID)
	if result, err := store.HidePost(ctx, targetID, postID); err != nil || result.ModeratedByUserID != targetID {
		t.Fatalf("new moderator hide post result=%#v err=%v", result, err)
	}

	for i := 0; i < 2; i++ {
		res = serveRoleAdminRequest(
			server,
			http.MethodDelete,
			"/api/v2/admin/users/"+fmt.Sprint(targetID)+"/roles/moderator",
			adminToken,
		)
		state = decodeRoleAdminState(t, res)
		if state.Moderator || state.ModeratorGrantedByUserID != nil {
			t.Fatalf("revoke %d state=%#v", i, state)
		}
	}
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM user_roles
		WHERE user_id = $1
		  AND role = $2
	`, targetID, role.Moderator).Scan(&roleCount); err != nil {
		t.Fatal(err)
	}
	if roleCount != 0 {
		t.Fatalf("moderator rows after revoke=%d", roleCount)
	}

	postID = createRoleAdminPost(t, store, memberID)
	if _, err := store.HidePost(ctx, targetID, postID); !errors.Is(err, moderation.ErrForbidden) {
		t.Fatalf("revoked moderator hide error=%v", err)
	}
}

func TestRoleAdminGrantPreservesOriginalGrantorAndConcurrentOperationsStayUnique(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	adminA := createRoleAdminUser(t, store, "role-admin-a")
	adminB := createRoleAdminUser(t, store, "role-admin-b")
	targetID := createRoleAdminUser(t, store, "role-concurrent-target")
	grantRole(t, store, adminA, role.Admin)
	grantRole(t, store, adminB, role.Admin)

	first, err := store.GrantModerator(ctx, adminA, targetID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.GrantModerator(ctx, adminB, targetID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ModeratorGrantedByUserID == nil || second.ModeratorGrantedByUserID == nil ||
		*first.ModeratorGrantedByUserID != adminA || *second.ModeratorGrantedByUserID != adminA {
		t.Fatalf("grant audit first=%#v second=%#v", first, second)
	}

	if _, err := store.RevokeModerator(ctx, adminA, targetID); err != nil {
		t.Fatal(err)
	}

	const workers = 12
	start := make(chan struct{})
	results := make(chan roleadmin.State, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		actorID := adminA
		if i%2 == 1 {
			actorID = adminB
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			state, err := store.GrantModerator(ctx, actorID, targetID)
			if err != nil {
				errs <- err
				return
			}
			results <- state
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent grant error=%v", err)
	}
	for state := range results {
		if !state.Moderator || state.ModeratorGrantedByUserID == nil {
			t.Fatalf("concurrent grant state=%#v", state)
		}
		grantor := *state.ModeratorGrantedByUserID
		if grantor != adminA && grantor != adminB {
			t.Fatalf("unexpected grantor=%d state=%#v", grantor, state)
		}
	}

	var count int
	var persistedGrantor int64
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*), min(granted_by_user_id)
		FROM user_roles
		WHERE user_id = $1
		  AND role = $2
	`, targetID, role.Moderator).Scan(&count, &persistedGrantor); err != nil {
		t.Fatal(err)
	}
	if count != 1 || (persistedGrantor != adminA && persistedGrantor != adminB) {
		t.Fatalf("concurrent grant rows=%d grantor=%d", count, persistedGrantor)
	}

	start = make(chan struct{})
	revokeStates := make(chan roleadmin.State, workers)
	errs = make(chan error, workers)
	wg = sync.WaitGroup{}
	for i := 0; i < workers; i++ {
		actorID := adminA
		if i%2 == 1 {
			actorID = adminB
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			state, err := store.RevokeModerator(ctx, actorID, targetID)
			if err != nil {
				errs <- err
				return
			}
			revokeStates <- state
		}()
	}
	close(start)
	wg.Wait()
	close(revokeStates)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent revoke error=%v", err)
	}
	for state := range revokeStates {
		if state.Moderator || state.ModeratorGrantedByUserID != nil {
			t.Fatalf("concurrent revoke state=%#v", state)
		}
	}
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM user_roles
		WHERE user_id = $1
		  AND role = $2
	`, targetID, role.Moderator).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("concurrent revoke rows=%d", count)
	}
}

func TestRoleAdminMissingTargetCannotCreateRoleState(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	adminID := createRoleAdminUser(t, store, "role-missing-admin")
	grantRole(t, store, adminID, role.Admin)
	const missingUserID int64 = 999999999

	for _, operation := range []struct {
		name string
		run  func() (roleadmin.State, error)
	}{
		{"read", func() (roleadmin.State, error) {
			return store.GetUserRoleState(ctx, adminID, missingUserID)
		}},
		{"grant", func() (roleadmin.State, error) {
			return store.GrantModerator(ctx, adminID, missingUserID)
		}},
		{"revoke", func() (roleadmin.State, error) {
			return store.RevokeModerator(ctx, adminID, missingUserID)
		}},
	} {
		if _, err := operation.run(); !errors.Is(err, roleadmin.ErrUserNotFound) {
			t.Fatalf("%s error=%v", operation.name, err)
		}
	}
	var count int
	if err := store.pool.QueryRow(
		ctx,
		"SELECT count(*) FROM user_roles WHERE user_id = $1",
		missingUserID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("missing target role rows=%d", count)
	}
}

func TestRoleAdminPlansUsePrimaryKeysWithoutLargeSequentialScans(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	adminID := createRoleAdminUser(t, store, "role-plan-admin")
	targetID := createRoleAdminUser(t, store, "role-plan-target")
	grantRole(t, store, adminID, role.Admin)

	if _, err := store.pool.Exec(ctx, `
		WITH inserted AS (
			INSERT INTO users (username)
			SELECT 'role-plan-' || g::text
			FROM generate_series(1, 5000) AS g
			RETURNING id
		)
		INSERT INTO user_roles (user_id, role)
		SELECT id,
		       CASE WHEN id % 2 = 0 THEN $1::smallint ELSE $2::smallint END
		FROM inserted
	`, role.Moderator, role.Admin); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, "ANALYZE users, user_roles"); err != nil {
		t.Fatal(err)
	}

	explainRoleAdminPlan(
		t,
		store,
		"read",
		getUserRoleStateSQL,
		adminID,
		targetID,
		role.Admin,
		role.Moderator,
	)
	explainRoleAdminPlan(
		t,
		store,
		"grant",
		grantModeratorSQL,
		adminID,
		targetID,
		role.Admin,
		role.Moderator,
	)
	explainRoleAdminPlan(
		t,
		store,
		"revoke",
		revokeModeratorSQL,
		adminID,
		targetID,
		role.Admin,
		role.Moderator,
	)
}

func explainRoleAdminPlan(t *testing.T, store *Store, name, query string, args ...any) {
	t.Helper()
	rows, err := store.pool.Query(context.Background(), "EXPLAIN (ANALYZE, BUFFERS) "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	plan := collectPlan(t, rows)
	t.Logf("role admin %s plan:\n%s", name, plan)
	assertPlanContains(t, plan, "users_pkey")
	assertPlanContains(t, plan, "user_roles_pkey")
	assertPlanExcludes(
		t,
		plan,
		"Seq Scan on users",
		"Seq Scan on user_roles",
		"external merge",
		"Disk:",
	)
}

func createRoleAdminUser(t *testing.T, store *Store, username string) int64 {
	t.Helper()
	var userID int64
	if err := store.pool.QueryRow(
		context.Background(),
		"INSERT INTO users (username) VALUES ($1) RETURNING id",
		username,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}

func createRoleAdminPost(t *testing.T, store *Store, authorUserID int64) int64 {
	t.Helper()
	var postID int64
	if err := store.pool.QueryRow(
		context.Background(),
		"INSERT INTO posts (author_user_id) VALUES ($1) RETURNING id",
		authorUserID,
	).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	return postID
}

func createRoleAdminSession(t *testing.T, store *Store, userID int64) string {
	t.Helper()
	raw := sha256.Sum256([]byte(fmt.Sprintf("role-admin-session-%d", userID)))
	tokenHash := sha256.Sum256(raw[:])
	now := time.Now().UTC()
	if err := store.CreateSession(context.Background(), userID, tokenHash, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}

func serveRoleAdminRequest(
	server *httpapi.Server,
	method, path, token string,
) *httptest.ResponseRecorder {
	var req *http.Request
	if method == http.MethodGet {
		req = httptest.NewRequest(method, "http://ginbar.test"+path, nil)
	} else {
		req = httptest.NewRequest(method, "http://ginbar.test"+path, nil)
		req.Header.Set("Origin", "http://ginbar.test")
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "ginbar_session", Value: token})
	}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	return res
}

func decodeRoleAdminState(t *testing.T, res *httptest.ResponseRecorder) roleadmin.State {
	t.Helper()
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var state roleadmin.State
	if err := json.Unmarshal(res.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertRoleAdminError(
	t *testing.T,
	res *httptest.ResponseRecorder,
	status int,
	code string,
) {
	t.Helper()
	if res.Code != status || !strings.Contains(res.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("status=%d want=%d code=%q body=%s", res.Code, status, code, res.Body.String())
	}
}
