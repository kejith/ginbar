package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/kejith/ginbar/backend/v2/internal/tag"
)

type addPostTagRequest struct {
	Name string `json:"name"`
}

func (s *Server) listPostTags(w http.ResponseWriter, r *http.Request) {
	postID, ok := parsePostID(w, r)
	if !ok {
		return
	}
	viewerUserID, err := s.viewerUserID(r)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	snapshot, err := s.tags.List(r.Context(), postID, viewerUserID)
	if errors.Is(err, tag.ErrPostNotFound) {
		writeError(w, http.StatusNotFound, "post_not_found", "post not found")
		return
	}
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) addPostTag(w http.ResponseWriter, r *http.Request) {
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
	var request addPostTagRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}

	snapshot, err := s.tags.Add(r.Context(), principal.UserID, postID, request.Name)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, snapshot)
	case errors.Is(err, tag.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "invalid_tag", "tag name is invalid")
	case errors.Is(err, tag.ErrPostNotFound):
		writeError(w, http.StatusNotFound, "post_not_found", "post not found")
	default:
		writeServiceError(w, r, err)
	}
}

func (s *Server) removePostTag(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	postID, ok := parsePostID(w, r)
	if !ok {
		return
	}
	tagID, err := strconv.ParseInt(r.PathValue("tagId"), 10, 64)
	if err != nil || tagID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_tag_id", "tag id must be a positive integer")
		return
	}
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	snapshot, err := s.tags.Remove(r.Context(), principal.UserID, postID, tagID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, snapshot)
	case errors.Is(err, tag.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "tag removal requires moderator or admin role")
	case errors.Is(err, tag.ErrPostNotFound):
		writeError(w, http.StatusNotFound, "post_not_found", "post not found")
	default:
		writeServiceError(w, r, err)
	}
}
