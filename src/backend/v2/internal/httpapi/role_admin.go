package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/kejith/ginbar/backend/v2/internal/roleadmin"
)

func (s *Server) getUserRoles(w http.ResponseWriter, r *http.Request) {
	targetUserID, ok := parseRoleAdminUserID(w, r)
	if !ok {
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	state, err := s.roleAdmin.Get(r.Context(), principal.UserID, targetUserID)
	writeRoleAdminResult(w, r, state, err)
}

func (s *Server) grantModerator(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	targetUserID, ok := parseRoleAdminUserID(w, r)
	if !ok {
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	state, err := s.roleAdmin.GrantModerator(r.Context(), principal.UserID, targetUserID)
	writeRoleAdminResult(w, r, state, err)
}

func (s *Server) revokeModerator(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	targetUserID, ok := parseRoleAdminUserID(w, r)
	if !ok {
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	state, err := s.roleAdmin.RevokeModerator(r.Context(), principal.UserID, targetUserID)
	writeRoleAdminResult(w, r, state, err)
}

func parseRoleAdminUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	userID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_user_id", "user id must be a positive integer")
		return 0, false
	}
	return userID, true
}

func writeRoleAdminResult(w http.ResponseWriter, r *http.Request, state roleadmin.State, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, state)
	case errors.Is(err, roleadmin.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "role administration requires admin role")
	case errors.Is(err, roleadmin.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "user_not_found", "user not found")
	default:
		writeServiceError(w, r, err)
	}
}
