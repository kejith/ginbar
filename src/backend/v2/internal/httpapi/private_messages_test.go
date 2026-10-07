package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/privatemessage"
)

// Keep the shared apiStore satisfying Server's aggregate Store interface for unrelated HTTP tests.
func (s *apiStore) SendPrivateMessage(_ context.Context, _ privatemessage.SendRequest) (privatemessage.Message, error) {
	return privatemessage.Message{}, privatemessage.ErrRecipientUnavailable
}

func (s *apiStore) ListPrivateMessages(_ context.Context, _ privatemessage.Query) ([]privatemessage.Message, error) {
	return nil, privatemessage.ErrRecipientUnavailable
}

type privateMessageAPIStore struct {
	*apiStore
	sendResult privatemessage.Message
	messages   []privatemessage.Message
	err        error
	lastSend   privatemessage.SendRequest
	lastQuery  privatemessage.Query
	sendCalls  int
	listCalls  int
}

func (s *privateMessageAPIStore) SendPrivateMessage(_ context.Context, request privatemessage.SendRequest) (privatemessage.Message, error) {
	s.sendCalls++
	s.lastSend = request
	if s.err != nil {
		return privatemessage.Message{}, s.err
	}
	return s.sendResult, nil
}

func (s *privateMessageAPIStore) ListPrivateMessages(_ context.Context, query privatemessage.Query) ([]privatemessage.Message, error) {
	s.listCalls++
	s.lastQuery = query
	if s.err != nil {
		return nil, s.err
	}
	return s.messages, nil
}

func TestPrivateMessageSendRequiresAuthenticationAndSameOrigin(t *testing.T) {
	base := &apiStore{}
	store := &privateMessageAPIStore{apiStore: base}
	server := newAuthTestServer(store)

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, sameOriginRequest(http.MethodPost, "/api/v2/messages/77", `{"body":"hello"}`))
	if res.Code != http.StatusUnauthorized || store.sendCalls != 0 {
		t.Fatalf("signed-out status=%d calls=%d body=%s", res.Code, store.sendCalls, res.Body.String())
	}

	req := authenticatedRequest(base, http.MethodPost, "/api/v2/messages/77", `{"body":"hello"}`)
	req.Header.Set("Origin", "https://evil.test")
	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden || store.sendCalls != 0 || !strings.Contains(res.Body.String(), `"code":"origin_not_allowed"`) {
		t.Fatalf("cross-origin status=%d calls=%d body=%s", res.Code, store.sendCalls, res.Body.String())
	}
}

func TestPrivateMessageSendUsesSessionSenderAndReturnsAuthoritativeMessage(t *testing.T) {
	base := &apiStore{}
	createdAt := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	store := &privateMessageAPIStore{
		apiStore: base,
		sendResult: privatemessage.Message{
			ID:          91,
			SenderID:    42,
			RecipientID: 77,
			Body:        "hello",
			CreatedAt:   createdAt,
		},
	}
	server := newAuthTestServer(store)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPost, "/api/v2/messages/77", `{"body":"hello"}`))
	if res.Code != http.StatusCreated || store.sendCalls != 1 || store.lastSend.SenderUserID != 42 || store.lastSend.RecipientUserID != 77 || store.lastSend.Body != "hello" {
		t.Fatalf("status=%d calls=%d send=%#v body=%s", res.Code, store.sendCalls, store.lastSend, res.Body.String())
	}
	var message privatemessage.Message
	if err := json.Unmarshal(res.Body.Bytes(), &message); err != nil {
		t.Fatal(err)
	}
	if message != store.sendResult {
		t.Fatalf("response=%#v want=%#v", message, store.sendResult)
	}

	store.sendCalls = 0
	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPost, "/api/v2/messages/77", `{"senderId":999,"body":"hello"}`))
	if res.Code != http.StatusBadRequest || store.sendCalls != 0 || !strings.Contains(res.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("override status=%d calls=%d body=%s", res.Code, store.sendCalls, res.Body.String())
	}
}

