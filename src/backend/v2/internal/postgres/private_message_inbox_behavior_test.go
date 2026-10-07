package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/privatemessage"
)

func TestPrivateMessageInboxSummariesArePairUniqueIsolatedCurrentAndAvailabilityAware(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	actorID := createPrivateMessageUser(t, store, "pm-inbox-actor", privatemessage.UserStatusActive)
	peerB := createPrivateMessageUser(t, store, "pm-inbox-b", privatemessage.UserStatusActive)
	peerC := createPrivateMessageUser(t, store, "pm-inbox-c", privatemessage.UserStatusActive)
	peerD := createPrivateMessageUser(t, store, "pm-inbox-d", privatemessage.UserStatusActive)
	unrelatedA := createPrivateMessageUser(t, store, "pm-inbox-unrelated-a", privatemessage.UserStatusActive)
	unrelatedB := createPrivateMessageUser(t, store, "pm-inbox-unrelated-b", privatemessage.UserStatusActive)
	service := privatemessage.New(store)

	empty, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Conversations) != 0 || empty.Conversations == nil {
		t.Fatalf("empty=%#v", empty)
	}

	b1 := sendPrivateMessageForTest(t, service, actorID, peerB, "b-1")
	_ = sendPrivateMessageForTest(t, service, actorID, peerC, "c-1")
	b2 := sendPrivateMessageForTest(t, service, peerB, actorID, "b-2")
	c2 := sendPrivateMessageForTest(t, service, peerC, actorID, "c-2")
	d1 := sendPrivateMessageForTest(t, service, peerD, actorID, "d-1")
	_ = sendPrivateMessageForTest(t, service, unrelatedA, unrelatedB, "noise")
	if !(b1.ID < b2.ID && b2.ID < c2.ID && c2.ID < d1.ID) {
		t.Fatalf("unexpected ids b1=%d b2=%d c2=%d d1=%d", b1.ID, b2.ID, c2.ID, d1.ID)
	}

	page, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Conversations) != 3 || page.NextBefore != 0 {
		t.Fatalf("page=%#v", page)
	}
	wantPeers := []int64{peerD, peerC, peerB}
	wantLatest := []int64{d1.ID, c2.ID, b2.ID}
	seen := map[int64]bool{}
	for i, summary := range page.Conversations {
		if summary.Peer.ID != wantPeers[i] || summary.LatestMessage.ID != wantLatest[i] {
			t.Fatalf("summary[%d]=%#v want peer/latest=%d/%d", i, summary, wantPeers[i], wantLatest[i])
		}
		if seen[summary.Peer.ID] {
			t.Fatalf("duplicate peer=%d", summary.Peer.ID)
		}
		seen[summary.Peer.ID] = true
		if !summary.Peer.Available || summary.Peer.Username == "" || summary.LatestMessage.CreatedAt.IsZero() {
			t.Fatalf("summary[%d]=%#v", i, summary)
		}
	}
	if seen[unrelatedA] || seen[unrelatedB] {
		t.Fatalf("unrelated conversation leaked: %#v", page.Conversations)
	}

	if _, err := store.pool.Exec(ctx, "UPDATE users SET username = 'pm-inbox-c-renamed' WHERE id = $1", peerC); err != nil {
		t.Fatal(err)
	}
	renamed, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if summary := findConversation(t, renamed.Conversations, peerC); summary.Peer.Username != "pm-inbox-c-renamed" {
		t.Fatalf("current peer metadata not joined: %#v", summary)
	}

	if _, err := store.pool.Exec(ctx, "UPDATE users SET status = 1 WHERE id = $1", peerB); err != nil {
		t.Fatal(err)
	}
	unavailable, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	bSummary := findConversation(t, unavailable.Conversations, peerB)
	if bSummary.Peer.Available || bSummary.Peer.Username != "" || bSummary.Peer.ID != peerB {
		t.Fatalf("inactive peer summary=%#v", bSummary)
	}
	if _, err := service.List(ctx, privatemessage.Query{ActorUserID: actorID, PeerUserID: peerB}); !errors.Is(err, privatemessage.ErrRecipientUnavailable) {
		t.Fatalf("inactive direct thread error=%v", err)
	}
	if _, err := store.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", peerB); err == nil {
		t.Fatal("peer with durable messages/conversation unexpectedly became missing")
	}
}

