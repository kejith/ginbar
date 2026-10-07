package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/privatemessage"
)

func TestPrivateMessageInboxConcurrentSendsKeepOneAuthoritativeLatestSummaryPerPeer(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	actorID := createPrivateMessageUser(t, store, "pm-inbox-race-actor", privatemessage.UserStatusActive)
	service := privatemessage.New(store)
	const peerCount = 4
	const sendsPerPeer = 5
	peers := make([]int64, peerCount)
	for i := range peers {
		peers[i] = createPrivateMessageUser(t, store, fmt.Sprintf("pm-inbox-race-peer-%d", i), privatemessage.UserStatusActive)
	}

	start := make(chan struct{})
	type result struct {
		peerID int64
		id     int64
		err    error
	}
	results := make(chan result, peerCount*sendsPerPeer)
	var wg sync.WaitGroup
	for _, peerID := range peers {
		for j := 0; j < sendsPerPeer; j++ {
			peerID := peerID
			j := j
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				sender, recipient := actorID, peerID
				if j%2 == 1 {
					sender, recipient = peerID, actorID
				}
				message, err := service.Send(ctx, privatemessage.SendRequest{SenderUserID: sender, RecipientUserID: recipient, Body: fmt.Sprintf("race-%d", j)})
				results <- result{peerID: peerID, id: message.ID, err: err}
			}()
		}
	}
	close(start)
	wg.Wait()
	close(results)

	latest := make(map[int64]int64, peerCount)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.id > latest[result.peerID] {
			latest[result.peerID] = result.id
		}
	}
	page, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Conversations) != peerCount {
		t.Fatalf("conversations=%#v", page.Conversations)
	}
	for i, summary := range page.Conversations {
		if latest[summary.Peer.ID] != summary.LatestMessage.ID {
			t.Fatalf("summary=%#v latest=%v", summary, latest)
		}
		if i > 0 && page.Conversations[i-1].LatestMessage.ID <= summary.LatestMessage.ID {
			t.Fatalf("not descending: %#v", page.Conversations)
		}
	}
	var durableRows int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM private_message_conversations
		WHERE user_low_id = $1 OR user_high_id = $1
	`, actorID).Scan(&durableRows); err != nil {
		t.Fatal(err)
	}
	if durableRows != peerCount {
		t.Fatalf("durable conversation rows=%d want=%d", durableRows, peerCount)
	}
}