func TestPrivateMessageReadRequiresAuthenticationAndPassesNumericCursorQuery(t *testing.T) {
	base := &apiStore{}
	store := &privateMessageAPIStore{
		apiStore: base,
		messages: []privatemessage.Message{{ID: 30}, {ID: 29}, {ID: 28}},
	}
	server := newAuthTestServer(store)

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v2/messages/77", nil))
	if res.Code != http.StatusUnauthorized || store.listCalls != 0 {
		t.Fatalf("signed-out status=%d calls=%d body=%s", res.Code, store.listCalls, res.Body.String())
	}

	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodGet, "/api/v2/messages/77?before=31&limit=2", ""))
	if res.Code != http.StatusOK || store.listCalls != 1 || store.lastQuery.ActorUserID != 42 || store.lastQuery.PeerUserID != 77 || store.lastQuery.Before != 31 || store.lastQuery.Limit != 2 {
		t.Fatalf("status=%d calls=%d query=%#v body=%s", res.Code, store.listCalls, store.lastQuery, res.Body.String())
	}
	var page privatemessage.Page
	if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 || page.Messages[0].ID != 30 || page.Messages[1].ID != 29 || page.NextBefore != 29 {
		t.Fatalf("page=%#v", page)
	}
}

func TestPrivateMessageHTTPValidationAndStableErrors(t *testing.T) {
	base := &apiStore{}
	store := &privateMessageAPIStore{apiStore: base}
	server := newAuthTestServer(store)

	for _, path := range []string{"/api/v2/messages/0", "/api/v2/messages/-1", "/api/v2/messages/not-a-number"} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodGet, path, ""))
		if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), `"code":"invalid_peer_id"`) {
			t.Fatalf("path=%s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	for _, path := range []string{"/api/v2/messages/77?before=0", "/api/v2/messages/77?limit=0"} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodGet, path, ""))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	if store.listCalls != 0 {
		t.Fatalf("invalid reads reached store: %d", store.listCalls)
	}

	for _, payload := range []string{`{}`, `{"body":null}`, `{"body":""}`, `{"body":"a\u0000b"}`, `{"extra":true,"body":"ok"}`, `{"body":`} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPost, "/api/v2/messages/77", payload))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("payload=%q status=%d body=%s", payload, res.Code, res.Body.String())
		}
	}
	if store.sendCalls != 0 {
		t.Fatalf("invalid sends reached store: %d", store.sendCalls)
	}

	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		method     string
	}{
		{name: "send-unavailable", err: privatemessage.ErrRecipientUnavailable, wantStatus: http.StatusNotFound, wantCode: "recipient_unavailable", method: http.MethodPost},
		{name: "send-self", err: privatemessage.ErrSelfMessage, wantStatus: http.StatusConflict, wantCode: "self_message_forbidden", method: http.MethodPost},
		{name: "send-internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "internal", method: http.MethodPost},
		{name: "read-unavailable", err: privatemessage.ErrRecipientUnavailable, wantStatus: http.StatusNotFound, wantCode: "recipient_unavailable", method: http.MethodGet},
		{name: "read-self", err: privatemessage.ErrSelfMessage, wantStatus: http.StatusConflict, wantCode: "self_message_forbidden", method: http.MethodGet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.err = tc.err
			res := httptest.NewRecorder()
			body := ""
			if tc.method == http.MethodPost {
				body = `{"body":"ok"}`
			}
			server.Handler().ServeHTTP(res, authenticatedRequest(base, tc.method, "/api/v2/messages/77", body))
			if res.Code != tc.wantStatus || !strings.Contains(res.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
}

func TestPrivateMessageSendUnicodeAndRequestSizeBoundaries(t *testing.T) {
	base := &apiStore{}
	store := &privateMessageAPIStore{apiStore: base}
	server := newAuthTestServer(store)
	for _, tt := range []struct {
		body string
		want int
	}{
		{body: strings.Repeat("😀", privatemessage.MaxBodyCharacters), want: http.StatusCreated},
		{body: strings.Repeat("😀", privatemessage.MaxBodyCharacters+1), want: http.StatusBadRequest},
	} {
		store.err = nil
		store.sendResult = privatemessage.Message{ID: 1, SenderID: 42, RecipientID: 77, Body: tt.body}
		body := tt.body
		payload, err := json.Marshal(sendPrivateMessageRequest{Body: &body})
		if err != nil {
			t.Fatal(err)
		}
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPost, "/api/v2/messages/77", string(payload)))
		if res.Code != tt.want {
			t.Fatalf("chars=%d status=%d body=%s", len([]rune(tt.body)), res.Code, res.Body.String())
		}
	}

	callsBefore := store.sendCalls
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodPost, "/api/v2/messages/77", strings.Repeat("x", maxPrivateMessageRequestBytes+1)))
	if res.Code != http.StatusBadRequest || store.sendCalls != callsBefore {
		t.Fatalf("oversized status=%d calls=%d/%d body=%s", res.Code, store.sendCalls, callsBefore, res.Body.String())
	}
}
