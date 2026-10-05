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

	"github.com/kejith/ginbar/backend/v2/internal/auth"
	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/postvote"
)

func (s *apiStore) SetPostVote(_ context.Context, userID, postID int64, vote model.PostVote) (postvote.Result, error) {
	if userID <= 0 {
		return postvote.Result{}, errors.New("unexpected user id")
	}
	for index := range s.posts {
		if s.posts[index].ID != postID {
			continue
		}
		current := s.posts[index].UserVote
		s.posts[index].Score += int32(vote - current)
		s.posts[index].UserVote = vote
		return postvote.Result{PostID: postID, Score: s.posts[index].Score, Vote: vote}, nil
	}
	return postvote.Result{}, postvote.ErrPostNotFound
}

func TestPostVoteRequiresAuthentication(t *testing.T) {
	store := &apiStore{posts: []model.PostSummary{{ID: 42, Score: 3}}}
	req := sameOriginRequest(http.MethodPut, "/api/v2/posts/42/vote", `{"vote":1}`)
	res := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if store.posts[0].Score != 3 || store.posts[0].UserVote != model.VoteNeutral {
		t.Fatalf("unauthenticated request mutated store: %#v", store.posts[0])
	}
}

func TestPostVoteSetRemoveAndIdempotentContract(t *testing.T) {
	store := &apiStore{posts: []model.PostSummary{{ID: 42, Score: 10}}}
	server := newAuthTestServer(store)

	up := authenticatedRequest(store, http.MethodPut, "/api/v2/posts/42/vote", `{"vote":1}`)
	upRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(upRes, up)
	assertVoteResponse(t, upRes, http.StatusOK, 42, 11, model.VoteUp)

	repeat := authenticatedRequest(store, http.MethodPut, "/api/v2/posts/42/vote", `{"vote":1}`)
	repeatRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(repeatRes, repeat)
	assertVoteResponse(t, repeatRes, http.StatusOK, 42, 11, model.VoteUp)

	remove := authenticatedRequest(store, http.MethodPut, "/api/v2/posts/42/vote", `{"vote":0}`)
	removeRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(removeRes, remove)
	assertVoteResponse(t, removeRes, http.StatusOK, 42, 10, model.VoteNeutral)
}

func TestPostVoteRejectsInvalidBodyAndMissingPost(t *testing.T) {
	store := &apiStore{posts: []model.PostSummary{{ID: 42}}}
	server := newAuthTestServer(store)
	for _, body := range []string{`{"vote":2}`, `{}`, `{"vote":null}`, `{"vote":1,"extra":true}`, `{"vote":`} {
		req := authenticatedRequest(store, http.MethodPut, "/api/v2/posts/42/vote", body)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("body=%q status=%d response=%s", body, res.Code, res.Body.String())
		}
	}

	missing := authenticatedRequest(store, http.MethodPut, "/api/v2/posts/999/vote", `{"vote":-1}`)
	missingRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingRes, missing)
	if missingRes.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", missingRes.Code, missingRes.Body.String())
	}
}

func TestPostVoteRejectsCrossOrigin(t *testing.T) {
	store := &apiStore{posts: []model.PostSummary{{ID: 42}}}
	req := authenticatedRequest(store, http.MethodPut, "/api/v2/posts/42/vote", `{"vote":1}`)
	req.Header.Set("Origin", "https://evil.test")
	res := httptest.NewRecorder()
	newAuthTestServer(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestFeedResolvesOptionalViewerAndSignedOutStaysNeutral(t *testing.T) {
	store := &apiStore{posts: []model.PostSummary{{ID: 42, Score: 5, UserVote: model.VoteUp}}}
	server := newAuthTestServer(store)

	authed := authenticatedRequest(store, http.MethodGet, "/api/v2/feed?limit=1", "")
	authedRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(authedRes, authed)
	if authedRes.Code != http.StatusOK || store.query.ViewerUserID != 42 {
		t.Fatalf("status=%d viewer=%d body=%s", authedRes.Code, store.query.ViewerUserID, authedRes.Body.String())
	}
	var authedPage feed.Page
	if err := json.Unmarshal(authedRes.Body.Bytes(), &authedPage); err != nil {
		t.Fatal(err)
	}
	if len(authedPage.Posts) != 1 || authedPage.Posts[0].UserVote != model.VoteUp {
		t.Fatalf("authed page=%#v", authedPage)
	}

	store.posts[0].UserVote = model.VoteNeutral
	signedOut := httptest.NewRequest(http.MethodGet, "/api/v2/feed?limit=1", nil)
	signedOutRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(signedOutRes, signedOut)
	if signedOutRes.Code != http.StatusOK || store.query.ViewerUserID != 0 {
		t.Fatalf("status=%d viewer=%d body=%s", signedOutRes.Code, store.query.ViewerUserID, signedOutRes.Body.String())
	}
	if !strings.Contains(signedOutRes.Body.String(), `"userVote":0`) {
		t.Fatalf("signed-out response=%s", signedOutRes.Body.String())
	}
}

func TestAroundReturnsViewerVoteField(t *testing.T) {
	store := &apiStore{posts: []model.PostSummary{{ID: 42, Score: 8, UserVote: model.VoteDown}}}
	req := authenticatedRequest(store, http.MethodGet, "/api/v2/posts/42/around?radius=5", "")
	res := httptest.NewRecorder()
	newAuthTestServer(store).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"userVote":-1`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func authenticatedRequest(store *apiStore, method, path, body string) *http.Request {
	raw := make([]byte, auth.SessionTokenBytes)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	store.session = sha256.Sum256(raw)
	store.principal = auth.Principal{UserID: 42, Username: "VoteUser"}
	token := base64.RawURLEncoding.EncodeToString(raw)

	var req *http.Request
	if method == http.MethodGet {
		req = httptest.NewRequest(method, "http://ginbar.test"+path, nil)
	} else {
		req = sameOriginRequest(method, path, body)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	return req
}

func assertVoteResponse(t *testing.T, res *httptest.ResponseRecorder, status int, postID int64, score int32, vote model.PostVote) {
	t.Helper()
	if res.Code != status {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var body postvote.Result
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.PostID != postID || body.Score != score || body.Vote != vote {
		t.Fatalf("body=%#v", body)
	}
}
