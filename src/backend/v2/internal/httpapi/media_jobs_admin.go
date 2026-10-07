package httpapi

import (
	"errors"
	"net/http"

	"github.com/kejith/ginbar/backend/v2/internal/mediajobadmin"
)

func (s *Server) listMediaJobs(w http.ResponseWriter, r *http.Request) {
	before, err := optionalPositiveInt64(r.URL.Query().Get("before"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cursor", err.Error())
		return
	}
	limit, err := optionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	page, err := s.mediaJobs.List(r.Context(), mediajobadmin.Query{
		ActorUserID: principal.UserID,
		Before:      before,
		Limit:       limit,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, page)
	case errors.Is(err, mediajobadmin.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "media job inspection requires moderator or admin role")
	default:
		writeServiceError(w, r, err)
	}
}