func TestPrivateMessageInboxCursorIsUniqueNonOverlappingAndFreshReadsReorder(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	actorID := createPrivateMessageUser(t, store, "pm-inbox-cursor-actor", privatemessage.UserStatusActive)
	service := privatemessage.New(store)
	peers := make([]int64, 5)
	for i := range peers {
		peers[i] = createPrivateMessageUser(t, store, fmt.Sprintf("pm-inbox-cursor-peer-%d", i), privatemessage.UserStatusActive)
		sendPrivateMessageForTest(t, service, actorID, peers[i], fmt.Sprintf("seed-%d", i))
	}

	first, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Conversations) != 2 || first.NextBefore == 0 {
		t.Fatalf("first=%#v", first)
	}
	second, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Before: first.NextBefore, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Conversations) != 2 || second.NextBefore == 0 {
		t.Fatalf("second=%#v", second)
	}
	last, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Before: second.NextBefore, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Conversations) != 1 || last.NextBefore != 0 {
		t.Fatalf("last=%#v", last)
	}
	seen := map[int64]bool{}
	for _, page := range []privatemessage.InboxPage{first, second, last} {
		for i := range page.Conversations {
			summary := page.Conversations[i]
			if seen[summary.Peer.ID] {
				t.Fatalf("cursor overlap peer=%d", summary.Peer.ID)
			}
			seen[summary.Peer.ID] = true
			if i > 0 && page.Conversations[i-1].LatestMessage.ID <= summary.LatestMessage.ID {
				t.Fatalf("page not descending: %#v", page.Conversations)
			}
		}
	}
	if len(seen) != len(peers) {
		t.Fatalf("unique peers=%d want=%d", len(seen), len(peers))
	}

	stableFirst, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(stableFirst.Conversations) != 1 || stableFirst.NextBefore == 0 {
		t.Fatalf("stable first=%#v", stableFirst)
	}
	oldTopPeer := stableFirst.Conversations[0].Peer.ID
	oldCursor := stableFirst.NextBefore
	oldestPeer := peers[0]
	updated := sendPrivateMessageForTest(t, service, oldestPeer, actorID, "newest")

	fresh, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Conversations) != 1 || fresh.Conversations[0].Peer.ID != oldestPeer || fresh.Conversations[0].LatestMessage.ID != updated.ID || updated.ID <= oldCursor {
		t.Fatalf("fresh=%#v updated=%#v", fresh, updated)
	}
	continued, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Before: oldCursor, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range continued.Conversations {
		if summary.Peer.ID == oldestPeer || summary.Peer.ID == oldTopPeer {
			t.Fatalf("non-snapshot continuation duplicated/moved conversation: %#v", continued)
		}
		if summary.LatestMessage.ID >= oldCursor {
			t.Fatalf("cursor violation summary=%#v cursor=%d", summary, oldCursor)
		}
	}
}

func sendPrivateMessageForTest(t *testing.T, service *privatemessage.Service, senderID, recipientID int64, body string) privatemessage.Message {
	t.Helper()
	message, err := service.Send(context.Background(), privatemessage.SendRequest{SenderUserID: senderID, RecipientUserID: recipientID, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func findConversation(t *testing.T, summaries []privatemessage.ConversationSummary, peerID int64) privatemessage.ConversationSummary {
	t.Helper()
	for _, summary := range summaries {
		if summary.Peer.ID == peerID {
			return summary
		}
	}
	t.Fatalf("peer %d not found in %#v", peerID, summaries)
	return privatemessage.ConversationSummary{}
}
