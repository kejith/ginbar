package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/auth"
)

const (
	sessionCookieName = "ginbar_session"
	maxAuthBodyBytes  = 4096
)

type principalContextKey struct{}

type authUserResponse struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type authResponse struct {
	User authUserResponse `json:"user"`
}

type registerRequest struct {
	InvitationToken string `json:"invitation_token"`
	Username        string `json:"username"`
	Password        string `json:"password"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	var request registerRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	principal, err := s.auth.Register(r.Context(), auth.RegistrationRequest{
		InvitationToken: request.InvitationToken,
		Username:        request.Username,
		Password:        request.Password,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, authResponse{User: authUser(principal)})
	case errors.Is(err, auth.ErrInvalidRegistration):
		writeError(w, http.StatusBadRequest, "invalid_registration", "registration input is invalid")
	case errors.Is(err, auth.ErrInvalidInvitation):
		writeError(w, http.StatusBadRequest, "invalid_invitation", "invitation is invalid or unavailable")
	case errors.Is(err, auth.ErrUsernameUnavailable):
		writeError(w, http.StatusConflict, "username_unavailable", "username is unavailable")
	case errors.Is(err, auth.ErrKDFSaturated):
		writeError(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication capacity is temporarily unavailable")
	default:
		writeServiceError(w, r, err)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	var request loginRequest
	if !decodeBoundedJSON(w, r, &request) {
		return
	}
	result, err := s.auth.Login(r.Context(), request.Username, request.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "username or credential is invalid")
		return
	}
	if errors.Is(err, auth.ErrKDFSaturated) {
		writeError(w, http.StatusServiceUnavailable, "authentication_unavailable", "authentication capacity is temporarily unavailable")
		return
	}
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	s.setSessionCookie(w, result.Token, result.ExpiresAt)
	writeJSON(w, http.StatusOK, authResponse{User: authUser(result.Principal)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if err := s.auth.Logout(r.Context(), cookie.Value); err != nil {
			writeServiceError(w, r, err)
			return
		}
	}
	s.clearSessionCookie(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, authResponse{User: authUser(principal)})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		principal, err := s.auth.Authenticate(r.Context(), cookie.Value)
		if errors.Is(err, auth.ErrUnauthenticated) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if err != nil {
			writeServiceError(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func PrincipalFromContext(ctx context.Context) (auth.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(auth.Principal)
	return principal, ok
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(s.sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return false
	}
	return true
}

func (s *Server) sameOrigin(r *http.Request) bool {
	raw := r.Header.Get("Origin")
	if raw == "" {
		return false
	}
	origin, err := url.Parse(raw)
	if err != nil || origin.User != nil || origin.Host == "" || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		return false
	}
	if origin.Scheme != s.requestScheme(r) {
		return false
	}
	return strings.EqualFold(origin.Host, r.Host)
}

// requestScheme only accepts the TLS terminator's scheme when the immediate
// TCP peer is loopback and explicitly trusted by runtime configuration.
// A direct request must never be able to select its scheme via HTTP headers.
func (s *Server) requestScheme(r *http.Request) string {
	if s.trustLoopbackProxy {
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			addr, parseErr := netip.ParseAddr(peer)
			if parseErr == nil && addr.Unmap().IsLoopback() {
				values := r.Header.Values("X-Forwarded-Proto")
				if len(values) != 0 {
					// nginx overwrites this with one unambiguous $scheme.
					// Never accept chains, duplicates or noncanonical values.
					if len(values) != 1 || (values[0] != "http" && values[0] != "https") {
						return ""
					}
					return values[0]
				}
			}
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func authUser(principal auth.Principal) authUserResponse {
	return authUserResponse{ID: principal.UserID, Username: principal.Username}
}
