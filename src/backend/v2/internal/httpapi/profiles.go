package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/kejith/ginbar/backend/v2/internal/profile"
)

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_user_id", "user id must be a positive integer")
		return
	}
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
	page, err := s.profiles.Get(r.Context(), profile.Query{UserID: userID, Before: before, Limit: limit})
	if errors.Is(err, profile.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, "user_not_found", "user not found")
		return
	}
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
