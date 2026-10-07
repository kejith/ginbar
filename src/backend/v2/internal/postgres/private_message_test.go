package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/privatemessage"
)

func TestPrivateMessageSendUsesNumericIdentityAndReturnsAuthoritativeRow(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	senderID := createPrivateMessageUser(t, store, "pm-sender", privatemessage.UserStatusActive)
	recipientID := createPrivateMessageUser(t, store, "pm-recipient", privatemessage.UserStatusActive)
	inactiveID := createPrivateMessageUser(t, store, "pm-inactive", 1)
	service := privatemessage.New(store)

	created, err := service.Send(ctx, privatemessage.SendRequest{
		SenderUserID:    senderID,
		RecipientUserID: recipientID,
		Body:            "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID <= 0 || created.SenderID != senderID || created.RecipientID != recipientID || created.Body != "hello" || created.CreatedAt.IsZero() {
		t.Fatalf("created=%#v", created)
	}

	var stored privatemessage.Message
	if err := store.pool.QueryRow(ctx, `
		SELECT id, sender_user_id, recipient_user_id, body, created_at
		FROM private_messages
		WHERE id = $1
	`, created.ID).Scan(&stored.ID, &stored.SenderID, &stored.RecipientID, &stored.Body, &stored.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if stored.ID != created.ID || stored.SenderID != senderID || stored.RecipientID != recipientID || stored.Body != created.Body || !stored.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created=%#v stored=%#v", created, stored)
	}

	for _, unavailableID := range []int64{inactiveID, inactiveID + 999999} {
		if _, err := service.Send(ctx, privatemessage.SendRequest{SenderUserID: senderID, RecipientUserID: unavailableID, Body: "blocked"}); !errors.Is(err, privatemessage.ErrRecipientUnavailable) {
			t.Fatalf("recipient=%d error=%v", unavailableID, err)
		}
	}
	var count int
	if err := store.pool.QueryRow(ctx, "SELECT count(*) FROM private_messages").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("message rows=%d", count)
	}
}

