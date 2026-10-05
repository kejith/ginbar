package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/comment"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

// Keep the shared apiStore satisfying Server's aggregate Store interface for all existing HTTP tests.
func (s *apiStore) ListComments(_ context.Context, query comment.Query) ([]comment.Comment, error) {
	for _, post := range s.posts {
		if post.ID == query.PostID {
			return []comment.Comment{}, nil
		}
	}
	return nil, comment.ErrPostNotFound
}

func (s *apiStore) CreateComment(_ context.Context, request comment.CreateRequest) (comment.Comment, error) {
	for _, post := range s.posts {
		if post.ID == request.PostID {
			body := request.Body
			return comment.Comment{ID: 100, PostID: request.PostID, AuthorID: request.UserID, ParentCommentID: request.ParentCommentID, Body: &body, UserVote: model.VoteNeutral, CreatedAt: time.Unix(100, 0).UTC()}, nil
		}
	}
	return comment.Comment{}, comment.ErrPostNotFound
}

type commentAPIStore struct {
	*apiStore
	comments    []comment.Comment
	listErr     error
	createErr   error
	lastQuery   comment.Query
	lastCreate  comment.CreateRequest
	createValue comment.Comment
}

func (s *commentAPIStore) ListComments(_ context.Context, query comment.Query) ([]comment.Comment, error) {
	s.lastQuery = query
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.comments, nil
}

func (s *commentAPIStore) CreateComment(_ context.Context, request comment.CreateRequest) (comment.Comment, error) {
	s.lastCreate = request
	if s.createErr != nil {
		return comment.Comment{}, s.createErr
	}
	return s.createValue, nil
}

