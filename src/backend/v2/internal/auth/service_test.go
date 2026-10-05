package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

type serviceStore struct {
	invitationHash     [32]byte
	credential         PasswordCredential
	createdHash        [32]byte
	createdUserID      int64
	resolveHash        [32]byte
	principal          Principal
	revokedHash        [32]byte
	registeredVerifier string
	registerCalls      int
}

func (s *serviceStore) CheckInvitation(_ context.Context, tokenHash [32]byte, _ time.Time) error {
	if tokenHash != s.invitationHash {
		return ErrInvalidInvitation
	}
	return nil
}

func (s *serviceStore) RegisterUser(_ context.Context, tokenHash [32]byte, username, verifier string, _ time.Time) (Principal, error) {
	if tokenHash != s.invitationHash || username == "" || verifier == "" {
		return Principal{}, errors.New("bad registration arguments")
	}
	s.registeredVerifier = verifier
	s.registerCalls++
	return Principal{UserID: 42, Username: username}, nil
}

func (s *serviceStore) LookupPasswordCredential(_ context.Context, username string) (PasswordCredential, error) {
	if username != s.credential.Username {
		return PasswordCredential{}, ErrInvalidCredentials
	}
	return s.credential, nil
}

func (s *serviceStore) CreateSession(_ context.Context, userID int64, tokenHash [32]byte, _, _ time.Time) error {
	s.createdUserID = userID
	s.createdHash = tokenHash
	return nil
}

func (s *serviceStore) ResolveSession(_ context.Context, tokenHash [32]byte, _ time.Time) (Principal, error) {
	s.resolveHash = tokenHash
	if tokenHash != s.createdHash {
		return Principal{}, ErrUnauthenticated
	}
	return s.principal, nil
}

func (s *serviceStore) RevokeSession(_ context.Context, tokenHash [32]byte, _ time.Time) error {
	s.revokedHash = tokenHash
	return nil
}

func TestRegistrationHashesPasswordBeforePersistenceBoundary(t *testing.T) {
	invitation := "0123456789abcdef0123456789abcdef"
	store := &serviceStore{invitationHash: sha256.Sum256([]byte(invitation))}
	service := New(store, Config{PasswordParams: testPasswordParams(), SessionTTL: time.Hour})

	principal, err := service.Register(context.Background(), RegistrationRequest{
		InvitationToken: invitation,
		Username:        "Alice",
		Password:        "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}
	if principal.UserID != 42 || store.registerCalls != 1 {
		t.Fatalf("principal=%#v registerCalls=%d", principal, store.registerCalls)
	}
	if store.registeredVerifier == "correct horse battery staple" || store.registeredVerifier == "" {
		t.Fatalf("registration persistence boundary received %q", store.registeredVerifier)
	}
}

func TestLoginCreatesOpaqueHashedSessionAndAuthenticates(t *testing.T) {
	verifier, err := HashPassword("correct horse battery staple", testPasswordParams())
	if err != nil {
		t.Fatal(err)
	}
	store := &serviceStore{
		credential: PasswordCredential{UserID: 42, Username: "Alice", Status: UserStatusActive, Verifier: verifier},
		principal:  Principal{UserID: 42, Username: "Alice"},
	}
	service := New(store, Config{PasswordParams: testPasswordParams(), SessionTTL: time.Hour})

	result, err := service.Login(context.Background(), "Alice", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(result.Token)
	if err != nil || len(decoded) != SessionTokenBytes {
		t.Fatalf("token length=%d err=%v", len(decoded), err)
	}
	if result.Token == base64.RawURLEncoding.EncodeToString(store.createdHash[:]) {
		t.Fatal("returned session token is the persisted hash")
	}
	if store.createdHash != sha256.Sum256(decoded) || store.createdUserID != 42 {
		t.Fatalf("created user/hash mismatch: user=%d hash=%x", store.createdUserID, store.createdHash)
	}

	principal, err := service.Authenticate(context.Background(), result.Token)
	if err != nil || principal.UserID != 42 || store.resolveHash != store.createdHash {
		t.Fatalf("principal=%#v err=%v resolveHash=%x", principal, err, store.resolveHash)
	}
	if err := service.Logout(context.Background(), result.Token); err != nil {
		t.Fatal(err)
	}
	if store.revokedHash != store.createdHash {
		t.Fatalf("revoked hash=%x created=%x", store.revokedHash, store.createdHash)
	}
}

func TestLoginRejectsIncorrectMalformedAndInactiveCredentials(t *testing.T) {
	verifier, err := HashPassword("correct horse battery staple", testPasswordParams())
	if err != nil {
		t.Fatal(err)
	}
	cases := []PasswordCredential{
		{UserID: 1, Username: "Alice", Status: UserStatusActive, Verifier: verifier},
		{UserID: 1, Username: "Alice", Status: UserStatusActive, Verifier: "malformed"},
		{UserID: 1, Username: "Alice", Status: 1, Verifier: verifier},
	}
	passwords := []string{"wrong password is long enough", "correct horse battery staple", "correct horse battery staple"}
	for i, credential := range cases {
		store := &serviceStore{credential: credential}
		service := New(store, Config{PasswordParams: testPasswordParams(), SessionTTL: time.Hour})
		if _, err := service.Login(context.Background(), "Alice", passwords[i]); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("case %d error=%v", i, err)
		}
	}
}

func TestMalformedSessionTokenIsUnauthenticatedAndLogoutIsIdempotent(t *testing.T) {
	store := &serviceStore{}
	service := New(store, Config{PasswordParams: testPasswordParams(), SessionTTL: time.Hour})
	if _, err := service.Authenticate(context.Background(), "not-a-session"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("authenticate error=%v", err)
	}
	if err := service.Logout(context.Background(), "not-a-session"); err != nil {
		t.Fatalf("logout error=%v", err)
	}
	if store.revokedHash != ([32]byte{}) {
		t.Fatalf("malformed token reached store: %x", store.revokedHash)
	}
}
