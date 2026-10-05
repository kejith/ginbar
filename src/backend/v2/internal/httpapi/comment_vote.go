package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/kejith/ginbar/backend/v2/internal/commentvote"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

type commentVoteRequest struct {
	Vote *model.PostVote `json:"vote"`
}

func (s *Server) setCommentVote(w http.ResponseWriter, r *http.Request) {
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
	var request commentVoteRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	if request.Vote == nil || !request.Vote.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_vote", "vote must be -1, 0, or 1")
		return
	}

	result, err := s.commentVote.Set(r.Context(), principal.UserID, postID, commentID, *request.Vote)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, result)
	case errors.Is(err, commentvote.ErrInvalidVote):
		writeError(w, http.StatusBadRequest, "invalid_vote", "vote must be -1, 0, or 1")
	case errors.Is(err, commentvote.ErrCommentNotFound):
		writeError(w, http.StatusNotFound, "comment_not_found", "comment not found")
	default:
		writeServiceError(w, r, err)
	}
}
