package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kejith/ginbar/backend/v2/internal/privatemessage"
)

const sendPrivateMessageSQL = `
	WITH inserted AS (
		INSERT INTO private_messages (sender_user_id, recipient_user_id, body)
		SELECT $1, recipient.id, $3
		FROM users AS recipient
		WHERE recipient.id = $2
		  AND recipient.status = $4
		RETURNING id, sender_user_id, recipient_user_id, body, created_at
	), updated_conversation AS (
		INSERT INTO private_message_conversations (user_low_id, user_high_id, latest_message_id)
		SELECT
			LEAST(sender_user_id, recipient_user_id),
			GREATEST(sender_user_id, recipient_user_id),
			id
		FROM inserted
		ON CONFLICT (user_low_id, user_high_id) DO UPDATE
		SET latest_message_id = GREATEST(
			private_message_conversations.latest_message_id,
			EXCLUDED.latest_message_id
		)
		RETURNING 1
	)
	SELECT inserted.id, inserted.sender_user_id, inserted.recipient_user_id, inserted.body, inserted.created_at
	FROM inserted
	CROSS JOIN updated_conversation
`

const listPrivateMessagesFirstSQL = `
	SELECT
		COALESCE(message.id, 0),
		COALESCE(message.sender_user_id, 0),
		COALESCE(message.recipient_user_id, 0),
		COALESCE(message.body, ''),
		COALESCE(message.created_at, 'epoch'::timestamptz)
	FROM users AS peer
	LEFT JOIN LATERAL (
		SELECT id, sender_user_id, recipient_user_id, body, created_at
		FROM (
			(
				SELECT id, sender_user_id, recipient_user_id, body, created_at
				FROM private_messages
				WHERE sender_user_id = $1
				  AND recipient_user_id = $2
				ORDER BY id DESC
				LIMIT $3
			)
			UNION ALL
			(
				SELECT id, sender_user_id, recipient_user_id, body, created_at
				FROM private_messages
				WHERE sender_user_id = $2
				  AND recipient_user_id = $1
				ORDER BY id DESC
				LIMIT $3
			)
		) AS directional
		ORDER BY id DESC
		LIMIT $3
	) AS message ON true
	WHERE peer.id = $2
	  AND peer.status = $4
	ORDER BY message.id DESC NULLS LAST
`

const listPrivateMessagesBeforeSQL = `
	SELECT
		COALESCE(message.id, 0),
		COALESCE(message.sender_user_id, 0),
		COALESCE(message.recipient_user_id, 0),
		COALESCE(message.body, ''),
		COALESCE(message.created_at, 'epoch'::timestamptz)
	FROM users AS peer
	LEFT JOIN LATERAL (
		SELECT id, sender_user_id, recipient_user_id, body, created_at
		FROM (
			(
				SELECT id, sender_user_id, recipient_user_id, body, created_at
				FROM private_messages
				WHERE sender_user_id = $1
				  AND recipient_user_id = $2
				  AND id < $3
				ORDER BY id DESC
				LIMIT $4
			)
			UNION ALL
			(
				SELECT id, sender_user_id, recipient_user_id, body, created_at
				FROM private_messages
				WHERE sender_user_id = $2
				  AND recipient_user_id = $1
				  AND id < $3
				ORDER BY id DESC
				LIMIT $4
			)
		) AS directional
		ORDER BY id DESC
		LIMIT $4
	) AS message ON true
	WHERE peer.id = $2
	  AND peer.status = $5
	ORDER BY message.id DESC NULLS LAST
`

const listPrivateMessageInboxFirstSQL = `
	WITH candidates AS (
		(
			SELECT user_high_id AS peer_user_id, latest_message_id
			FROM private_message_conversations
			WHERE user_low_id = $1
			ORDER BY latest_message_id DESC
			LIMIT $2
		)
		UNION ALL
		(
			SELECT user_low_id AS peer_user_id, latest_message_id
			FROM private_message_conversations
			WHERE user_high_id = $1
			ORDER BY latest_message_id DESC
			LIMIT $2
		)
	), page AS (
		SELECT peer_user_id, latest_message_id
		FROM candidates
		ORDER BY latest_message_id DESC
		LIMIT $2
	)
	SELECT
		page.peer_user_id,
		CASE WHEN peer.status = $3 THEN peer.username ELSE '' END,
		COALESCE(peer.status = $3, false),
		latest.id,
		latest.sender_user_id,
		latest.created_at
	FROM page
	JOIN private_messages AS latest ON latest.id = page.latest_message_id
	LEFT JOIN users AS peer ON peer.id = page.peer_user_id
	ORDER BY page.latest_message_id DESC
`

