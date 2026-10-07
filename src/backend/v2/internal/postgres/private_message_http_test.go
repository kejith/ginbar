package postgres

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/privatemessage"
)

func TestHTTPPrivateMessageCrossOriginHasNoDurableWriteAndSameOriginUsesSessionIdentity(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	senderID := createPrivateMessageUser(t, store, "pm-http-sender", privatemessage.UserStatusActive)
	recipientID := createPrivateMessageUser(t, store, "pm-http-recipient", privatemessage.UserStatusActive)
	server, cookie := regenerationAuthenticatedServer(t, store, senderID, 0x71)
	path := "/api/v2/messages/" + strconv.FormatInt(recipientID, 10)

	crossOrigin := httptest.NewRequest(http.MethodPost, "http://ginbar.test"+path, strings.NewReader(`{"body":"blocked"}`))
	crossOrigin.Header.Set("Origin", "https://evil.test")
	crossOrigin.AddCookie(&http.Cookie{Name: "ginbar_session", Value: cookie})
	crossResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(crossResponse, crossOrigin)
	if crossResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d body=%s", crossResponse.Code, crossResponse.Body.String())
	}
	var count int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM private_messages").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cross-origin request created %d messages", count)
	}

	sameOrigin := httptest.NewRequest(http.MethodPost, "http://ginbar.test"+path, strings.NewReader(`{"body":"hello"}`))
	sameOrigin.Header.Set("Origin", "http://ginbar.test")
	sameOrigin.AddCookie(&http.Cookie{Name: "ginbar_session", Value: cookie})
	sameResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(sameResponse, sameOrigin)
	if sameResponse.Code != http.StatusCreated {
		t.Fatalf("same-origin status=%d body=%s", sameResponse.Code, sameResponse.Body.String())
	}
	var sent privatemessage.Message
	if err := json.Unmarshal(sameResponse.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.SenderID != senderID || sent.RecipientID != recipientID || sent.Body != "hello" || sent.ID <= 0 {
		t.Fatalf("sent=%#v", sent)
	}

	var storedSender, storedRecipient int64
	if err := store.pool.QueryRow(ctx, `
		SELECT sender_user_id, recipient_user_id
		FROM private_messages
		WHERE id = $1
	`, sent.ID).Scan(&storedSender, &storedRecipient); err != nil {
		t.Fatal(err)
	}
	if storedSender != senderID || storedRecipient != recipientID {
		t.Fatalf("stored sender/recipient=%d/%d", storedSender, storedRecipient)
	}

	read := httptest.NewRequest(http.MethodGet, "http://ginbar.test"+path+"?limit=10", nil)
	read.AddCookie(&http.Cookie{Name: "ginbar_session", Value: cookie})
	readResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("read status=%d body=%s", readResponse.Code, readResponse.Body.String())
	}
	var page privatemessage.Page
	if err := json.Unmarshal(readResponse.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Messages[0].ID != sent.ID || page.NextBefore != 0 {
		t.Fatalf("page=%#v", page)
	}
}