func TestPrivateMessageThreadIsPairScopedDescendingAndCursorUnique(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	userA := createPrivateMessageUser(t, store, "pm-thread-a", privatemessage.UserStatusActive)
	userB := createPrivateMessageUser(t, store, "pm-thread-b", privatemessage.UserStatusActive)
	userC := createPrivateMessageUser(t, store, "pm-thread-c", privatemessage.UserStatusActive)
	service := privatemessage.New(store)

	var abIDs []int64
	for _, request := range []privatemessage.SendRequest{
		{SenderUserID: userA, RecipientUserID: userB, Body: "ab-1"},
		{SenderUserID: userA, RecipientUserID: userC, Body: "ac-noise"},
		{SenderUserID: userB, RecipientUserID: userA, Body: "ba-2"},
		{SenderUserID: userB, RecipientUserID: userC, Body: "bc-noise"},
		{SenderUserID: userA, RecipientUserID: userB, Body: "ab-3"},
		{SenderUserID: userB, RecipientUserID: userA, Body: "ba-4"},
		{SenderUserID: userA, RecipientUserID: userB, Body: "ab-5"},
	} {
		created, err := service.Send(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if (request.SenderUserID == userA && request.RecipientUserID == userB) || (request.SenderUserID == userB && request.RecipientUserID == userA) {
			abIDs = append(abIDs, created.ID)
		}
	}

	first, err := service.List(ctx, privatemessage.Query{ActorUserID: userA, PeerUserID: userB, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Messages) != 2 || first.Messages[0].ID != abIDs[4] || first.Messages[1].ID != abIDs[3] || first.NextBefore != abIDs[3] {
		t.Fatalf("first=%#v ids=%v", first, abIDs)
	}
	for _, message := range first.Messages {
		assertPrivateMessagePair(t, message, userA, userB)
	}

	second, err := service.List(ctx, privatemessage.Query{ActorUserID: userA, PeerUserID: userB, Before: first.NextBefore, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Messages) != 2 || second.Messages[0].ID != abIDs[2] || second.Messages[1].ID != abIDs[1] || second.NextBefore != abIDs[1] {
		t.Fatalf("second=%#v ids=%v", second, abIDs)
	}
	for _, message := range second.Messages {
		assertPrivateMessagePair(t, message, userA, userB)
		if message.ID == first.Messages[0].ID || message.ID == first.Messages[1].ID {
			t.Fatalf("overlapping message id=%d", message.ID)
		}
	}

	last, err := service.List(ctx, privatemessage.Query{ActorUserID: userA, PeerUserID: userB, Before: second.NextBefore, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Messages) != 1 || last.Messages[0].ID != abIDs[0] || last.NextBefore != 0 {
		t.Fatalf("last=%#v ids=%v", last, abIDs)
	}

	reverse, err := service.List(ctx, privatemessage.Query{ActorUserID: userB, PeerUserID: userA, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(reverse.Messages) != len(abIDs) {
		t.Fatalf("reverse messages=%#v", reverse.Messages)
	}
	for i, message := range reverse.Messages {
		if message.ID != abIDs[len(abIDs)-1-i] {
			t.Fatalf("reverse[%d]=%d ids=%v", i, message.ID, abIDs)
		}
		assertPrivateMessagePair(t, message, userA, userB)
	}
}

func TestPrivateMessageThreadRejectsMissingInactiveAndSelfPeers(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	actorID := createPrivateMessageUser(t, store, "pm-read-actor", privatemessage.UserStatusActive)
	inactiveID := createPrivateMessageUser(t, store, "pm-read-inactive", 1)
	service := privatemessage.New(store)

	for _, peerID := range []int64{inactiveID, inactiveID + 999999} {
		if _, err := service.List(ctx, privatemessage.Query{ActorUserID: actorID, PeerUserID: peerID}); !errors.Is(err, privatemessage.ErrRecipientUnavailable) {
			t.Fatalf("peer=%d error=%v", peerID, err)
		}
	}
	if _, err := service.List(ctx, privatemessage.Query{ActorUserID: actorID, PeerUserID: actorID}); !errors.Is(err, privatemessage.ErrSelfMessage) {
		t.Fatalf("self thread error=%v", err)
	}
}

func TestPrivateMessageConcurrentIdenticalSendsCreateDistinctRows(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	senderID := createPrivateMessageUser(t, store, "pm-concurrent-sender", privatemessage.UserStatusActive)
	recipientID := createPrivateMessageUser(t, store, "pm-concurrent-recipient", privatemessage.UserStatusActive)
	service := privatemessage.New(store)

	const requestCount = 12
	start := make(chan struct{})
	results := make(chan struct {
		message privatemessage.Message
		err     error
	}, requestCount)
	var wg sync.WaitGroup
	for range requestCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			message, err := service.Send(ctx, privatemessage.SendRequest{SenderUserID: senderID, RecipientUserID: recipientID, Body: "identical"})
			results <- struct {
				message privatemessage.Message
				err     error
			}{message: message, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	ids := make(map[int64]struct{}, requestCount)
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.message.SenderID != senderID || result.message.RecipientID != recipientID || result.message.Body != "identical" {
			t.Fatalf("message=%#v", result.message)
		}
		ids[result.message.ID] = struct{}{}
	}
	if len(ids) != requestCount {
		t.Fatalf("distinct ids=%d want=%d", len(ids), requestCount)
	}
	var count int
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM private_messages
		WHERE sender_user_id = $1 AND recipient_user_id = $2 AND body = 'identical'
	`, senderID, recipientID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != requestCount {
		t.Fatalf("durable rows=%d want=%d", count, requestCount)
	}
}

func TestPrivateMessageSQLPlansAreBoundedAndIndexBacked(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	actorID := createPrivateMessageUser(t, store, "pm-plan-actor", privatemessage.UserStatusActive)
	peerID := createPrivateMessageUser(t, store, "pm-plan-peer", privatemessage.UserStatusActive)
	noiseA := createPrivateMessageUser(t, store, "pm-plan-noise-a", privatemessage.UserStatusActive)
	noiseB := createPrivateMessageUser(t, store, "pm-plan-noise-b", privatemessage.UserStatusActive)

	if _, err := store.pool.Exec(ctx, `
		INSERT INTO users (username)
		SELECT 'pm-plan-user-' || g::text
		FROM generate_series(1, 6000) AS g
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO private_messages (sender_user_id, recipient_user_id, body)
		SELECT
			CASE WHEN g % 2 = 0 THEN $1::bigint ELSE $2::bigint END,
			CASE WHEN g % 2 = 0 THEN $2::bigint ELSE $1::bigint END,
			'target-' || g::text
		FROM generate_series(1, 5000) AS g
	`, actorID, peerID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO private_messages (sender_user_id, recipient_user_id, body)
		SELECT
			CASE WHEN g % 2 = 0 THEN $1::bigint ELSE $2::bigint END,
			CASE WHEN g % 2 = 0 THEN $2::bigint ELSE $1::bigint END,
			'noise-' || g::text
		FROM generate_series(1, 20000) AS g
	`, noiseA, noiseB); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, "ANALYZE users, private_messages"); err != nil {
		t.Fatal(err)
	}

	firstPlan := explainPlan(t, store, "EXPLAIN (ANALYZE, BUFFERS) "+listPrivateMessagesFirstSQL, actorID, peerID, privatemessage.DefaultLimit+1, privatemessage.UserStatusActive)
	t.Logf("private message first-page plan:\n%s", firstPlan)
	assertPlanContains(t, firstPlan, "users_pkey")
	assertPlanContains(t, firstPlan, "private_messages_thread_idx")
	assertPlanExcludes(t, firstPlan, "Seq Scan on users", "Seq Scan on private_messages", "external merge", "Disk:")

	var before int64
	if err := store.pool.QueryRow(ctx, `
		SELECT min(id) + 2500
		FROM private_messages
		WHERE LEAST(sender_user_id, recipient_user_id) = LEAST($1::bigint, $2::bigint)
		  AND GREATEST(sender_user_id, recipient_user_id) = GREATEST($1::bigint, $2::bigint)
	`, actorID, peerID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	cursorPlan := explainPlan(t, store, "EXPLAIN (ANALYZE, BUFFERS) "+listPrivateMessagesBeforeSQL, actorID, peerID, before, privatemessage.DefaultLimit+1, privatemessage.UserStatusActive)
	t.Logf("private message cursor plan:\n%s", cursorPlan)
	assertPlanContains(t, cursorPlan, "users_pkey")
	assertPlanContains(t, cursorPlan, "private_messages_thread_idx")
	assertPlanExcludes(t, cursorPlan, "Seq Scan on users", "Seq Scan on private_messages", "external merge", "Disk:")

	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+sendPrivateMessageSQL, actorID, peerID, "plan-send", privatemessage.UserStatusActive)
	if err != nil {
		t.Fatal(err)
	}
	sendPlan := collectPlan(t, rows)
	t.Logf("private message send plan:\n%s", sendPlan)
	assertPlanContains(t, sendPlan, "users_pkey")
	assertPlanExcludes(t, sendPlan, "Seq Scan on users", "external merge", "Disk:")

	for name, sql := range map[string]string{
		"first":  listPrivateMessagesFirstSQL,
		"cursor": listPrivateMessagesBeforeSQL,
		"send":   sendPrivateMessageSQL,
	} {
		if strings.Contains(strings.ToUpper(sql), "OFFSET") {
			t.Fatalf("%s SQL must not use OFFSET", name)
		}
	}
}

func createPrivateMessageUser(t *testing.T, store *Store, username string, status int16) int64 {
	t.Helper()
	var userID int64
	if err := store.pool.QueryRow(context.Background(), `
		INSERT INTO users (username, status)
		VALUES ($1, $2)
		RETURNING id
	`, username, status).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return userID
}

func assertPrivateMessagePair(t *testing.T, message privatemessage.Message, userA, userB int64) {
	t.Helper()
	if !((message.SenderID == userA && message.RecipientID == userB) || (message.SenderID == userB && message.RecipientID == userA)) {
		t.Fatalf("message leaked from unrelated pair: %#v", message)
	}
}

func TestPrivateMessageSchemaRejectsSelfAndOversizedRowsDefensively(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()
	userID := createPrivateMessageUser(t, store, "pm-schema-user", privatemessage.UserStatusActive)
	otherID := createPrivateMessageUser(t, store, "pm-schema-other", privatemessage.UserStatusActive)

	for _, args := range [][]any{
		{userID, userID, "self"},
		{userID, otherID, strings.Repeat("x", privatemessage.MaxBodyCharacters+1)},
	} {
		_, err := store.pool.Exec(ctx, `
			INSERT INTO private_messages (sender_user_id, recipient_user_id, body)
			VALUES ($1, $2, $3)
		`, args...)
		if err == nil {
			t.Fatalf("defensive constraint unexpectedly accepted args=%v", args[:2])
		}
	}
}
