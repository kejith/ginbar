package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kejith/ginbar/backend/v2/internal/auth"
	"github.com/kejith/ginbar/backend/v2/internal/schema"
)

var fastPasswordParams = auth.PasswordParams{
	MemoryKiB:   8 * 1024,
	Iterations:  1,
	Parallelism: 1,
	SaltBytes:   16,
	KeyBytes:    32,
}

func TestRegistrationClaimsInvitationAndPersistsOnlyVerifier(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()

	token := "registration-token-0000000000000001"
	invitationID := createInvitation(t, store, token, nil, false, false)
	service := auth.New(store, auth.Config{PasswordParams: fastPasswordParams, SessionTTL: time.Hour})
	principal, err := service.Register(context.Background(), auth.RegistrationRequest{
		InvitationToken: token,
		Username:        "Alice",
		Password:        "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.UserID <= 0 || principal.Username != "Alice" {
		t.Fatalf("principal=%#v", principal)
	}

	var claimedBy int64
	var claimedAt time.Time
	if err := store.pool.QueryRow(context.Background(), `
		SELECT claimed_by_user_id, claimed_at
		FROM invitations
		WHERE id = $1
	`, invitationID).Scan(&claimedBy, &claimedAt); err != nil {
		t.Fatal(err)
	}
	if claimedBy != principal.UserID || claimedAt.IsZero() {
		t.Fatalf("invitation claim user=%d time=%v", claimedBy, claimedAt)
	}

	var credentialUserID int64
	var kind int16
	var verifier string
	if err := store.pool.QueryRow(context.Background(), `
		SELECT user_id, kind, secret_hash
		FROM user_credentials
		WHERE user_id = $1
	`, principal.UserID).Scan(&credentialUserID, &kind, &verifier); err != nil {
		t.Fatal(err)
	}
	if credentialUserID != principal.UserID || kind != auth.CredentialKindPassword {
		t.Fatalf("credential user/kind=%d/%d", credentialUserID, kind)
	}
	if verifier == "correct horse battery staple" {
		t.Fatal("raw password was persisted")
	}
	ok, err := auth.VerifyPassword("correct horse battery staple", verifier)
	if err != nil || !ok {
		t.Fatalf("persisted verifier does not verify: ok=%v err=%v", ok, err)
	}

	wantInvitationHash := sha256.Sum256([]byte(token))
	var storedInvitationHash []byte
	if err := store.pool.QueryRow(context.Background(), "SELECT token_hash FROM invitations WHERE id = $1", invitationID).Scan(&storedInvitationHash); err != nil {
		t.Fatal(err)
	}
	if string(storedInvitationHash) != string(wantInvitationHash[:]) || string(storedInvitationHash) == token {
		t.Fatalf("stored invitation hash=%x", storedInvitationHash)
	}
}

func TestRegistrationRejectsUnavailableInvitations(t *testing.T) {
	tests := []struct {
		name    string
		expires func() *time.Time
		revoked bool
		claimed bool
		token   string
	}{
		{name: "expired", expires: func() *time.Time { v := time.Now().Add(-time.Minute); return &v }},
		{name: "revoked", revoked: true},
		{name: "claimed", claimed: true},
		{name: "unknown", token: "unknown-token-00000000000000000001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, cleanup := testAuthStore(t)
			defer cleanup()
			token := tt.token
			if token == "" {
				token = "registration-token-" + fmt.Sprintf("%-16s", tt.name)
			}
			if tt.name != "unknown" {
				var expires *time.Time
				if tt.expires != nil {
					expires = tt.expires()
				}
				createInvitation(t, store, token, expires, tt.revoked, tt.claimed)
			}
			service := auth.New(store, auth.Config{PasswordParams: fastPasswordParams, SessionTTL: time.Hour})
			_, err := service.Register(context.Background(), auth.RegistrationRequest{
				InvitationToken: token,
				Username:        "Alice",
				Password:        "correct horse battery staple",
			})
			if !errors.Is(err, auth.ErrInvalidInvitation) {
				t.Fatalf("error=%v", err)
			}
			var count int
			if err := store.pool.QueryRow(context.Background(), "SELECT count(*) FROM users WHERE username = 'Alice'").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("unexpected registered users=%d", count)
			}
		})
	}
}

