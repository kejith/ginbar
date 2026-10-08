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

	"github.com/kejith/ginbar/backend/v2/internal/auth"
	"github.com/kejith/ginbar/backend/v2/internal/comment"
	"github.com/kejith/ginbar/backend/v2/internal/commentvote"
	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/ingest"
	"github.com/kejith/ginbar/backend/v2/internal/mediajobadmin"
	"github.com/kejith/ginbar/backend/v2/internal/mediastatus"
	"github.com/kejith/ginbar/backend/v2/internal/moderation"
	"github.com/kejith/ginbar/backend/v2/internal/postvote"
	"github.com/kejith/ginbar/backend/v2/internal/privatemessage"
	"github.com/kejith/ginbar/backend/v2/internal/profile"
	"github.com/kejith/ginbar/backend/v2/internal/regenerate"
	"github.com/kejith/ginbar/backend/v2/internal/roleadmin"
	"github.com/kejith/ginbar/backend/v2/internal/search"
	"github.com/kejith/ginbar/backend/v2/internal/tag"
)

type Store interface {
	feed.Store
	mediastatus.Store
	auth.Store
	postvote.Store
	comment.Store
	commentvote.Store
	tag.Store
	profile.Store
	privatemessage.Store
	moderation.Store
	mediajobadmin.Store
	regenerate.Repository
	roleadmin.Store
}

type Config struct {
	RequestTimeout       time.Duration
	IngestRequestTimeout time.Duration
	CookieSecure         bool
	TrustLoopbackProxy   bool
	Auth                 auth.Config
	Ingest               *ingest.Service
}

func DefaultConfig() Config {
	return Config{
		RequestTimeout:       3 * time.Second,
		IngestRequestTimeout: 2 * time.Minute,
		CookieSecure:         true,
		Auth:                 auth.DefaultConfig(),
	}
}

