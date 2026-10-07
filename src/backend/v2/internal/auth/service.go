package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	CredentialKindPassword int16 = 0
	UserStatusActive       int16 = 0

	SessionTokenBytes       = 32
	MinInvitationTokenBytes = 16
	MaxInvitationTokenBytes = 256
)

var (
	ErrInvalidRegistration  = errors.New("invalid registration input")
	ErrInvalidInvitation    = errors.New("invalid invitation")
	ErrUsernameUnavailable  = errors.New("username unavailable")
	ErrInvalidCredentials   = errors.New("invalid credentials")
	ErrKDFSaturated         = errors.New("password KDF admission saturated")
	ErrUnauthenticated      = errors.New("unauthenticated")
	ErrCommitOutcomeUnknown = errors.New("transaction commit outcome unknown")
)

type Principal struct {
	UserID   int64
	Username string
}

type PasswordCredential struct {
	UserID   int64
	Username string
	Status   int16
	Verifier string
}

type Store interface {
	CheckInvitation(ctx context.Context, tokenHash [32]byte, now time.Time) error
	RegisterUser(ctx context.Context, tokenHash [32]byte, username, verifier string, now time.Time) (Principal, error)
	LookupPasswordCredential(ctx context.Context, username string) (PasswordCredential, error)
	CreateSession(ctx context.Context, userID int64, tokenHash [32]byte, createdAt, expiresAt time.Time) error
	ResolveSession(ctx context.Context, tokenHash [32]byte, now time.Time) (Principal, error)
	RevokeSession(ctx context.Context, tokenHash [32]byte, now time.Time) error
}

type KDFAdmissionConfig struct {
	MaxConcurrent int
	MaxQueued     int
}

type Config struct {
	PasswordParams PasswordParams
	SessionTTL     time.Duration
	KDFAdmission   KDFAdmissionConfig
}

func DefaultConfig() Config {
	return Config{
		PasswordParams: DefaultPasswordParams(),
		SessionTTL:     30 * 24 * time.Hour,
		KDFAdmission: KDFAdmissionConfig{
			MaxConcurrent: 1,
			MaxQueued:     4,
		},
	}
}

type Service struct {
	store          Store
	passwordParams PasswordParams
	sessionTTL     time.Duration
	kdfAdmission   *kdfAdmission
	now            func() time.Time
	random         io.Reader
}

type RegistrationRequest struct {
	InvitationToken string
	Username        string
	Password        string
}

type LoginResult struct {
	Principal Principal
	Token     string
	ExpiresAt time.Time
}

func New(store Store, cfg Config) *Service {
	if cfg.PasswordParams == (PasswordParams{}) {
		cfg.PasswordParams = DefaultPasswordParams()
	}
	defaults := DefaultConfig()
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = defaults.SessionTTL
	}
	if cfg.KDFAdmission == (KDFAdmissionConfig{}) {
		cfg.KDFAdmission = defaults.KDFAdmission
	} else {
		if cfg.KDFAdmission.MaxConcurrent <= 0 {
			cfg.KDFAdmission.MaxConcurrent = defaults.KDFAdmission.MaxConcurrent
		}
		if cfg.KDFAdmission.MaxQueued < 0 {
			cfg.KDFAdmission.MaxQueued = defaults.KDFAdmission.MaxQueued
		}
	}
	return &Service{
		store:          store,
		passwordParams: cfg.PasswordParams,
		sessionTTL:     cfg.SessionTTL,
		kdfAdmission:   newKDFAdmission(cfg.KDFAdmission.MaxConcurrent, cfg.KDFAdmission.MaxQueued),
		now:            time.Now,
		random:         rand.Reader,
	}
}

func (s *Service) Register(ctx context.Context, request RegistrationRequest) (Principal, error) {
	if !validInvitationToken(request.InvitationToken) || !validUsername(request.Username) {
		return Principal{}, ErrInvalidRegistration
	}
	if err := validatePassword(request.Password); err != nil {
		return Principal{}, ErrInvalidRegistration
	}
	if err := validateParams(s.passwordParams); err != nil {
		return Principal{}, err
	}

	tokenHash := sha256.Sum256([]byte(request.InvitationToken))
	verifier, err := func() (string, error) {
		release, err := s.kdfAdmission.acquire(ctx)
		if err != nil {
			return "", err
		}
		defer release()

		if err := s.store.CheckInvitation(ctx, tokenHash, s.now()); err != nil {
			if errors.Is(err, ErrInvalidInvitation) {
				return "", ErrInvalidInvitation
			}
			return "", err
		}
		verifier, err := HashPassword(request.Password, s.passwordParams)
		if err != nil {
			if errors.Is(err, ErrInvalidPassword) {
				return "", ErrInvalidRegistration
			}
			return "", err
		}
		return verifier, nil
	}()
	if err != nil {
		return Principal{}, err
	}

	principal, err := s.store.RegisterUser(ctx, tokenHash, request.Username, verifier, s.now())
	if err != nil {
		return Principal{}, err
	}
	return principal, nil
}

func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	if !validUsername(username) || validatePassword(password) != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	credential, err := func() (PasswordCredential, error) {
		release, err := s.kdfAdmission.acquire(ctx)
		if err != nil {
			return PasswordCredential{}, err
		}
		defer release()

		credential, err := s.store.LookupPasswordCredential(ctx, username)
		if err != nil {
			if errors.Is(err, ErrInvalidCredentials) {
				return PasswordCredential{}, ErrInvalidCredentials
			}
			return PasswordCredential{}, err
		}
		if credential.Status != UserStatusActive {
			return PasswordCredential{}, ErrInvalidCredentials
		}
		ok, err := VerifyPassword(password, credential.Verifier)
		if err != nil || !ok {
			return PasswordCredential{}, ErrInvalidCredentials
		}
		return credential, nil
	}()
	if err != nil {
		return LoginResult{}, err
	}

	tokenBytes := make([]byte, SessionTokenBytes)
	if _, err := io.ReadFull(s.random, tokenBytes); err != nil {
		return LoginResult{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256(tokenBytes)
	createdAt := s.now()
	expiresAt := createdAt.Add(s.sessionTTL)
	if err := s.store.CreateSession(ctx, credential.UserID, tokenHash, createdAt, expiresAt); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		Principal: Principal{UserID: credential.UserID, Username: credential.Username},
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	tokenHash, ok := hashSessionToken(token)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	principal, err := s.store.ResolveSession(ctx, tokenHash, s.now())
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, err
	}
	return principal, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	tokenHash, ok := hashSessionToken(token)
	if !ok {
		return nil
	}
	return s.store.RevokeSession(ctx, tokenHash, s.now())
}

func hashSessionToken(token string) ([32]byte, bool) {
	var zero [32]byte
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(decoded) != SessionTokenBytes {
		return zero, false
	}
	return sha256.Sum256(decoded), true
}

func validInvitationToken(token string) bool {
	return len(token) >= MinInvitationTokenBytes && len(token) <= MaxInvitationTokenBytes
}

func validUsername(username string) bool {
	if !utf8.ValidString(username) || strings.IndexByte(username, 0) >= 0 {
		return false
	}
	count := utf8.RuneCountInString(username)
	return count >= 1 && count <= 32
}