func TestCommentsReadPublicCursorAndUnavailablePost(t *testing.T) {
	body := "one"
	store := &commentAPIStore{apiStore: &apiStore{}, comments: []comment.Comment{{ID: 10, PostID: 42, AuthorID: 7, Body: &body}, {ID: 11, PostID: 42, AuthorID: 8, Body: &body}, {ID: 12, PostID: 42, AuthorID: 9, Body: &body}}}
	res := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v2/posts/42/comments?after=5&limit=2", nil))
	if res.Code != http.StatusOK || store.lastQuery.PostID != 42 || store.lastQuery.After != 5 || store.lastQuery.Limit != 2 || store.lastQuery.ViewerUserID != 0 {
		t.Fatalf("status=%d query=%#v body=%s", res.Code, store.lastQuery, res.Body.String())
	}
	var page comment.Page
	if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Comments) != 2 || page.NextAfter != 11 || !strings.Contains(res.Body.String(), `"userVote":0`) {
		t.Fatalf("page=%#v body=%s", page, res.Body.String())
	}

	store = &commentAPIStore{apiStore: &apiStore{}, listErr: comment.ErrPostNotFound}
	res = httptest.NewRecorder()
	New(store).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v2/posts/999/comments", nil))
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"code":"post_not_found"`) {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestCommentsReadResolvesAuthenticatedViewer(t *testing.T) {
	body := "one"
	base := &apiStore{}
	store := &commentAPIStore{
		apiStore: base,
		comments: []comment.Comment{{ID: 10, PostID: 42, AuthorID: 7, Body: &body, UserVote: model.VoteDown}},
	}
	res := httptest.NewRecorder()
	newAuthTestServer(store).Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodGet, "/api/v2/posts/42/comments?limit=1", ""))
	if res.Code != http.StatusOK || store.lastQuery.ViewerUserID != 42 || !strings.Contains(res.Body.String(), `"userVote":-1`) {
		t.Fatalf("status=%d query=%#v body=%s", res.Code, store.lastQuery, res.Body.String())
	}
}

func TestCommentsReadRejectsMalformedIDsAndCursor(t *testing.T) {
	for _, path := range []string{"/api/v2/posts/nope/comments", "/api/v2/posts/0/comments", "/api/v2/posts/42/comments?after=0", "/api/v2/posts/42/comments?limit=0"} {
		res := httptest.NewRecorder()
		New(&commentAPIStore{apiStore: &apiStore{}}).Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
}

func TestCommentCreateAuthOriginValidationAndAuthoritativeResponse(t *testing.T) {
	base := &apiStore{}
	body := "reply"
	parent := int64(77)
	store := &commentAPIStore{apiStore: base, createValue: comment.Comment{ID: 101, PostID: 42, AuthorID: 42, ParentCommentID: &parent, Body: &body, UserVote: model.VoteNeutral, CreatedAt: time.Unix(101, 0).UTC()}}
	server := newAuthTestServer(store)

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, sameOriginRequest(http.MethodPost, "/api/v2/posts/42/comments", `{"body":"reply"}`))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d body=%s", res.Code, res.Body.String())
	}

	req := authenticatedRequest(base, http.MethodPost, "/api/v2/posts/42/comments", `{"body":"reply"}`)
	req.Header.Set("Origin", "https://evil.test")
	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || store.lastCreate.PostID != 0 {
		t.Fatalf("cross-origin status=%d create=%#v", res.Code, store.lastCreate)
	}

	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPost, "/api/v2/posts/42/comments", `{"parentCommentId":77,"body":"reply"}`))
	if res.Code != http.StatusCreated || store.lastCreate.UserID != 42 || store.lastCreate.ParentCommentID == nil || *store.lastCreate.ParentCommentID != 77 {
		t.Fatalf("status=%d create=%#v body=%s", res.Code, store.lastCreate, res.Body.String())
	}
	var created comment.Comment
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID != 101 || created.AuthorID != 42 || created.Body == nil || *created.Body != "reply" || created.UserVote != model.VoteNeutral {
		t.Fatalf("created=%#v", created)
	}
	if !strings.Contains(res.Body.String(), `"userVote":0`) {
		t.Fatalf("created body=%s", res.Body.String())
	}
}

func TestCommentCreateBodyPayloadAndExpectedErrors(t *testing.T) {
	base := &apiStore{}
	store := &commentAPIStore{apiStore: base}
	server := newAuthTestServer(store)
	for _, payload := range []string{`{}`, `{"body":null}`, `{"body":""}`, `{"body":"ok","parentCommentId":0}`, `{"body":"ok","extra":true}`, `{"body":`} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPost, "/api/v2/posts/42/comments", payload))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("payload=%q status=%d body=%s", payload, res.Code, res.Body.String())
		}
	}

	for _, tt := range []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{comment.ErrPostNotFound, http.StatusNotFound, "post_not_found"},
		{comment.ErrParentCommentNotFound, http.StatusNotFound, "parent_comment_not_found"},
		{comment.ErrParentCommentDeleted, http.StatusConflict, "parent_comment_deleted"},
	} {
		store.createErr = tt.err
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPost, "/api/v2/posts/42/comments", `{"body":"ok"}`))
		if res.Code != tt.wantStatus || !strings.Contains(res.Body.String(), `"code":"`+tt.wantCode+`"`) {
			t.Fatalf("error=%v status=%d body=%s", tt.err, res.Code, res.Body.String())
		}
	}
}

func TestCommentCreateUnicodeBodyBoundary(t *testing.T) {
	base := &apiStore{}
	store := &commentAPIStore{apiStore: base}
	server := newAuthTestServer(store)
	for _, tt := range []struct {
		body string
		want int
	}{
		{strings.Repeat("😀", comment.MaxBodyCharacters), http.StatusCreated},
		{strings.Repeat("😀", comment.MaxBodyCharacters+1), http.StatusBadRequest},
	} {
		body := tt.body
		store.createValue = comment.Comment{ID: 1, PostID: 42, AuthorID: 42, Body: &body, UserVote: model.VoteNeutral}
		payload, err := json.Marshal(createCommentRequest{Body: &body})
		if err != nil {
			t.Fatal(err)
		}
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPost, "/api/v2/posts/42/comments", string(payload)))
		if res.Code != tt.want {
			t.Fatalf("chars=%d status=%d body=%s", len([]rune(body)), res.Code, res.Body.String())
		}
	}
}
