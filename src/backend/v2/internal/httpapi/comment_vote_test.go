package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/commentvote"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

func (s *apiStore) SetCommentVote(_ context.Context, userID, postID, commentID int64, vote model.PostVote) (commentvote.Result, error) {
	if userID <= 0 {
		return commentvote.Result{}, errors.New("unexpected user id")
	}
	for _, post := range s.posts {
		if post.ID == postID && commentID == 100 {
			return commentvote.Result{CommentID: commentID, Score: int32(vote), Vote: vote}, nil
		}
	}
	return commentvote.Result{}, commentvote.ErrCommentNotFound
}

type commentVoteAPIStore struct {
	*apiStore
	postID    int64
	commentID int64
	score     int32
	vote      model.PostVote
	calls     int
}

func (s *commentVoteAPIStore) SetCommentVote(_ context.Context, userID, postID, commentID int64, vote model.PostVote) (commentvote.Result, error) {
	if userID <= 0 || postID != s.postID || commentID != s.commentID {
		return commentvote.Result{}, commentvote.ErrCommentNotFound
	}
	s.score += int32(vote - s.vote)
	s.vote = vote
	s.calls++
	return commentvote.Result{CommentID: commentID, Score: s.score, Vote: vote}, nil
}

func TestCommentVoteRequiresAuthenticationAndRejectsCrossOrigin(t *testing.T) {
	base := &apiStore{posts: []model.PostSummary{{ID: 42}}}
	store := &commentVoteAPIStore{apiStore: base, postID: 42, commentID: 100, score: 7}
	server := newAuthTestServer(store)

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, sameOriginRequest(http.MethodPut, "/api/v2/posts/42/comments/100/vote", `{"vote":1}`))
	if res.Code != http.StatusUnauthorized || store.calls != 0 {
		t.Fatalf("signed-out status=%d calls=%d body=%s", res.Code, store.calls, res.Body.String())
	}

	req := authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/comments/100/vote", `{"vote":1}`)
	req.Header.Set("Origin", "https://evil.test")
	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || store.calls != 0 {
		t.Fatalf("cross-origin status=%d calls=%d body=%s", res.Code, store.calls, res.Body.String())
	}
}

func TestCommentVoteExplicitTransitionsAndAuthoritativeResponse(t *testing.T) {
	base := &apiStore{posts: []model.PostSummary{{ID: 42}}}
	store := &commentVoteAPIStore{apiStore: base, postID: 42, commentID: 100, score: 10}
	server := newAuthTestServer(store)

	for _, tt := range []struct {
		vote      model.PostVote
		wantScore int32
	}{
		{model.VoteUp, 11},
		{model.VoteUp, 11},
		{model.VoteNeutral, 10},
		{model.VoteDown, 9},
		{model.VoteNeutral, 10},
		{model.VoteUp, 11},
		{model.VoteDown, 9},
		{model.VoteUp, 11},
	} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/comments/100/vote", `{"vote":`+string(rune('0'+tt.vote))+`}`))
		if tt.vote == model.VoteDown {
			res = httptest.NewRecorder()
			server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/comments/100/vote", `{"vote":-1}`))
		}
		if res.Code != http.StatusOK {
			t.Fatalf("vote=%d status=%d body=%s", tt.vote, res.Code, res.Body.String())
		}
		var result commentvote.Result
		if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.CommentID != 100 || result.Score != tt.wantScore || result.Vote != tt.vote {
			t.Fatalf("vote=%d result=%#v", tt.vote, result)
		}
	}
}

func TestCommentVoteRejectsMalformedIDsBodyAndMissingComment(t *testing.T) {
	base := &apiStore{posts: []model.PostSummary{{ID: 42}}}
	server := newAuthTestServer(base)
	for _, path := range []string{
		"/api/v2/posts/nope/comments/100/vote",
		"/api/v2/posts/0/comments/100/vote",
		"/api/v2/posts/42/comments/nope/vote",
		"/api/v2/posts/42/comments/0/vote",
	} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPut, path, `{"vote":1}`))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}

	for _, body := range []string{`{"vote":2}`, `{}`, `{"vote":null}`, `{"vote":1,"extra":true}`, `{"vote":`} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/comments/100/vote", body))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("body=%q status=%d response=%s", body, res.Code, res.Body.String())
		}
	}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/comments/999/vote", `{"vote":1}`))
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"code":"comment_not_found"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
