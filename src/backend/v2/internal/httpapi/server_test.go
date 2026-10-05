package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/auth"
	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/mediastatus"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

type apiStore struct {
	posts      []model.PostSummary
	query      feed.Query
	wait       bool
	status     *mediastatus.Snapshot
	invitation [32]byte
	credential auth.PasswordCredential
	principal  auth.Principal
	session    [32]byte
	revoked    [32]byte
	verifier   string
}

func (s *apiStore) ListFeed(ctx context.Context, q feed.Query) ([]model.PostSummary, error) {
	s.query = q
	if s.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.posts, nil
}

func (s *apiStore) AroundPost(_ context.Context, q feed.AroundQuery) ([]model.PostSummary, error) {
	for _, post := range s.posts {
		if post.ID == q.PostID {
			return s.posts, nil
		}
	}
	return nil, nil
}

func (s *apiStore) LoadMediaStatus(ctx context.Context, postID int64) (mediastatus.Snapshot, error) {
	if s.wait {
		<-ctx.Done()
		return mediastatus.Snapshot{}, ctx.Err()
	}
	if s.status == nil || s.status.PostID != postID {
		return mediastatus.Snapshot{}, mediastatus.ErrPostNotFound
	}
	return *s.status, nil
}

func (s *apiStore) CheckInvitation(_ context.Context, tokenHash [32]byte, _ time.Time) error {
	if tokenHash != s.invitation {
		return auth.ErrInvalidInvitation
	}
	return nil
}

func (s *apiStore) RegisterUser(_ context.Context, tokenHash [32]byte, username, verifier string, _ time.Time) (auth.Principal, error) {
	if tokenHash != s.invitation {
		return auth.Principal{}, auth.ErrInvalidInvitation
	}
	s.verifier = verifier
	return auth.Principal{UserID: 42, Username: username}, nil
}

func (s *apiStore) LookupPasswordCredential(_ context.Context, username string) (auth.PasswordCredential, error) {
	if username != s.credential.Username {
		return auth.PasswordCredential{}, auth.ErrInvalidCredentials
	}
	return s.credential, nil
}

func (s *apiStore) CreateSession(_ context.Context, userID int64, tokenHash [32]byte, _, _ time.Time) error {
	if userID != s.credential.UserID {
		return errors.New("unexpected session user")
	}
	s.session = tokenHash
	return nil
}

