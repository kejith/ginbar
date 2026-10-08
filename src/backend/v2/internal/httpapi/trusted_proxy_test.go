package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTrustedProxySchemeAndSameOriginMutationContract(t *testing.T) {
	for _, tt := range []struct {
		name       string
		url        string
		remote     string
		origin     string
		forwarded  []string
		trust      bool
		wantStatus int
	}{
		{"trusted-https", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "https://ginbar.test", []string{"https"}, true, http.StatusNoContent},
		{"trusted-http", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "http://ginbar.test", []string{"http"}, true, http.StatusNoContent},
		{"trusted-ipv6", "http://ginbar.test/api/v2/auth/logout", "[::1]:1234", "https://ginbar.test", []string{"https"}, true, http.StatusNoContent},
		{"trusted-mapped-loopback", "http://ginbar.test/api/v2/auth/logout", "[::ffff:127.0.0.1]:1234", "https://ginbar.test", []string{"https"}, true, http.StatusNoContent},
		{"trusted-explicit-port", "http://ginbar.test:8443/api/v2/auth/logout", "127.0.0.1:1234", "https://ginbar.test:8443", []string{"https"}, true, http.StatusNoContent},
		{"trusted-wrong-port", "http://ginbar.test:8443/api/v2/auth/logout", "127.0.0.1:1234", "https://ginbar.test", []string{"https"}, true, http.StatusForbidden},
		{"trusted-wrong-origin", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "https://evil.test", []string{"https"}, true, http.StatusForbidden},
		{"trusted-scheme-mismatch", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "http://ginbar.test", []string{"https"}, true, http.StatusForbidden},
		{"disabled-even-on-loopback", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "https://ginbar.test", []string{"https"}, false, http.StatusForbidden},
		{"untrusted-direct-spoof", "http://ginbar.test/api/v2/auth/logout", "192.0.2.100:1234", "https://ginbar.test", []string{"https"}, true, http.StatusForbidden},
		{"untrusted-direct-ignore-header", "http://ginbar.test/api/v2/auth/logout", "192.0.2.100:1234", "http://ginbar.test", []string{"https"}, true, http.StatusNoContent},
		{"untrusted-bad-remote", "http://ginbar.test/api/v2/auth/logout", "unparsable", "https://ginbar.test", []string{"https"}, true, http.StatusForbidden},
		{"trusted-no-header", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "http://ginbar.test", nil, true, http.StatusNoContent},
		{"trusted-duplicate", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "https://ginbar.test", []string{"https", "https"}, true, http.StatusForbidden},
		{"trusted-chain", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "https://ginbar.test", []string{"https,http"}, true, http.StatusForbidden},
		{"trusted-capitalization", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "https://ginbar.test", []string{"HTTPS"}, true, http.StatusForbidden},
		{"trusted-whitespace", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "https://ginbar.test", []string{" https"}, true, http.StatusForbidden},
		{"trusted-empty", "http://ginbar.test/api/v2/auth/logout", "127.0.0.1:1234", "https://ginbar.test", []string{""}, true, http.StatusForbidden},
		{"direct-tls-overrides-untrusted-header", "https://ginbar.test/api/v2/auth/logout", "192.0.2.100:1234", "https://ginbar.test", []string{"http"}, true, http.StatusNoContent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.TrustLoopbackProxy = tt.trust
			server := NewWithConfig(&apiStore{}, cfg)
			req := httptest.NewRequest(http.MethodPost, tt.url, nil)
			req.RemoteAddr = tt.remote
			req.Header.Set("Origin", tt.origin)
			for _, value := range tt.forwarded {
				req.Header.Add("X-Forwarded-Proto", value)
			}
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != tt.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", res.Code, tt.wantStatus, res.Body.String())
			}
			if res.Code == http.StatusForbidden && !containsOriginRejection(res.Body.String()) {
				t.Fatalf("incorrect rejection body=%s", res.Body.String())
			}
		})
	}
}

func containsOriginRejection(body string) bool {
	return strings.Contains(body, "\"origin_not_allowed\"")
}
