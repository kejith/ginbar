package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/auth"
)

type blockingHTTPAuthStore struct {
	*apiStore
	lookupStarted chan struct{}
	releaseLookup chan struct{}
}

func (s *blockingHTTPAuthStore) LookupPasswordCredential(ctx context.Context, username string) (auth.PasswordCredential, error) {
	select {
	case <-s.lookupStarted:
	default:
		close(s.lookupStarted)
		select {
		case <-s.releaseLookup:
		case <-ctx.Done():
			return auth.PasswordCredential{}, ctx.Err()
		}
	}
	return s.apiStore.LookupPasswordCredential(ctx, username)
}

func TestAuthKDFAdmissionSaturationUsesStableHTTPErrorForLoginAndRegistration(t *testing.T) {
	password := "correct horse battery staple"
	verifier, err := auth.HashPassword(password, fastHTTPPasswordParams())
	if err != nil {
		t.Fatal(err)
	}
	store := &blockingHTTPAuthStore{
		apiStore: &apiStore{credential: auth.PasswordCredential{
			UserID:   42,
			Username: "Alice",
			Status:   auth.UserStatusActive,
			Verifier: verifier,
		}},
		lookupStarted: make(chan struct{}),
		releaseLookup: make(chan struct{}),
	}
	cfg := DefaultConfig()
	cfg.Auth.PasswordParams = fastHTTPPasswordParams()
	cfg.Auth.KDFAdmission = auth.KDFAdmissionConfig{MaxConcurrent: 1, MaxQueued: 0}
	server := NewWithConfig(store, cfg)

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := sameOriginRequest(http.MethodPost, "/api/v2/auth/login", `{"username":"Alice","password":"`+password+`"}`)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		firstDone <- res
	}()
	<-store.lookupStarted

	assertAuthUnavailable := func(t *testing.T, req *http.Request) {
		t.Helper()
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
		}
		var body errorEnvelope
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != "authentication_unavailable" {
			t.Fatalf("body=%#v", body)
		}
	}

	assertAuthUnavailable(t, sameOriginRequest(http.MethodPost, "/api/v2/auth/login", `{"username":"Missing","password":"`+password+`"}`))
	assertAuthUnavailable(t, sameOriginRequest(http.MethodPost, "/api/v2/auth/register", `{"invitation_token":"0123456789abcdef0123456789abcdef","username":"Bob","password":"`+password+`"}`))

	close(store.releaseLookup)
	first := <-firstDone
	if first.Code != http.StatusOK {
		t.Fatalf("first login status=%d body=%s", first.Code, first.Body.String())
	}
}
