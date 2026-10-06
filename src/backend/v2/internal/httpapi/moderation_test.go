package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/moderation"
)

func (s *apiStore) HidePost(_ context.Context, actorUserID, postID int64) (moderation.PostResult, error) {
	return moderation.PostResult{
		PostID:            postID,
		Deleted:           true,
		ModeratedAt:       time.Unix(100, 0).UTC(),
		ModeratedByUserID: actorUserID,
	}, nil
}

func (s *apiStore) HideComment(_ context.Context, actorUserID, postID, commentID int64) (moderation.CommentResult, error) {
	return moderation.CommentResult{
		PostID:            postID,
		CommentID:         commentID,
		Deleted:           true,
		ModeratedAt:       time.Unix(100, 0).UTC(),
		ModeratedByUserID: actorUserID,
	}, nil
}

type moderationAPIStore struct {
	*apiStore
	postResult     moderation.PostResult
	commentResult  moderation.CommentResult
	postErr        error
	commentErr     error
	postActorID    int64
	postID         int64
	commentActorID int64
	commentPostID  int64
	commentID      int64
}

func (s *moderationAPIStore) HidePost(_ context.Context, actorUserID, postID int64) (moderation.PostResult, error) {
	s.postActorID = actorUserID
	s.postID = postID
	if s.postErr != nil {
		return moderation.PostResult{}, s.postErr
	}
	result := s.postResult
	if result.PostID == 0 {
		result = moderation.PostResult{
			PostID:            postID,
			Deleted:           true,
			ModeratedAt:       time.Unix(200, 0).UTC(),
			ModeratedByUserID: actorUserID,
		}
	}
	return result, nil
}

func (s *moderationAPIStore) HideComment(_ context.Context, actorUserID, postID, commentID int64) (moderation.CommentResult, error) {
	s.commentActorID = actorUserID
	s.commentPostID = postID
	s.commentID = commentID
	if s.commentErr != nil {
		return moderation.CommentResult{}, s.commentErr
	}
	result := s.commentResult
	if result.CommentID == 0 {
		result = moderation.CommentResult{
			PostID:            postID,
			CommentID:         commentID,
			Deleted:           true,
			ModeratedAt:       time.Unix(201, 0).UTC(),
			ModeratedByUserID: actorUserID,
		}
	}
	return result, nil
}

func TestModerationRequiresAuthenticationAndSameOrigin(t *testing.T) {
	base := &apiStore{}
	store := &moderationAPIStore{apiStore: base}
	server := newAuthTestServer(store)

	signedOut := sameOriginRequest(http.MethodPut, "/api/v2/posts/42/moderation", "")
	signedOutRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(signedOutRes, signedOut)
	if signedOutRes.Code != http.StatusUnauthorized || store.postActorID != 0 {
		t.Fatalf("signed-out status=%d actor=%d", signedOutRes.Code, store.postActorID)
	}

	crossOrigin := authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/moderation", "")
	crossOrigin.Header.Set("Origin", "https://evil.test")
	crossOriginRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(crossOriginRes, crossOrigin)
	if crossOriginRes.Code != http.StatusForbidden || store.postActorID != 0 {
		t.Fatalf("cross-origin status=%d actor=%d", crossOriginRes.Code, store.postActorID)
	}
}

func TestModerationMapsAuthorizationMissingAndAuthoritativeResults(t *testing.T) {
	base := &apiStore{}
	store := &moderationAPIStore{apiStore: base, postErr: moderation.ErrForbidden}
	server := newAuthTestServer(store)

	forbidden := authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/moderation", "")
	forbiddenRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(forbiddenRes, forbidden)
	if forbiddenRes.Code != http.StatusForbidden || store.postActorID != 42 {
		t.Fatalf("forbidden status=%d actor=%d body=%s", forbiddenRes.Code, store.postActorID, forbiddenRes.Body.String())
	}

	store.postErr = moderation.ErrPostNotFound
	missingPost := authenticatedRequest(base, http.MethodPut, "/api/v2/posts/999/moderation", "")
	missingPostRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingPostRes, missingPost)
	if missingPostRes.Code != http.StatusNotFound || !strings.Contains(missingPostRes.Body.String(), `"code":"post_not_found"`) {
		t.Fatalf("missing post status=%d body=%s", missingPostRes.Code, missingPostRes.Body.String())
	}

	store.postErr = nil
	okPost := authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/moderation", "")
	okPostRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(okPostRes, okPost)
	if okPostRes.Code != http.StatusOK {
		t.Fatalf("post status=%d body=%s", okPostRes.Code, okPostRes.Body.String())
	}
	var post moderation.PostResult
	if err := json.Unmarshal(okPostRes.Body.Bytes(), &post); err != nil {
		t.Fatal(err)
	}
	if post.PostID != 42 || !post.Deleted || post.ModeratedByUserID != 42 || post.ModeratedAt.IsZero() {
		t.Fatalf("post result=%#v", post)
	}

	store.commentErr = moderation.ErrCommentNotFound
	missingComment := authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/comments/777/moderation", "")
	missingCommentRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(missingCommentRes, missingComment)
	if missingCommentRes.Code != http.StatusNotFound || !strings.Contains(missingCommentRes.Body.String(), `"code":"comment_not_found"`) {
		t.Fatalf("missing comment status=%d body=%s", missingCommentRes.Code, missingCommentRes.Body.String())
	}

	store.commentErr = nil
	okComment := authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/comments/7/moderation", "")
	okCommentRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(okCommentRes, okComment)
	if okCommentRes.Code != http.StatusOK {
		t.Fatalf("comment status=%d body=%s", okCommentRes.Code, okCommentRes.Body.String())
	}
	var commentResult moderation.CommentResult
	if err := json.Unmarshal(okCommentRes.Body.Bytes(), &commentResult); err != nil {
		t.Fatal(err)
	}
	if commentResult.PostID != 42 || commentResult.CommentID != 7 || !commentResult.Deleted || commentResult.ModeratedByUserID != 42 {
		t.Fatalf("comment result=%#v", commentResult)
	}
}

func TestModerationRejectsInvalidCommentIDBeforeMutation(t *testing.T) {
	base := &apiStore{}
	store := &moderationAPIStore{apiStore: base}
	res := httptest.NewRecorder()
	newAuthTestServer(store).Handler().ServeHTTP(
		res,
		authenticatedRequest(base, http.MethodPut, "/api/v2/posts/42/comments/0/moderation", ""),
	)
	if res.Code != http.StatusBadRequest || store.commentActorID != 0 {
		t.Fatalf("status=%d actor=%d body=%s", res.Code, store.commentActorID, res.Body.String())
	}
}
