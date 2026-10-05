package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/kejith/ginbar/backend/v2/internal/comment"
)

const maxCommentRequestBytes = comment.MaxBodyCharacters*4 + 1024

type createCommentRequest struct {
	ParentCommentID *int64  `json:"parentCommentId"`
	Body            *string `json:"body"`
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) {
	postID, ok := parsePostID(w, r)
	if !ok {
		return
	}
	after, err := optionalPositiveInt64(r.URL.Query().Get("after"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cursor", err.Error())
		return
	}
	limit, err := optionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	viewerUserID, err := s.viewerUserID(r)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	page, err := s.comments.List(r.Context(), comment.Query{
		PostID:       postID,
		After:        after,
		Limit:        limit,
		ViewerUserID: viewerUserID,
	})
	if errors.Is(err, comment.ErrPostNotFound) {
		writeError(w, http.StatusNotFound, "post_not_found", "post not found")
		return
	}
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) createComment(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	postID, ok := parsePostID(w, r)
	if !ok {
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	var request createCommentRequest
	if !decodeCommentJSON(w, r, &request) {
		return
	}
	if request.Body == nil {
		writeError(w, http.StatusBadRequest, "invalid_comment_body", "comment body must contain 1 to 10000 characters")
		return
	}
	created, err := s.comments.Create(r.Context(), comment.CreateRequest{
		PostID:          postID,
		UserID:          principal.UserID,
		ParentCommentID: request.ParentCommentID,
		Body:            *request.Body,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, created)
	case errors.Is(err, comment.ErrInvalidBody):
		writeError(w, http.StatusBadRequest, "invalid_comment_body", "comment body must contain 1 to 10000 characters")
	case errors.Is(err, comment.ErrInvalidParentComment):
		writeError(w, http.StatusBadRequest, "invalid_parent_comment_id", "parent comment id must be a positive integer")
	case errors.Is(err, comment.ErrPostNotFound):
		writeError(w, http.StatusNotFound, "post_not_found", "post not found")
	case errors.Is(err, comment.ErrParentCommentNotFound):
		writeError(w, http.StatusNotFound, "parent_comment_not_found", "parent comment not found")
	case errors.Is(err, comment.ErrParentCommentDeleted):
		writeError(w, http.StatusConflict, "parent_comment_deleted", "cannot reply to a deleted comment")
	default:
		writeServiceError(w, r, err)
	}
}

func decodeCommentJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxCommentRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return false
	}
	return true
}