func TestRegistrationDuplicateUsernameDoesNotConsumeInvitation(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	service := auth.New(store, auth.Config{PasswordParams: fastPasswordParams, SessionTTL: time.Hour})

	firstToken := "duplicate-token-00000000000000000001"
	secondToken := "duplicate-token-00000000000000000002"
	createInvitation(t, store, firstToken, nil, false, false)
	secondID := createInvitation(t, store, secondToken, nil, false, false)
	if _, err := service.Register(context.Background(), auth.RegistrationRequest{
		InvitationToken: firstToken,
		Username:        "CaseName",
		Password:        "correct horse battery staple",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Register(context.Background(), auth.RegistrationRequest{
		InvitationToken: secondToken,
		Username:        "casename",
		Password:        "correct horse battery staple",
	}); !errors.Is(err, auth.ErrUsernameUnavailable) {
		t.Fatalf("error=%v", err)
	}

	var claimedBy *int64
	var claimedAt *time.Time
	if err := store.pool.QueryRow(context.Background(), `
		SELECT claimed_by_user_id, claimed_at FROM invitations WHERE id = $1
	`, secondID).Scan(&claimedBy, &claimedAt); err != nil {
		t.Fatal(err)
	}
	if claimedBy != nil || claimedAt != nil {
		t.Fatalf("duplicate username consumed invitation: user=%v time=%v", claimedBy, claimedAt)
	}
}

func TestConcurrentCaseInsensitiveDuplicateUsernamesHaveSingleWinner(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	firstToken := "concurrent-name-token-0000000000000001"
	secondToken := "concurrent-name-token-0000000000000002"
	firstInvitationID := createInvitation(t, store, firstToken, nil, false, false)
	secondInvitationID := createInvitation(t, store, secondToken, nil, false, false)
	firstHash := sha256.Sum256([]byte(firstToken))
	secondHash := sha256.Sum256([]byte(secondToken))

	type attempt struct {
		hash     [32]byte
		username string
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, candidate := range []attempt{
		{hash: firstHash, username: "RaceName"},
		{hash: secondHash, username: "racename"},
	} {
		candidate := candidate
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.RegisterUser(ctx, candidate.hash, candidate.username, "$argon2id$test", time.Now())
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, auth.ErrUsernameUnavailable):
			conflicts++
		default:
			t.Fatalf("unexpected error=%v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}

	var userCount int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE lower(username) = 'racename'").Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 1 {
		t.Fatalf("duplicate username count=%d", userCount)
	}
	var claimedCount int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM invitations
		WHERE id = ANY($1::bigint[])
		  AND claimed_by_user_id IS NOT NULL
	`, []int64{firstInvitationID, secondInvitationID}).Scan(&claimedCount); err != nil {
		t.Fatal(err)
	}
	if claimedCount != 1 {
		t.Fatalf("claimed invitations=%d", claimedCount)
	}
}

func TestRegistrationCredentialFailureRollsBackUserAndInvitation(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	token := "rollback-token-0000000000000000000001"
	invitationID := createInvitation(t, store, token, nil, false, false)
	if _, err := store.pool.Exec(ctx, `
		CREATE FUNCTION reject_test_credential() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'test credential failure';
		END;
		$$
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		CREATE TRIGGER reject_test_credential
		BEFORE INSERT ON user_credentials
		FOR EACH ROW EXECUTE FUNCTION reject_test_credential()
	`); err != nil {
		t.Fatal(err)
	}

	tokenHash := sha256.Sum256([]byte(token))
	_, err := store.RegisterUser(ctx, tokenHash, "RollbackUser", "$argon2id$test", time.Now())
	if err == nil {
		t.Fatal("registration unexpectedly succeeded")
	}
	var userCount int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE username = 'RollbackUser'").Scan(&userCount); err != nil {
		t.Fatal(err)
	}
	if userCount != 0 {
		t.Fatalf("partial user persisted: %d", userCount)
	}
	var claimedBy *int64
	if err := store.pool.QueryRow(ctx, "SELECT claimed_by_user_id FROM invitations WHERE id = $1", invitationID).Scan(&claimedBy); err != nil {
		t.Fatal(err)
	}
	if claimedBy != nil {
		t.Fatalf("failed registration consumed invitation: %v", claimedBy)
	}
}

func TestConcurrentInvitationClaimsHaveSingleWinner(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	token := "concurrent-token-0000000000000000001"
	createInvitation(t, store, token, nil, false, false)
	tokenHash := sha256.Sum256([]byte(token))

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, username := range []string{"ConcurrentA", "ConcurrentB"} {
		username := username
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.RegisterUser(ctx, tokenHash, username, "$argon2id$test", time.Now())
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	invalidInvitations := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, auth.ErrInvalidInvitation):
			invalidInvitations++
		default:
			t.Fatalf("unexpected error=%v", err)
		}
	}
	if successes != 1 || invalidInvitations != 1 {
		t.Fatalf("successes=%d invalidInvitations=%d", successes, invalidInvitations)
	}
	var count int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE username LIKE 'Concurrent%'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("registered users=%d", count)
	}
}

func TestLoginSessionResolveExpiryRevocationAndIndependentSessions(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	token := "session-registration-token-000000000001"
	createInvitation(t, store, token, nil, false, false)
	service := auth.New(store, auth.Config{PasswordParams: fastPasswordParams, SessionTTL: time.Hour})
	principal, err := service.Register(ctx, auth.RegistrationRequest{
		InvitationToken: token,
		Username:        "SessionUser",
		Password:        "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := service.Login(ctx, "sessionuser", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(ctx, "SessionUser", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token {
		t.Fatal("independent logins returned the same session token")
	}

	decoded, err := base64.RawURLEncoding.Strict().DecodeString(first.Token)
	if err != nil {
		t.Fatal(err)
	}
	firstHash := sha256.Sum256(decoded)
	var persistedHash []byte
	var storedUserID int64
	if err := store.pool.QueryRow(ctx, `
		SELECT token_hash, user_id
		FROM user_sessions
		WHERE token_hash = $1
	`, firstHash[:]).Scan(&persistedHash, &storedUserID); err != nil {
		t.Fatal(err)
	}
	if storedUserID != principal.UserID || string(persistedHash) != string(firstHash[:]) || string(persistedHash) == first.Token {
		t.Fatalf("stored session user/hash=%d/%x", storedUserID, persistedHash)
	}

	got, err := service.Authenticate(ctx, first.Token)
	if err != nil || got.UserID != principal.UserID {
		t.Fatalf("authenticate first=%#v err=%v", got, err)
	}
	if err := service.Logout(ctx, first.Token); err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, first.Token); err != nil {
		t.Fatalf("idempotent logout=%v", err)
	}
	if _, err := service.Authenticate(ctx, first.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("revoked session error=%v", err)
	}
	if got, err := service.Authenticate(ctx, second.Token); err != nil || got.UserID != principal.UserID {
		t.Fatalf("independent session got=%#v err=%v", got, err)
	}

	expiredRaw := make([]byte, auth.SessionTokenBytes)
	for i := range expiredRaw {
		expiredRaw[i] = 0x42
	}
	expiredHash := sha256.Sum256(expiredRaw)
	now := time.Now()
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO user_sessions (token_hash, user_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4)
	`, expiredHash[:], principal.UserID, now.Add(-2*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	expiredToken := base64.RawURLEncoding.EncodeToString(expiredRaw)
	if _, err := service.Authenticate(ctx, expiredToken); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("expired session error=%v", err)
	}

	if _, err := store.pool.Exec(ctx, "UPDATE users SET status = 1 WHERE id = $1", principal.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, second.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("disabled user session error=%v", err)
	}
	if _, err := service.Login(ctx, "SessionUser", "correct horse battery staple"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("disabled user login error=%v", err)
	}
}

func TestSessionLookupDoesNotMutateSession(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	var userID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ('ReadOnlySession') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, auth.SessionTokenBytes)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	hash := sha256.Sum256(raw)
	createdAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
	expiresAt := createdAt.Add(time.Hour)
	if err := store.CreateSession(ctx, userID, hash, createdAt, expiresAt); err != nil {
		t.Fatal(err)
	}

	before := sessionSnapshot(t, store, hash)
	if _, err := store.ResolveSession(ctx, hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	after := sessionSnapshot(t, store, hash)
	if before != after {
		t.Fatalf("session lookup mutated row: before=%#v after=%#v", before, after)
	}

	unknown := sha256.Sum256([]byte("unknown session"))
	if _, err := store.ResolveSession(ctx, unknown, time.Now()); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("unknown session error=%v", err)
	}
}

type storedSession struct {
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}

func sessionSnapshot(t *testing.T, store *Store, hash [32]byte) storedSession {
	t.Helper()
	var snapshot storedSession
	if err := store.pool.QueryRow(context.Background(), `
		SELECT user_id, created_at, expires_at, revoked_at
		FROM user_sessions
		WHERE token_hash = $1
	`, hash[:]).Scan(&snapshot.UserID, &snapshot.CreatedAt, &snapshot.ExpiresAt, &snapshot.RevokedAt); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func createInvitation(t *testing.T, store *Store, token string, expiresAt *time.Time, revoked, claimed bool) int64 {
	t.Helper()
	ctx := context.Background()
	var creatorID int64
	creatorName := fmt.Sprintf("creator-%d", time.Now().UnixNano())
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ($1) RETURNING id", creatorName).Scan(&creatorID); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(token))
	var revokedAt any
	if revoked {
		revokedAt = time.Now()
	}
	var claimedBy any
	var claimedAt any
	if claimed {
		var claimedID int64
		claimedName := fmt.Sprintf("claimed-%d", time.Now().UnixNano())
		if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ($1) RETURNING id", claimedName).Scan(&claimedID); err != nil {
			t.Fatal(err)
		}
		claimedBy = claimedID
		claimedAt = time.Now()
	}
	var expiresValue any
	if expiresAt != nil {
		expiresValue = *expiresAt
	}
	var invitationID int64
	if err := store.pool.QueryRow(ctx, `
		INSERT INTO invitations (
			token_hash, created_by_user_id, claimed_by_user_id, expires_at, claimed_at, revoked_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, hash[:], creatorID, claimedBy, expiresValue, claimedAt, revokedAt).Scan(&invitationID); err != nil {
		t.Fatal(err)
	}
	return invitationID
}

func testAuthStore(t *testing.T) (*Store, func()) {
	t.Helper()
	url := os.Getenv("GINBAR_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("GINBAR_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	schemaName := fmt.Sprintf("ginbar_auth_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		admin.Close(context.Background())
		t.Fatalf("create schema: %v", err)
	}
	if _, err := admin.Exec(ctx, "SET search_path TO "+schemaName); err != nil {
		admin.Close(context.Background())
		t.Fatalf("set migration search path: %v", err)
	}
	names, err := schema.MigrationNames()
	if err != nil {
		admin.Close(context.Background())
		t.Fatal(err)
	}
	for _, name := range names {
		migration, err := schema.Migration(name)
		if err != nil {
			admin.Close(context.Background())
			t.Fatal(err)
		}
		if _, err := admin.Exec(ctx, string(migration), pgx.QueryExecModeSimpleProtocol); err != nil {
			admin.Close(context.Background())
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	poolConfig, err := pgxpool.ParseConfig(url)
	if err != nil {
		admin.Close(context.Background())
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schemaName
	poolConfig.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		admin.Close(context.Background())
		t.Fatal(err)
	}
	store := &Store{pool: pool}
	cleanup := func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "SET search_path TO public")
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA "+schemaName+" CASCADE")
		_ = admin.Close(cleanupCtx)
	}
	return store, cleanup
}
