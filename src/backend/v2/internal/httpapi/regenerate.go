package httpapi

import (
	"errors"
	"net/http"

	"github.com/kejith/ginbar/backend/v2/internal/regenerate"
)

type regenerationResponse struct {
	PostID  int64              `json:"postId"`
	JobID   int64              `json:"jobId"`
	Outcome regenerate.Outcome `json:"outcome"`
}

func (s *Server) requestRegeneration(w http.ResponseWriter, r *http.Request) {
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

	requested, err := s.regeneration.Request(r.Context(), principal.UserID, postID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, regenerationResponse{
			PostID:  postID,
			JobID:   requested.JobID,
			Outcome: requested.Outcome,
		})
	case errors.Is(err, regenerate.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "media regeneration requires admin role")
	case errors.Is(err, regenerate.ErrNotRegenerable):
		writeError(w, http.StatusConflict, "post_not_regenerable", "post is not regenerable")
	default:
		writeServiceError(w, r, err)
	}
}
