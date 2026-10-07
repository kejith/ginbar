package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/privatemessage"
)

func TestPrivateMessageInboxSQLPlansAreBoundedAndIndexBacked(t *testing.T) {
	store, cleanup := testAuthStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := store.pool.Exec(ctx, `
		INSERT INTO users (username)
		SELECT 'pm-inbox-plan-low-' || g::text
		FROM generate_series(1, 200) AS g
	`); err != nil {
		t.Fatal(err)
	}
	actorID := createPrivateMessageUser(t, store, "pm-inbox-plan-actor", privatemessage.UserStatusActive)
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO users (username)
		SELECT 'pm-inbox-plan-high-' || g::text
		FROM generate_series(1, 200) AS g
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO users (username)
		SELECT 'pm-inbox-plan-noise-' || g::text
		FROM generate_series(1, 6000) AS g
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := store.pool.Exec(ctx, `
		WITH peers AS (
			SELECT id
			FROM users
			WHERE username LIKE 'pm-inbox-plan-low-%'
			   OR username LIKE 'pm-inbox-plan-high-%'
		), traffic AS (
			SELECT id AS peer_id, g
			FROM peers
			CROSS JOIN generate_series(1, 20) AS g
		)
		INSERT INTO private_messages (sender_user_id, recipient_user_id, body)
		SELECT
			CASE WHEN g % 2 = 0 THEN $1::bigint ELSE peer_id END,
			CASE WHEN g % 2 = 0 THEN peer_id ELSE $1::bigint END,
			'actor-' || peer_id::text || '-' || g::text
		FROM traffic
	`, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		WITH numbered AS (
			SELECT id, row_number() OVER (ORDER BY id) AS rn
			FROM users
			WHERE username LIKE 'pm-inbox-plan-noise-%'
		), pairs AS (
			SELECT a.id AS a_id, b.id AS b_id
			FROM numbered AS a
			JOIN numbered AS b ON b.rn = a.rn + 1
			WHERE a.rn % 2 = 1
		), traffic AS (
			SELECT a_id, b_id, g
			FROM pairs
			CROSS JOIN generate_series(1, 5) AS g
		)
		INSERT INTO private_messages (sender_user_id, recipient_user_id, body)
		SELECT
			CASE WHEN g % 2 = 0 THEN a_id ELSE b_id END,
			CASE WHEN g % 2 = 0 THEN b_id ELSE a_id END,
			'noise-' || a_id::text || '-' || g::text
		FROM traffic
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO private_message_conversations (user_low_id, user_high_id, latest_message_id)
		SELECT
			LEAST(sender_user_id, recipient_user_id),
			GREATEST(sender_user_id, recipient_user_id),
			max(id)
		FROM private_messages
		GROUP BY
			LEAST(sender_user_id, recipient_user_id),
			GREATEST(sender_user_id, recipient_user_id)
		ON CONFLICT (user_low_id, user_high_id) DO UPDATE
		SET latest_message_id = EXCLUDED.latest_message_id
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, "ANALYZE users, private_messages, private_message_conversations"); err != nil {
		t.Fatal(err)
	}

	firstPlan := explainPlan(t, store, "EXPLAIN (ANALYZE, BUFFERS) "+listPrivateMessageInboxFirstSQL, actorID, privatemessage.DefaultLimit+1, privatemessage.UserStatusActive)
	t.Logf("private message inbox first-page plan:\n%s", firstPlan)
	assertPlanContains(t, firstPlan, "private_message_conversations_low_latest_idx")
	assertPlanContains(t, firstPlan, "private_message_conversations_high_latest_idx")
	assertPlanContains(t, firstPlan, "private_messages_pkey")
	assertPlanContains(t, firstPlan, "users_pkey")
	assertPlanExcludes(t, firstPlan, "Seq Scan on private_message_conversations", "Seq Scan on private_messages", "Seq Scan on users", "external merge", "Disk:")

	service := privatemessage.New(store)
	first, err := service.Inbox(ctx, privatemessage.InboxQuery{ActorUserID: actorID, Limit: privatemessage.DefaultLimit})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextBefore == 0 {
		t.Fatalf("expected cursor from plan fixture: %#v", first)
	}
	cursorPlan := explainPlan(t, store, "EXPLAIN (ANALYZE, BUFFERS) "+listPrivateMessageInboxBeforeSQL, actorID, first.NextBefore, privatemessage.DefaultLimit+1, privatemessage.UserStatusActive)
	t.Logf("private message inbox cursor plan:\n%s", cursorPlan)
	assertPlanContains(t, cursorPlan, "private_message_conversations_low_latest_idx")
	assertPlanContains(t, cursorPlan, "private_message_conversations_high_latest_idx")
	assertPlanContains(t, cursorPlan, "private_messages_pkey")
	assertPlanContains(t, cursorPlan, "users_pkey")
	assertPlanExcludes(t, cursorPlan, "Seq Scan on private_message_conversations", "Seq Scan on private_messages", "Seq Scan on users", "external merge", "Disk:")

	var peerID int64
	if err := store.pool.QueryRow(ctx, "SELECT id FROM users WHERE username = 'pm-inbox-plan-high-1'").Scan(&peerID); err != nil {
		t.Fatal(err)
	}
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
	t.Logf("private message inbox-aware send plan:\n%s", sendPlan)
	assertPlanContains(t, sendPlan, "users_pkey")
	assertPlanExcludes(t, sendPlan, "Seq Scan on users", "Seq Scan on private_messages", "Seq Scan on private_message_conversations", "external merge", "Disk:")

	for name, sql := range map[string]string{
		"inbox-first":  listPrivateMessageInboxFirstSQL,
		"inbox-cursor": listPrivateMessageInboxBeforeSQL,
		"send":         sendPrivateMessageSQL,
	} {
		if strings.Contains(strings.ToUpper(sql), "OFFSET") {
			t.Fatalf("%s SQL must not use OFFSET", name)
		}
	}
}
