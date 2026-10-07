package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kejith/ginbar/backend/v2/internal/privatemessage"
)

const sendPrivateMessageSQL = `
	INSERT INTO private_messages (sender_user_id, recipient_user_id, body)
	SELECT $1, recipient.id, $3
	FROM users AS recipient
	WHERE recipient.id = $2
	  AND recipient.status = $4
	RETURNING id, sender_user_id, recipient_user_id, body, created_at
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
