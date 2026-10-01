package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/search"
)

type Server struct {
	feed           *feed.Service
	mux            *http.ServeMux
	requestTimeout time.Duration
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func New(store feed.Store) *Server { return newServer(store, 3*time.Second) }

func newServer(store feed.Store, requestTimeout time.Duration) *Server {
	s := &Server{feed: feed.New(store), mux: http.NewServeMux(), requestTimeout: requestTimeout}
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /api/v2/feed", s.listFeed)
	s.mux.HandleFunc("GET /api/v2/posts/{id}/around", s.aroundPost)
	return s
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
		defer cancel()
		s.mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listFeed(w http.ResponseWriter, r *http.Request) {
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
	parsed, err := search.Parse(r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_search", err.Error())
		return
	}
	page, err := s.feed.List(r.Context(), feed.Query{Before: before, Limit: limit, Search: parsed})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) aroundPost(w http.ResponseWriter, r *http.Request) {
	postID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || postID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_post_id", "post id must be a positive integer")
		return
	}
	radius, err := optionalPositiveInt(r.URL.Query().Get("radius"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_radius", err.Error())
		return
	}
	parsed, err := search.Parse(r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_search", err.Error())
		return
	}
	result, err := s.feed.Around(r.Context(), feed.AroundQuery{PostID: postID, Radius: radius, Search: parsed})
	if errors.Is(err, feed.ErrPostNotFound) {
		writeError(w, http.StatusNotFound, "post_not_found", "post not found")
		return
	}
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(r.Context().Err(), context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, "timeout", "request timed out")
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(r.Context().Err(), context.Canceled) {
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", "request failed")
}

func optionalPositiveInt64(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("value must be a positive integer")
	}
	return parsed, nil
}

func optionalPositiveInt(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("value must be a positive integer")
	}
	return parsed, nil
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSONStatus(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) { writeJSONStatus(w, status, value) }

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil && !strings.Contains(err.Error(), "closed") {
		return
	}
}
