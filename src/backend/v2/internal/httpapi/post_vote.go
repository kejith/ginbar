package httpapi

import (
	"errors"
	"net/http"

	"github.com/kejith/ginbar/backend/v2/internal/auth"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/postvote"
)

type postVoteRequest struct {
	Vote *model.PostVote `json:"vote"`
}

func (s *Server) viewerUserID(r *http.Request) (int64, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if errors.Is(err, http.ErrNoCookie) {
		return 0, nil
	}
	if err != nil {
		return 0, nil
	}
	principal, err := s.auth.Authenticate(r.Context(), cookie.Value)
	if errors.Is(err, auth.ErrUnauthenticated) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return principal.UserID, nil
}

func (s *Server) setPostVote(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
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
	var request postVoteRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	if request.Vote == nil || !request.Vote.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_vote", "vote must be -1, 0, or 1")
		return
	}

	result, err := s.postVote.Set(r.Context(), principal.UserID, postID, *request.Vote)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, result)
	case errors.Is(err, postvote.ErrInvalidVote):
		writeError(w, http.StatusBadRequest, "invalid_vote", "vote must be -1, 0, or 1")
	case errors.Is(err, postvote.ErrPostNotFound):
		writeError(w, http.StatusNotFound, "post_not_found", "post not found")
	default:
		writeServiceError(w, r, err)
	}
}