type Server struct {
	feed                 *feed.Service
	mediaStatus          *mediastatus.Service
	auth                 *auth.Service
	postVote             *postvote.Service
	comments             *comment.Service
	commentVote          *commentvote.Service
	tags                 *tag.Service
	profiles             *profile.Service
	privateMessages      *privatemessage.Service
	moderation           *moderation.Service
	mediaJobs            *mediajobadmin.Service
	regeneration         *regenerate.Service
	roleAdmin            *roleadmin.Service
	ingest               *ingest.Service
	mux                  *http.ServeMux
	requestTimeout       time.Duration
	ingestRequestTimeout time.Duration
	cookieSecure         bool
	trustLoopbackProxy   bool
	sessionTTL           time.Duration
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func New(store Store) *Server { return NewWithConfig(store, DefaultConfig()) }

func NewWithConfig(store Store, cfg Config) *Server {
	defaults := DefaultConfig()
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaults.RequestTimeout
	}
	if cfg.IngestRequestTimeout <= 0 {
		cfg.IngestRequestTimeout = defaults.IngestRequestTimeout
	}
	if cfg.Auth.PasswordParams == (auth.PasswordParams{}) {
		cfg.Auth.PasswordParams = defaults.Auth.PasswordParams
	}
	if cfg.Auth.SessionTTL <= 0 {
		cfg.Auth.SessionTTL = defaults.Auth.SessionTTL
	}
	s := &Server{
		feed:                 feed.New(store),
		mediaStatus:          mediastatus.New(store),
		auth:                 auth.New(store, cfg.Auth),
		postVote:             postvote.New(store),
		comments:             comment.New(store),
		commentVote:          commentvote.New(store),
		tags:                 tag.New(store),
		profiles:             profile.New(store),
		privateMessages:      privatemessage.New(store),
		moderation:           moderation.New(store),
		mediaJobs:            mediajobadmin.New(store),
		regeneration:         regenerate.New(store),
		roleAdmin:            roleadmin.New(store),
		ingest:               cfg.Ingest,
		mux:                  http.NewServeMux(),
		requestTimeout:       cfg.RequestTimeout,
		ingestRequestTimeout: cfg.IngestRequestTimeout,
		cookieSecure:         cfg.CookieSecure,
		trustLoopbackProxy:   cfg.TrustLoopbackProxy,
		sessionTTL:           cfg.Auth.SessionTTL,
	}
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /api/v2/feed", s.listFeed)
	s.mux.HandleFunc("GET /api/v2/users/{id}", s.getProfile)
	s.mux.Handle("GET /api/v2/messages", s.requireAuth(http.HandlerFunc(s.listPrivateMessageInbox)))
	s.mux.Handle("GET /api/v2/messages/{id}", s.requireAuth(http.HandlerFunc(s.listPrivateMessages)))
	s.mux.Handle("POST /api/v2/messages/{id}", s.requireAuth(http.HandlerFunc(s.sendPrivateMessage)))
	s.mux.HandleFunc("GET /api/v2/posts/{id}/around", s.aroundPost)
	s.mux.HandleFunc("GET /api/v2/posts/{id}/media-status", s.postMediaStatus)
	s.mux.HandleFunc("GET /api/v2/posts/{id}/comments", s.listComments)
	s.mux.Handle("POST /api/v2/posts/{id}/comments", s.requireAuth(http.HandlerFunc(s.createComment)))
	s.mux.Handle("PUT /api/v2/posts/{id}/comments/{commentId}/vote", s.requireAuth(http.HandlerFunc(s.setCommentVote)))
	s.mux.Handle("PUT /api/v2/posts/{id}/comments/{commentId}/moderation", s.requireAuth(http.HandlerFunc(s.moderateComment)))
	s.mux.Handle("PUT /api/v2/posts/{id}/vote", s.requireAuth(http.HandlerFunc(s.setPostVote)))
	s.mux.Handle("PUT /api/v2/posts/{id}/moderation", s.requireAuth(http.HandlerFunc(s.moderatePost)))
	s.mux.Handle("GET /api/v2/admin/media-jobs", s.requireAuth(http.HandlerFunc(s.listMediaJobs)))
	s.mux.Handle("POST /api/v2/admin/posts/{id}/regeneration", s.requireAuth(http.HandlerFunc(s.requestRegeneration)))
	s.mux.Handle("GET /api/v2/admin/users/{id}/roles", s.requireAuth(http.HandlerFunc(s.getUserRoles)))
	s.mux.Handle("PUT /api/v2/admin/users/{id}/roles/moderator", s.requireAuth(http.HandlerFunc(s.grantModerator)))
	s.mux.Handle("DELETE /api/v2/admin/users/{id}/roles/moderator", s.requireAuth(http.HandlerFunc(s.revokeModerator)))
	s.mux.Handle("PUT /api/v2/admin/users/{id}/roles/admin", s.requireAuth(http.HandlerFunc(s.grantAdmin)))
	s.mux.Handle("DELETE /api/v2/admin/users/{id}/roles/admin", s.requireAuth(http.HandlerFunc(s.revokeAdmin)))
	s.mux.HandleFunc("GET /api/v2/posts/{id}/tags", s.listPostTags)
	s.mux.Handle("POST /api/v2/posts/{id}/tags", s.requireAuth(http.HandlerFunc(s.addPostTag)))
	s.mux.Handle("DELETE /api/v2/posts/{id}/tags/{tagId}", s.requireAuth(http.HandlerFunc(s.removePostTag)))
	if s.ingest != nil {
		s.mux.Handle("POST /api/v2/posts/upload", s.requireAuth(http.HandlerFunc(s.createPostUpload)))
		s.mux.Handle("POST /api/v2/posts/import-url", s.requireAuth(http.HandlerFunc(s.createPostFromURL)))
	}
	s.mux.HandleFunc("POST /api/v2/auth/register", s.register)
	s.mux.HandleFunc("POST /api/v2/auth/login", s.login)
	s.mux.HandleFunc("POST /api/v2/auth/logout", s.logout)
	s.mux.Handle("GET /api/v2/auth/me", s.requireAuth(http.HandlerFunc(s.currentUser)))
	return s
}

func newServer(store Store, requestTimeout time.Duration) *Server {
	cfg := DefaultConfig()
	cfg.RequestTimeout = requestTimeout
	return NewWithConfig(store, cfg)
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timeout := s.requestTimeout
		if s.isIngestRequest(r) {
			timeout = s.ingestRequestTimeout
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		s.mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) isIngestRequest(r *http.Request) bool {
	if s.ingest == nil || r.Method != http.MethodPost {
		return false
	}
	return r.URL.Path == "/api/v2/posts/upload" || r.URL.Path == "/api/v2/posts/import-url"
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
	viewerUserID, err := s.viewerUserID(r)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	page, err := s.feed.List(r.Context(), feed.Query{
		Before:       before,
		Limit:        limit,
		ViewerUserID: viewerUserID,
		Search:       parsed,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) aroundPost(w http.ResponseWriter, r *http.Request) {
	postID, ok := parsePostID(w, r)
	if !ok {
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
	viewerUserID, err := s.viewerUserID(r)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	result, err := s.feed.Around(r.Context(), feed.AroundQuery{
		PostID:       postID,
		Radius:       radius,
		ViewerUserID: viewerUserID,
		Search:       parsed,
	})
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

func (s *Server) postMediaStatus(w http.ResponseWriter, r *http.Request) {
	postID, ok := parsePostID(w, r)
	if !ok {
		return
	}
	status, err := s.mediaStatus.Public(r.Context(), postID)
	if errors.Is(err, mediastatus.ErrPostNotFound) {
		writeError(w, http.StatusNotFound, "post_not_found", "post not found")
		return
	}
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func parsePostID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	postID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || postID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_post_id", "post id must be a positive integer")
		return 0, false
	}
	return postID, true
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
