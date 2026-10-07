package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/adminbootstrap"
	"github.com/kejith/ginbar/backend/v2/internal/httpapi"
	"github.com/kejith/ginbar/backend/v2/internal/mediajobadmin"
	"github.com/kejith/ginbar/backend/v2/internal/role"
	"github.com/kejith/ginbar/backend/v2/internal/roleadmin"
)

func TestBootstrapFirstAdminSingleUseAndNumericProvenance(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := store.BootstrapFirstAdmin(ctx, 999999999); !errors.Is(err, adminbootstrap.ErrUserNotFound) {
		t.Fatalf("missing bootstrap target error=%v", err)
	}

	userID := createRoleAdminUser(t, store, "bootstrap-admin")
	state, err := store.BootstrapFirstAdmin(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if state.UserID != userID || !state.Admin || state.AdminGrantedByUserID == nil || *state.AdminGrantedByUserID != userID {
		t.Fatalf("bootstrap state=%#v", state)
	}

	var roleCount int
	var grantorID int64
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*), min(granted_by_user_id)
		FROM user_roles
		WHERE user_id = $1
		  AND role = $2
	`, userID, role.Admin).Scan(&roleCount, &grantorID); err != nil {
		t.Fatal(err)
	}
	if roleCount != 1 || grantorID != userID {
		t.Fatalf("bootstrap rows=%d grantor=%d", roleCount, grantorID)
	}

	secondID := createRoleAdminUser(t, store, "bootstrap-second")
	if _, err := store.BootstrapFirstAdmin(ctx, secondID); !errors.Is(err, adminbootstrap.ErrAlreadyInitialized) {
		t.Fatalf("repeated bootstrap error=%v", err)
	}
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM user_roles
		WHERE role = $1
	`, role.Admin).Scan(&roleCount); err != nil {
		t.Fatal(err)
	}
	if roleCount != 1 {
		t.Fatalf("admin rows after repeated bootstrap=%d", roleCount)
	}
}

