package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type blockingServiceStore struct {
	*serviceStore
	lookupStarted chan struct{}
	releaseLookup chan struct{}
	lookupCalls   atomic.Int32
}

func (s *blockingServiceStore) LookupPasswordCredential(ctx context.Context, username string) (PasswordCredential, error) {
	call := s.lookupCalls.Add(1)
	if call == 1 {
		close(s.lookupStarted)
		select {
		case <-s.releaseLookup:
		case <-ctx.Done():
			return PasswordCredential{}, ctx.Err()
		}
	}
	return s.serviceStore.LookupPasswordCredential(ctx, username)
}

func TestServiceKDFAdmissionIsSharedAcrossLoginAndRegistration(t *testing.T) {
	password := "correct horse battery staple"
	verifier, err := HashPassword(password, testPasswordParams())
	if err != nil {
		t.Fatal(err)
	}
	invitation := "0123456789abcdef0123456789abcdef"
	base := &serviceStore{
		invitationHash: sha256.Sum256([]byte(invitation)),
		credential: PasswordCredential{
			UserID:   42,
			Username: "Alice",
			Status:   UserStatusActive,
			Verifier: verifier,
		},
	}
	store := &blockingServiceStore{
		serviceStore:  base,
		lookupStarted: make(chan struct{}),
		releaseLookup: make(chan struct{}),
	}
	cfg := Config{
		PasswordParams: testPasswordParams(),
		SessionTTL:     time.Hour,
		KDFAdmission:   KDFAdmissionConfig{MaxConcurrent: 1, MaxQueued: 1},
	}
	service := New(store, cfg)

	loginDone := make(chan error, 1)
	go func() {
		_, err := service.Login(context.Background(), "Alice", password)
		loginDone <- err
	}()
	<-store.lookupStarted

	registerDone := make(chan error, 1)
	go func() {
		_, err := service.Register(context.Background(), RegistrationRequest{
			InvitationToken: invitation,
			Username:        "Bob",
			Password:        password,
		})
		registerDone <- err
	}()
	waitForAdmissionCount(t, service.kdfAdmission.waiting, 1)

	if _, err := service.Login(context.Background(), "Missing", password); !errors.Is(err, ErrKDFSaturated) {
		t.Fatalf("saturated missing-user login error = %v", err)
	}
	if got := store.lookupCalls.Load(); got != 1 {
		t.Fatalf("saturated request reached credential lookup: calls=%d", got)
	}

	close(store.releaseLookup)
	if err := <-loginDone; err != nil {
		t.Fatalf("first login: %v", err)
	}
	if err := <-registerDone; err != nil {
		t.Fatalf("queued registration: %v", err)
	}
	if len(service.kdfAdmission.running) != 0 || len(service.kdfAdmission.waiting) != 0 {
		t.Fatalf("capacity retained: running=%d waiting=%d", len(service.kdfAdmission.running), len(service.kdfAdmission.waiting))
	}
}

func TestServiceKDFAdmissionReleasesAfterCredentialError(t *testing.T) {
	password := "correct horse battery staple"
	store := &serviceStore{credential: PasswordCredential{
		UserID:   42,
		Username: "Alice",
		Status:   UserStatusActive,
		Verifier: "malformed",
	}}
	service := New(store, Config{
		PasswordParams: testPasswordParams(),
		SessionTTL:     time.Hour,
		KDFAdmission:   KDFAdmissionConfig{MaxConcurrent: 1, MaxQueued: 0},
	})

	if _, err := service.Login(context.Background(), "Alice", password); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("malformed verifier login error = %v", err)
	}
	if len(service.kdfAdmission.running) != 0 {
		t.Fatalf("running capacity retained after error: %d", len(service.kdfAdmission.running))
	}

	verifier, err := HashPassword(password, testPasswordParams())
	if err != nil {
		t.Fatal(err)
	}
	store.credential.Verifier = verifier
	if _, err := service.Login(context.Background(), "Alice", password); err != nil {
		t.Fatalf("login after error: %v", err)
	}
}

type cancelingLookupStore struct {
	*serviceStore
	cancel context.CancelFunc
}

func (s *cancelingLookupStore) LookupPasswordCredential(ctx context.Context, username string) (PasswordCredential, error) {
	s.cancel()
	return s.serviceStore.LookupPasswordCredential(ctx, username)
}

type cancelingInvitationStore struct {
	*serviceStore
	cancel context.CancelFunc
}

func (s *cancelingInvitationStore) CheckInvitation(_ context.Context, tokenHash [32]byte, _ time.Time) error {
	s.cancel()
	if tokenHash != s.invitationHash {
		return ErrInvalidInvitation
	}
	return nil
}

func TestServiceKDFAdmissionStopsBeforeKDFWhenContextCancelsAfterLookup(t *testing.T) {
	password := "correct horse battery staple"
	ctx, cancel := context.WithCancel(context.Background())
	store := &cancelingLookupStore{
		serviceStore: &serviceStore{credential: PasswordCredential{
			UserID:   42,
			Username: "Alice",
			Status:   UserStatusActive,
			Verifier: "malformed",
		}},
		cancel: cancel,
	}
	service := New(store, Config{
		PasswordParams: testPasswordParams(),
		SessionTTL:     time.Hour,
		KDFAdmission:   KDFAdmissionConfig{MaxConcurrent: 1, MaxQueued: 0},
	})

	if _, err := service.Login(ctx, "Alice", password); !errors.Is(err, context.Canceled) {
		t.Fatalf("login error = %v", err)
	}
	if len(service.kdfAdmission.running) != 0 {
		t.Fatalf("running capacity retained after cancellation: %d", len(service.kdfAdmission.running))
	}
}

func TestServiceKDFAdmissionStopsBeforeHashWhenContextCancelsAfterInvitationCheck(t *testing.T) {
	password := "correct horse battery staple"
	invitation := "0123456789abcdef0123456789abcdef"
	ctx, cancel := context.WithCancel(context.Background())
	base := &serviceStore{invitationHash: sha256.Sum256([]byte(invitation))}
	store := &cancelingInvitationStore{serviceStore: base, cancel: cancel}
	service := New(store, Config{
		PasswordParams: testPasswordParams(),
		SessionTTL:     time.Hour,
		KDFAdmission:   KDFAdmissionConfig{MaxConcurrent: 1, MaxQueued: 0},
	})

	if _, err := service.Register(ctx, RegistrationRequest{
		InvitationToken: invitation,
		Username:        "Alice",
		Password:        password,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("registration error = %v", err)
	}
	if base.registerCalls != 0 {
		t.Fatalf("registration reached persistence after cancellation: calls=%d", base.registerCalls)
	}
	if len(service.kdfAdmission.running) != 0 {
		t.Fatalf("running capacity retained after cancellation: %d", len(service.kdfAdmission.running))
	}
}
