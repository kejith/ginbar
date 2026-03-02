package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

// ── Request ID ────────────────────────────────────────────────────────────────

// newRequestID generates a 16-byte (32 hex char) random correlation ID.
// Falls back to "unknown" on the (essentially impossible) rand failure.
func newRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

// ── Body masking ──────────────────────────────────────────────────────────────

// sensitiveKeys is the deny-list of JSON field names whose values are replaced
// with "[REDACTED]" before any body is logged.
var sensitiveKeys = map[string]struct{}{
	"password":      {},
	"token":         {},
	"invite_token":  {},
	"secret":        {},
	"authorization": {},
	"session_id":    {},
	"access_token":  {},
	"refresh_token": {},
}

const maxBodyLogBytes = 2048

// maskBody parses raw JSON, redacts sensitive keys, and returns the result as
// a value suitable for passing to slog as an "any" attribute.
// Non-JSON bodies and oversized bodies are represented as a short string.
func maskBody(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxBodyLogBytes {
		return "[body too large to log]"
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		// Not JSON (form data, binary, etc.) — don't risk logging raw content.
		return "[non-JSON body]"
	}
	redactMap(m)
	return m
}

// redactMap recursively replaces values of sensitive keys with "[REDACTED]".
func redactMap(m map[string]any) {
	for k, v := range m {
		if _, sensitive := sensitiveKeys[k]; sensitive {
			m[k] = "[REDACTED]"
			continue
		}
		// Recurse into nested objects.
		if nested, ok := v.(map[string]any); ok {
			redactMap(nested)
		}
	}
}

// truncate returns s if len(s) <= max, otherwise returns the first max bytes
// with a "[…]" suffix.  Used to keep debug log lines readable.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "[…]"
}