func TestBootstrapFirstAdminConcurrentAttemptsCreateExactlyOneAdmin(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	userA := createRoleAdminUser(t, store, "bootstrap-race-a")
	userB := createRoleAdminUser(t, store, "bootstrap-race-b")
	start := make(chan struct{})
	type outcome struct {
		state roleadmin.State
		err   error
	}
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for _, userID := range []int64{userA, userB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			state, err := store.BootstrapFirstAdmin(ctx, userID)
			outcomes <- outcome{state: state, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	var successCount, initializedCount int
	for result := range outcomes {
		switch {
		case result.err == nil:
			successCount++
			if !result.state.Admin || result.state.AdminGrantedByUserID == nil || *result.state.AdminGrantedByUserID != result.state.UserID {
				t.Fatalf("successful bootstrap state=%#v", result.state)
			}
		case errors.Is(result.err, adminbootstrap.ErrAlreadyInitialized):
			initializedCount++
		default:
			t.Fatalf("concurrent bootstrap error=%v", result.err)
		}
	}
	if successCount != 1 || initializedCount != 1 {
		t.Fatalf("bootstrap outcomes success=%d initialized=%d", successCount, initializedCount)
	}

	var adminCount int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM user_roles WHERE role = $1", role.Admin).Scan(&adminCount); err != nil {
		t.Fatal(err)
	}
	if adminCount != 1 {
		t.Fatalf("concurrent bootstrap admin rows=%d", adminCount)
	}
}

func TestHTTPAdminRoleGrantRevokeAuthorizationAndImmediateAuthority(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	memberID := createRoleAdminUser(t, store, "admin-role-member")
	moderatorID := createRoleAdminUser(t, store, "admin-role-moderator")
	adminID := createRoleAdminUser(t, store, "admin-role-admin")
	targetID := createRoleAdminUser(t, store, "admin-role-target")
	grantRole(t, store, moderatorID, role.Moderator)
	grantRole(t, store, adminID, role.Admin)

	memberToken := createRoleAdminSession(t, store, memberID)
	moderatorToken := createRoleAdminSession(t, store, moderatorID)
	adminToken := createRoleAdminSession(t, store, adminID)
	targetToken := createRoleAdminSession(t, store, targetID)

	cfg := httpapi.DefaultConfig()
	cfg.CookieSecure = false
	server := httpapi.NewWithConfig(store, cfg)
	adminPath := "/api/v2/admin/users/" + fmt.Sprint(targetID) + "/roles/admin"

	res := serveRoleAdminRequest(server, http.MethodPut, adminPath, "")
	assertRoleAdminError(t, res, http.StatusUnauthorized, "unauthenticated")
	for _, token := range []string{memberToken, moderatorToken} {
		res = serveRoleAdminRequest(server, http.MethodPut, adminPath, token)
		assertRoleAdminError(t, res, http.StatusForbidden, "forbidden")
		res = serveRoleAdminRequest(server, http.MethodDelete, adminPath, token)
		assertRoleAdminError(t, res, http.StatusForbidden, "forbidden")
	}

	for i := 0; i < 2; i++ {
		res = serveRoleAdminRequest(server, http.MethodPut, adminPath, adminToken)
		state := decodeRoleAdminState(t, res)
		if !state.Admin || state.AdminGrantedByUserID == nil || *state.AdminGrantedByUserID != adminID {
			t.Fatalf("grant %d state=%#v", i, state)
		}
	}

	res = serveRoleAdminRequest(server, http.MethodPut, adminPath, targetToken)
	state := decodeRoleAdminState(t, res)
	if !state.Admin || state.AdminGrantedByUserID == nil || *state.AdminGrantedByUserID != adminID {
		t.Fatalf("repeat grant by newly authoritative admin state=%#v", state)
	}

	var roleCount int
	var grantorID int64
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*), min(granted_by_user_id)
		FROM user_roles
		WHERE user_id = $1
		  AND role = $2
	`, targetID, role.Admin).Scan(&roleCount, &grantorID); err != nil {
		t.Fatal(err)
	}
	if roleCount != 1 || grantorID != adminID {
		t.Fatalf("admin rows=%d grantor=%d", roleCount, grantorID)
	}

	res = serveRoleAdminRequest(server, http.MethodGet, "/api/v2/admin/users/"+fmt.Sprint(memberID)+"/roles", targetToken)
	if got := decodeRoleAdminState(t, res); got.UserID != memberID {
		t.Fatalf("new admin read state=%#v", got)
	}

	if _, err := store.ListMediaJobs(ctx, targetID, 0, 1); err != nil {
		t.Fatalf("new admin media-job authorization error=%v", err)
	}

	res = serveRoleAdminRequest(server, http.MethodDelete, "/api/v2/admin/users/"+fmt.Sprint(adminID)+"/roles/admin", adminToken)
	assertRoleAdminError(t, res, http.StatusConflict, "self_admin_revoke_forbidden")

	for i := 0; i < 2; i++ {
		res = serveRoleAdminRequest(server, http.MethodDelete, adminPath, adminToken)
		state = decodeRoleAdminState(t, res)
		if state.Admin || state.AdminGrantedByUserID != nil {
			t.Fatalf("revoke %d state=%#v", i, state)
		}
	}
	res = serveRoleAdminRequest(server, http.MethodGet, "/api/v2/admin/users/"+fmt.Sprint(memberID)+"/roles", targetToken)
	assertRoleAdminError(t, res, http.StatusForbidden, "forbidden")
	if _, err := store.ListMediaJobs(ctx, targetID, 0, 1); !errors.Is(err, mediajobadmin.ErrForbidden) {
		t.Fatalf("revoked admin media-job authorization error=%v", err)
	}

	// adminID is now the sole admin; the explicit no-self-revocation policy is
	// also the single-admin lockout guard.
	res = serveRoleAdminRequest(server, http.MethodDelete, "/api/v2/admin/users/"+fmt.Sprint(adminID)+"/roles/admin", adminToken)
	assertRoleAdminError(t, res, http.StatusConflict, "self_admin_revoke_forbidden")

	missingPath := "/api/v2/admin/users/999999999/roles/admin"
	res = serveRoleAdminRequest(server, http.MethodPut, missingPath, adminToken)
	assertRoleAdminError(t, res, http.StatusNotFound, "user_not_found")
	res = serveRoleAdminRequest(server, http.MethodDelete, missingPath, adminToken)
	assertRoleAdminError(t, res, http.StatusNotFound, "user_not_found")
	res = serveRoleAdminRequest(server, http.MethodPut, "/api/v2/admin/users/not-a-number/roles/admin", adminToken)
	assertRoleAdminError(t, res, http.StatusBadRequest, "invalid_user_id")
}

func TestConcurrentCrossAdminRevocationCannotRemoveLastAdmin(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	adminA := createRoleAdminUser(t, store, "last-admin-a")
	adminB := createRoleAdminUser(t, store, "last-admin-b")
	grantRole(t, store, adminA, role.Admin)
	grantRole(t, store, adminB, role.Admin)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, pair := range [][2]int64{{adminA, adminB}, {adminB, adminA}} {
		actorID, targetID := pair[0], pair[1]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.RevokeAdmin(ctx, actorID, targetID)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var successCount, forbiddenCount int
	for err := range errs {
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, roleadmin.ErrForbidden):
			forbiddenCount++
		default:
			t.Fatalf("cross-revoke error=%v", err)
		}
	}
	if successCount != 1 || forbiddenCount != 1 {
		t.Fatalf("cross-revoke outcomes success=%d forbidden=%d", successCount, forbiddenCount)
	}

	var adminCount int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM user_roles WHERE role = $1", role.Admin).Scan(&adminCount); err != nil {
		t.Fatal(err)
	}
	if adminCount != 1 {
		t.Fatalf("admins after concurrent cross-revoke=%d", adminCount)
	}
}

func TestConcurrentAdminGrantsStayUniqueAndPreserveOneGrantor(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	adminA := createRoleAdminUser(t, store, "admin-grant-a")
	adminB := createRoleAdminUser(t, store, "admin-grant-b")
	targetID := createRoleAdminUser(t, store, "admin-grant-target")
	grantRole(t, store, adminA, role.Admin)
	grantRole(t, store, adminB, role.Admin)

	const workers = 12
	start := make(chan struct{})
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
			state, err := store.GrantAdmin(ctx, actorID, targetID)
			if err == nil && (!state.Admin || state.AdminGrantedByUserID == nil) {
				err = fmt.Errorf("incomplete state: %#v", state)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent admin grant error=%v", err)
		}
	}

	var count int
	var grantorID int64
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*), min(granted_by_user_id)
		FROM user_roles
		WHERE user_id = $1
		  AND role = $2
	`, targetID, role.Admin).Scan(&count, &grantorID); err != nil {
		t.Fatal(err)
	}
	if count != 1 || (grantorID != adminA && grantorID != adminB) {
		t.Fatalf("concurrent admin grant rows=%d grantor=%d", count, grantorID)
	}
}