func (s *apiStore) ResolveSession(_ context.Context, tokenHash [32]byte, _ time.Time) (auth.Principal, error) {
	if tokenHash != s.session || tokenHash == ([32]byte{}) {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	return s.principal, nil
}

func (s *apiStore) RevokeSession(_ context.Context, tokenHash [32]byte, _ time.Time) error {
	s.revoked = tokenHash
	return nil
}

func TestFeedContract(t *testing.T) {
	store := &apiStore{posts: []model.PostSummary{{ID: 42}, {ID: 41}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/feed?before=50&limit=1&q=cat+-anime+score:%3E%3D100", nil)
	res := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if store.query.Before != 50 || store.query.Limit != 1 || len(store.query.Search.IncludeTags) != 1 {
		t.Fatalf("unexpected parsed query: %#v", store.query)
	}
	var body feed.Page
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Posts) != 1 || body.NextBefore != 42 {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestFeedAcceptsHyphenatedTagSmokeQuery(t *testing.T) {
	store := &apiStore{}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/feed?before=50000&limit=60&q=tag-42%20score:%3E%3D100", nil)
	res := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if got := store.query.Search.IncludeTags; len(got) != 1 || got[0] != "tag-42" {
		t.Fatalf("unexpected include tags: %#v", got)
	}
	if store.query.Search.Score == nil || store.query.Search.Score.Value != 100 {
		t.Fatalf("unexpected score predicate: %#v", store.query.Search.Score)
	}
}

func TestFeedRejectsMalformedSearch(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/feed?q=score:100", nil)
	res := httptest.NewRecorder()
	New(&apiStore{}).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestAroundReturns404WhenSelectedPostMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/posts/99/around", nil)
	res := httptest.NewRecorder()
	New(&apiStore{posts: []model.PostSummary{{ID: 98}}}).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestMediaStatusReturnsReleasedRegenerationState(t *testing.T) {
	store := &apiStore{status: &mediastatus.Snapshot{
		PostID:       42,
		ReleaseState: 1,
		MediaReady:   true,
		Job: &mediastatus.JobSnapshot{
			State:       mediastatus.JobStatePending,
			MaxAttempts: 5,
		},
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/posts/42/media-status", nil)
	res := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body mediastatus.Status
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Phase != mediastatus.PhaseWaiting || body.Operation != mediastatus.OperationRegeneration || !body.UsableMedia {
		t.Fatalf("body = %#v", body)
	}
}

func TestMediaStatusDoesNotExposeInitialIngestion(t *testing.T) {
	store := &apiStore{status: &mediastatus.Snapshot{
		PostID: 42,
		Job:    &mediastatus.JobSnapshot{State: mediastatus.JobStateFailed, MaxAttempts: 5},
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/posts/42/media-status", nil)
	res := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body errorEnvelope
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "post_not_found" {
		t.Fatalf("body = %#v", body)
	}
}

func TestRequestDeadlineCancelsStore(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/feed", nil)
	res := httptest.NewRecorder()
	newServer(&apiStore{wait: true}, 2*time.Millisecond).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestRegistrationRejectsCrossOriginAndDoesNotEchoSecrets(t *testing.T) {
	secret := "correct horse battery staple"
	invitation := "0123456789abcdef0123456789abcdef"
	store := &apiStore{invitation: sha256.Sum256([]byte(invitation))}
	server := newAuthTestServer(store)
	body := `{"invitation_token":"` + invitation + `","username":"Alice","password":"` + secret + `"}`

	req := httptest.NewRequest(http.MethodPost, "http://ginbar.test/api/v2/auth/register", strings.NewReader(body))
	req.Header.Set("Origin", "https://evil.test")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), secret) || strings.Contains(res.Body.String(), invitation) {
		t.Fatalf("secret echoed in error body: %s", res.Body.String())
	}
}

func TestRegistrationValidatesBoundedBodyAndStoresVerifier(t *testing.T) {
	secret := "correct horse battery staple"
	invitation := "0123456789abcdef0123456789abcdef"
	store := &apiStore{invitation: sha256.Sum256([]byte(invitation))}
	server := newAuthTestServer(store)
	body := `{"invitation_token":"` + invitation + `","username":"Alice","password":"` + secret + `"}`

	req := sameOriginRequest(http.MethodPost, "/api/v2/auth/register", body)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if store.verifier == secret || !strings.HasPrefix(store.verifier, "$argon2id$") {
		t.Fatalf("unexpected stored verifier: %q", store.verifier)
	}

	oversized := sameOriginRequest(http.MethodPost, "/api/v2/auth/register", strings.Repeat("x", maxAuthBodyBytes+1))
	overRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(overRes, oversized)
	if overRes.Code != http.StatusBadRequest || strings.Contains(overRes.Body.String(), strings.Repeat("x", 32)) {
		t.Fatalf("oversized status=%d body=%s", overRes.Code, overRes.Body.String())
	}
}

func TestLoginCookieCurrentUserAndLogoutContract(t *testing.T) {
	verifier, err := auth.HashPassword("correct horse battery staple", fastHTTPPasswordParams())
	if err != nil {
		t.Fatal(err)
	}
	store := &apiStore{
		credential: auth.PasswordCredential{UserID: 42, Username: "Alice", Status: auth.UserStatusActive, Verifier: verifier},
		principal:  auth.Principal{UserID: 42, Username: "Alice"},
	}
	server := newAuthTestServer(store)
	loginBody := `{"username":"Alice","password":"correct horse battery staple"}`
	loginReq := sameOriginRequest(http.MethodPost, "/api/v2/auth/login", loginBody)
	loginRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginRes, loginReq)
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginRes.Code, loginRes.Body.String())
	}
	cookies := loginRes.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.MaxAge <= 0 || cookie.Expires.IsZero() {
		t.Fatalf("cookie=%#v", cookie)
	}
	if strings.Contains(loginRes.Body.String(), cookie.Value) {
		t.Fatalf("session token exposed in response body: %s", loginRes.Body.String())
	}

	meReq := httptest.NewRequest(http.MethodGet, "http://ginbar.test/api/v2/auth/me", nil)
	meReq.AddCookie(cookie)
	meRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(meRes, meReq)
	if meRes.Code != http.StatusOK || !strings.Contains(meRes.Body.String(), `"id":42`) || strings.Contains(meRes.Body.String(), cookie.Value) {
		t.Fatalf("me status=%d body=%s", meRes.Code, meRes.Body.String())
	}

	logoutReq := sameOriginRequest(http.MethodPost, "/api/v2/auth/logout", "")
	logoutReq.AddCookie(cookie)
	logoutRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(logoutRes, logoutReq)
	if logoutRes.Code != http.StatusNoContent || store.revoked != store.session {
		t.Fatalf("logout status=%d revoked=%x session=%x", logoutRes.Code, store.revoked, store.session)
	}
	cleared := logoutRes.Result().Cookies()
	if len(cleared) != 1 || cleared[0].MaxAge >= 0 || cleared[0].Value != "" || !cleared[0].HttpOnly || !cleared[0].Secure {
		t.Fatalf("cleared cookie=%#v", cleared)
	}
}

func TestLoginFailureIsBoundedAndDoesNotEchoSecret(t *testing.T) {
	verifier, err := auth.HashPassword("correct horse battery staple", fastHTTPPasswordParams())
	if err != nil {
		t.Fatal(err)
	}
	store := &apiStore{credential: auth.PasswordCredential{
		UserID: 42, Username: "Alice", Status: auth.UserStatusActive, Verifier: verifier,
	}}
	server := newAuthTestServer(store)
	secret := "wrong password is long enough"
	req := sameOriginRequest(http.MethodPost, "/api/v2/auth/login", `{"username":"Alice","password":"`+secret+`"}`)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized || strings.Contains(res.Body.String(), secret) || strings.Contains(res.Body.String(), verifier) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}

	overReq := sameOriginRequest(http.MethodPost, "/api/v2/auth/login", strings.Repeat("x", maxAuthBodyBytes+1))
	overRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(overRes, overReq)
	if overRes.Code != http.StatusBadRequest {
		t.Fatalf("oversized login status=%d body=%s", overRes.Code, overRes.Body.String())
	}
}

func TestLogoutRejectsCrossOriginBeforeRevocation(t *testing.T) {
	store := &apiStore{}
	server := newAuthTestServer(store)
	raw := make([]byte, auth.SessionTokenBytes)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	req := httptest.NewRequest(http.MethodPost, "http://ginbar.test/api/v2/auth/logout", nil)
	req.Header.Set("Origin", "https://evil.test")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || store.revoked != ([32]byte{}) {
		t.Fatalf("status=%d revoked=%x", res.Code, store.revoked)
	}
}

func TestSameOriginHonorsReverseProxyScheme(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://ginbar.test/api/v2/auth/logout", nil)
	req.Header.Set("Origin", "https://ginbar.test")
	req.Header.Set("X-Forwarded-Proto", "https")
	if !sameOrigin(req) {
		t.Fatal("same origin behind TLS-terminating proxy was rejected")
	}
}

func TestCurrentUserUnauthorizedHasStableSemantics(t *testing.T) {
	server := newAuthTestServer(&apiStore{})
	for _, cookieValue := range []string{"", "malformed"} {
		req := httptest.NewRequest(http.MethodGet, "http://ginbar.test/api/v2/auth/me", nil)
		if cookieValue != "" {
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookieValue})
		}
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("cookie=%q status=%d body=%s", cookieValue, res.Code, res.Body.String())
		}
		var body errorEnvelope
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != "unauthenticated" {
			t.Fatalf("cookie=%q body=%#v", cookieValue, body)
		}
	}
}

func newAuthTestServer(store Store) *Server {
	cfg := DefaultConfig()
	cfg.Auth.PasswordParams = fastHTTPPasswordParams()
	return NewWithConfig(store, cfg)
}

func fastHTTPPasswordParams() auth.PasswordParams {
	return auth.PasswordParams{
		MemoryKiB:   8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltBytes:   16,
		KeyBytes:    32,
	}
}

func sameOriginRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, "http://ginbar.test"+path, strings.NewReader(body))
	req.Header.Set("Origin", "http://ginbar.test")
	return req
}
