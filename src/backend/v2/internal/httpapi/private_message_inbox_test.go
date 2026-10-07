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

func (s *apiStore) ListPrivateMessageInbox(_ context.Context, _ privatemessage.InboxQuery) ([]privatemessage.ConversationSummary, error) {
	return nil, nil
}

type privateMessageInboxAPIStore struct {
	*privateMessageAPIStore
	inbox          []privatemessage.ConversationSummary
	lastInboxQuery privatemessage.InboxQuery
	inboxErr       error
	inboxCalls     int
}

func (s *privateMessageInboxAPIStore) ListPrivateMessageInbox(_ context.Context, query privatemessage.InboxQuery) ([]privatemessage.ConversationSummary, error) {
	s.inboxCalls++
	s.lastInboxQuery = query
	if s.inboxErr != nil {
		return nil, s.inboxErr
	}
	return s.inbox, nil
}

func TestPrivateMessageInboxRequiresAuthenticationAndUsesSessionActor(t *testing.T) {
	base := &apiStore{}
	createdAt := time.Date(2026, time.October, 7, 18, 0, 0, 0, time.UTC)
	store := &privateMessageInboxAPIStore{
		privateMessageAPIStore: &privateMessageAPIStore{apiStore: base},
		inbox: []privatemessage.ConversationSummary{
			{Peer: privatemessage.InboxPeer{ID: 77, Username: "peer-77", Available: true}, LatestMessage: privatemessage.LatestMessageSummary{ID: 30, SenderID: 77, CreatedAt: createdAt}},
			{Peer: privatemessage.InboxPeer{ID: 78, Username: "peer-78", Available: true}, LatestMessage: privatemessage.LatestMessageSummary{ID: 29, SenderID: 42, CreatedAt: createdAt.Add(-time.Minute)}},
			{Peer: privatemessage.InboxPeer{ID: 79, Username: "peer-79", Available: true}, LatestMessage: privatemessage.LatestMessageSummary{ID: 28, SenderID: 79, CreatedAt: createdAt.Add(-2 * time.Minute)}},
		},
	}
	server := newAuthTestServer(store)

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v2/messages", nil))
	if res.Code != http.StatusUnauthorized || store.inboxCalls != 0 {
		t.Fatalf("signed-out status=%d calls=%d body=%s", res.Code, store.inboxCalls, res.Body.String())
	}

	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodGet, "/api/v2/messages?before=31&limit=2", ""))
	if res.Code != http.StatusOK || store.inboxCalls != 1 || store.lastInboxQuery.ActorUserID != 42 || store.lastInboxQuery.Before != 31 || store.lastInboxQuery.Limit != 2 {
		t.Fatalf("status=%d calls=%d query=%#v body=%s", res.Code, store.inboxCalls, store.lastInboxQuery, res.Body.String())
	}
	var page privatemessage.InboxPage
	if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Conversations) != 2 || page.Conversations[0].Peer.ID != 77 || page.Conversations[1].Peer.ID != 78 || page.NextBefore != 29 {
		t.Fatalf("page=%#v", page)
	}
}

func TestPrivateMessageInboxValidationUnavailablePeerShapeAndStableInternalError(t *testing.T) {
	base := &apiStore{}
	store := &privateMessageInboxAPIStore{privateMessageAPIStore: &privateMessageAPIStore{apiStore: base}}
	server := newAuthTestServer(store)

	for _, path := range []string{"/api/v2/messages?before=0", "/api/v2/messages?limit=0"} {
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodGet, path, ""))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, res.Code, res.Body.String())
		}
	}
	if store.inboxCalls != 0 {
		t.Fatalf("invalid inbox reads reached store: %d", store.inboxCalls)
	}

	store.inbox = []privatemessage.ConversationSummary{{
		Peer:          privatemessage.InboxPeer{ID: 77, Available: false},
		LatestMessage: privatemessage.LatestMessageSummary{ID: 9, SenderID: 77},
	}}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodGet, "/api/v2/messages?limit=5", ""))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"peer":{"id":77,"available":false}`) || strings.Contains(res.Body.String(), `"username"`) {
		t.Fatalf("unavailable status=%d body=%s", res.Code, res.Body.String())
	}

	store.inboxErr = errors.New("database unavailable")
	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, authenticatedRequest(base, http.MethodGet, "/api/v2/messages", ""))
	if res.Code != http.StatusInternalServerError || !strings.Contains(res.Body.String(), `"code":"internal"`) {
		t.Fatalf("internal status=%d body=%s", res.Code, res.Body.String())
	}
}
