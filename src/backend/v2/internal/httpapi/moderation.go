package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/kejith/ginbar/backend/v2/internal/moderation"
)

func (s *Server) moderatePost(w http.ResponseWriter, r *http.Request) {
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

	result, err := s.moderation.HidePost(r.Context(), principal.UserID, postID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, result)
	case errors.Is(err, moderation.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "post moderation requires moderator or admin role")
	case errors.Is(err, moderation.ErrPostNotFound):
		writeError(w, http.StatusNotFound, "post_not_found", "post not found")
	default:
		writeServiceError(w, r, err)
	}
}

func (s *Server) moderateComment(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	postID, ok := parsePostID(w, r)
	if !ok {
		return
	}
	commentID, err := strconv.ParseInt(r.PathValue("commentId"), 10, 64)
	if err != nil || commentID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_comment_id", "comment id must be a positive integer")
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	result, err := s.moderation.HideComment(r.Context(), principal.UserID, postID, commentID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, result)
	case errors.Is(err, moderation.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "comment moderation requires moderator or admin role")
	case errors.Is(err, moderation.ErrCommentNotFound):
		writeError(w, http.StatusNotFound, "comment_not_found", "comment not found")
	default:
		writeServiceError(w, r, err)
	}
}