func TestBootstrapFirstAdminPlanIsBoundedForFreshInstallation(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	userID := createRoleAdminUser(t, store, "bootstrap-plan-admin")
	if _, err := store.pool.Exec(ctx, "ANALYZE users, user_roles"); err != nil {
		t.Fatal(err)
	}

	rows, err := store.pool.Query(
		ctx,
		"EXPLAIN (ANALYZE, BUFFERS) "+bootstrapFirstAdminSQL,
		userID,
		role.Admin,
		role.Moderator,
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := collectPlan(t, rows)
	t.Logf("first admin bootstrap plan:\n%s", plan)
	assertPlanContains(t, plan, "users_pkey")
	assertPlanExcludes(t, plan, "external merge", "Disk:")

	var grantorID int64
	if err := store.pool.QueryRow(ctx, `
		SELECT granted_by_user_id
		FROM user_roles
		WHERE user_id = $1
		  AND role = $2
	`, userID, role.Admin).Scan(&grantorID); err != nil {
		t.Fatal(err)
	}
	if grantorID != userID {
		t.Fatalf("bootstrap plan provenance=%d want=%d", grantorID, userID)
	}
}

func TestAdminRoleMutationPlansUsePrimaryKeysWithoutLargeSequentialScans(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	adminID := createRoleAdminUser(t, store, "admin-role-plan-admin")
	targetID := createRoleAdminUser(t, store, "admin-role-plan-target")
	grantRole(t, store, adminID, role.Admin)

	if _, err := store.pool.Exec(ctx, `
		WITH inserted AS (
			INSERT INTO users (username)
			SELECT 'admin-role-plan-' || g::text
			FROM generate_series(1, 5000) AS g
			RETURNING id
		)
		INSERT INTO user_roles (user_id, role)
		SELECT id, $1::smallint
		FROM inserted
	`, role.Moderator); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, "ANALYZE users, user_roles"); err != nil {
		t.Fatal(err)
	}

	explainRoleAdminPlan(t, store, "grant-admin", grantAdminSQL, adminID, targetID, role.Admin, role.Moderator)
	explainRoleAdminPlan(t, store, "revoke-admin", revokeAdminSQL, adminID, targetID, role.Admin, role.Moderator)
}