const listPrivateMessageInboxBeforeSQL = `
	WITH candidates AS (
		(
			SELECT user_high_id AS peer_user_id, latest_message_id
			FROM private_message_conversations
			WHERE user_low_id = $1
			  AND latest_message_id < $2
			ORDER BY latest_message_id DESC
			LIMIT $3
		)
		UNION ALL
		(
			SELECT user_low_id AS peer_user_id, latest_message_id
			FROM private_message_conversations
			WHERE user_high_id = $1
			  AND latest_message_id < $2
			ORDER BY latest_message_id DESC
			LIMIT $3
		)
	), page AS (
		SELECT peer_user_id, latest_message_id
		FROM candidates
		ORDER BY latest_message_id DESC
		LIMIT $3
	)
	SELECT
		page.peer_user_id,
		CASE WHEN peer.status = $4 THEN peer.username ELSE '' END,
		COALESCE(peer.status = $4, false),
		latest.id,
		latest.sender_user_id,
		latest.created_at
	FROM page
	JOIN private_messages AS latest ON latest.id = page.latest_message_id
	LEFT JOIN users AS peer ON peer.id = page.peer_user_id
	ORDER BY page.latest_message_id DESC
`

func (s *Store) SendPrivateMessage(ctx context.Context, request privatemessage.SendRequest) (privatemessage.Message, error) {
	var message privatemessage.Message
	err := s.pool.QueryRow(
		ctx,
		sendPrivateMessageSQL,
		request.SenderUserID,
		request.RecipientUserID,
		request.Body,
		privatemessage.UserStatusActive,
	).Scan(
		&message.ID,
		&message.SenderID,
		&message.RecipientID,
		&message.Body,
		&message.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return privatemessage.Message{}, privatemessage.ErrRecipientUnavailable
	}
	if err != nil {
		return privatemessage.Message{}, fmt.Errorf("send private message: %w", err)
	}
	return message, nil
}

func (s *Store) ListPrivateMessages(ctx context.Context, query privatemessage.Query) ([]privatemessage.Message, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if query.Before == 0 {
		rows, err = s.pool.Query(
			ctx,
			listPrivateMessagesFirstSQL,
			query.ActorUserID,
			query.PeerUserID,
			query.Limit+1,
			privatemessage.UserStatusActive,
		)
	} else {
		rows, err = s.pool.Query(
			ctx,
			listPrivateMessagesBeforeSQL,
			query.ActorUserID,
			query.PeerUserID,
			query.Before,
			query.Limit+1,
			privatemessage.UserStatusActive,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query private messages: %w", err)
	}
	defer rows.Close()

	messages := make([]privatemessage.Message, 0, query.Limit+1)
	peerFound := false
	for rows.Next() {
		peerFound = true
		var message privatemessage.Message
		if err := rows.Scan(
			&message.ID,
			&message.SenderID,
			&message.RecipientID,
			&message.Body,
			&message.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan private message: %w", err)
		}
		if message.ID != 0 {
			messages = append(messages, message)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read private messages: %w", err)
	}
	if !peerFound {
		return nil, privatemessage.ErrRecipientUnavailable
	}
	return messages, nil
}

func (s *Store) ListPrivateMessageInbox(ctx context.Context, query privatemessage.InboxQuery) ([]privatemessage.ConversationSummary, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if query.Before == 0 {
		rows, err = s.pool.Query(
			ctx,
			listPrivateMessageInboxFirstSQL,
			query.ActorUserID,
			query.Limit+1,
			privatemessage.UserStatusActive,
		)
	} else {
		rows, err = s.pool.Query(
			ctx,
			listPrivateMessageInboxBeforeSQL,
			query.ActorUserID,
			query.Before,
			query.Limit+1,
			privatemessage.UserStatusActive,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("query private message inbox: %w", err)
	}
	defer rows.Close()

	conversations := make([]privatemessage.ConversationSummary, 0, query.Limit+1)
	for rows.Next() {
		var summary privatemessage.ConversationSummary
		if err := rows.Scan(
			&summary.Peer.ID,
			&summary.Peer.Username,
			&summary.Peer.Available,
			&summary.LatestMessage.ID,
			&summary.LatestMessage.SenderID,
			&summary.LatestMessage.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan private message inbox: %w", err)
		}
		conversations = append(conversations, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read private message inbox: %w", err)
	}
	return conversations, nil
}
