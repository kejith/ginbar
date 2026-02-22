package api

import (
	"context"
	"sort"
	"strconv"
	"time"

	dbgen "wallium/db/gen"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgtype"
)

// ── Shapes returned to the client ─────────────────────────────────────────────

// ConversationItem summarises a private thread with one partner.
type ConversationItem struct {
	Partner  string    `json:"partner"`
	LastAt   time.Time `json:"last_at"`
	Unread   int       `json:"unread"`
	LastBody string    `json:"last_body"`
}

// ── Forms ─────────────────────────────────────────────────────────────────────

type sendMessageForm struct {
	ToName  string `json:"to_name"  form:"to_name"`
	Body    string `json:"body"     form:"body"`
	Subject string `json:"subject"  form:"subject"`
}

type markReadForm struct {
	ID int32 `json:"id" form:"id"`
}

type broadcastForm struct {
	Body    string `json:"body"    form:"body"`
	Subject string `json:"subject" form:"subject"`
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// buildConversations groups a flat private-message list into per-partner summaries.
// Messages must be ordered by created_at DESC (most recent first).
func buildConversations(msgs []dbgen.Message, myName string) []ConversationItem {
	byPartner := map[string]*ConversationItem{}
	order := []string{}

	for _, m := range msgs {
		// Determine which side is "the other person".
		partner := m.ToName
		if m.ToName == myName {
			partner = m.FromName.String
		}
		if partner == "" {
			continue
		}

		c, ok := byPartner[partner]
		if !ok {
			c = &ConversationItem{Partner: partner}
			byPartner[partner] = c
			order = append(order, partner)
		}

		// Messages are DESC; first seen = most recent.
		if c.LastAt.IsZero() {
			c.LastAt = m.CreatedAt.Time
			c.LastBody = m.Body
		}

		// Count messages sent TO me that are still unread.
		if m.ToName == myName && !m.ReadAt.Valid {
			c.Unread++
		}
	}

	// Preserve creation order (already DESC by last_at because msgs are sorted).
	result := make([]ConversationItem, 0, len(order))
	for _, p := range order {
		result = append(result, *byPartner[p])
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastAt.After(result[j].LastAt)
	})
	return result
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// GET /api/message/unread — total unread count for the current user.
func (s *Server) GetUnreadCount(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return c.JSON(fiber.Map{"count": 0})
	}
	count, err := s.store.GetUnreadCount(c.Context(), u.Name)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"count": count})
}

// GET /api/message/inbox — conversations sidebar only (private messages).
func (s *Server) GetInbox(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	privMsgs, err := s.store.GetPrivateMessages(
		c.Context(),
		pgtype.Text{String: u.Name, Valid: true},
	)
	if err != nil {
		return err
	}

	conversations := buildConversations(privMsgs, u.Name)

	return c.JSON(fiber.Map{
		"conversations": conversations,
	})
}

// GET /api/message/notifications?page=1 — paginated enriched notifications.
func (s *Server) GetNotificationsPage(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	const pageSize = 20
	page := 1
	if p, err2 := strconv.Atoi(c.Query("page", "1")); err2 == nil && p > 0 {
		page = p
	}
	offset := int32((page - 1) * pageSize)

	rows, err := s.store.GetNotificationsEnriched(c.Context(), dbgen.GetNotificationsEnrichedParams{
		ToName: u.Name,
		Limit:  pageSize,
		Offset: offset,
	})
	if err != nil {
		return err
	}

	hasMore := len(rows) == pageSize
	return c.JSON(fiber.Map{
		"notifications": rows,
		"page":          page,
		"has_more":      hasMore,
	})
}

// GET /api/message/thread/:partner — full private thread with one partner.
func (s *Server) GetThread(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	partner := c.Params("partner")
	if partner == "" {
		return fiber.NewError(fiber.StatusBadRequest, "partner required")
	}

	msgs, err := s.store.GetThread(c.Context(), dbgen.GetThreadParams{
		FromName: pgtype.Text{String: u.Name, Valid: true},
		ToName:   partner,
	})
	if err != nil {
		return err
	}

	// Mark all incoming unread messages in this thread as read.
	for _, m := range msgs {
		if m.ToName == u.Name && !m.ReadAt.Valid {
			_ = s.store.MarkMessageRead(c.Context(), dbgen.MarkMessageReadParams{
				ID:     m.ID,
				ToName: u.Name,
			})
		}
	}

	return c.JSON(fiber.Map{"messages": msgs})
}

// POST /api/message/send — send a private message.
func (s *Server) SendMessage(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	form := new(sendMessageForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if form.ToName == "" || form.Body == "" {
		return fiber.NewError(fiber.StatusBadRequest, "to_name and body are required")
	}
	if form.ToName == u.Name {
		return fiber.NewError(fiber.StatusBadRequest, "cannot message yourself")
	}

	msg, err := s.store.CreateMessage(c.Context(), dbgen.CreateMessageParams{
		Kind:         "private",
		FromName:     pgtype.Text{String: u.Name, Valid: true},
		ToName:       form.ToName,
		Subject:      pgtype.Text{Valid: false},
		Body:         form.Body,
		RefPostID:    pgtype.Int4{Valid: false},
		RefCommentID: pgtype.Int4{Valid: false},
	})
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(msg)
}

// POST /api/message/mark-read — mark a single message as read.
func (s *Server) MarkMessageRead(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	form := new(markReadForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if err := s.store.MarkMessageRead(c.Context(), dbgen.MarkMessageReadParams{
		ID:     form.ID,
		ToName: u.Name,
	}); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusOK)
}

// POST /api/message/mark-all-read — mark every unread message as read.
func (s *Server) MarkAllRead(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	if err := s.store.MarkAllReadForUser(c.Context(), u.Name); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusOK)
}

// ── Admin: broadcast ──────────────────────────────────────────────────────────

// POST /api/admin/message/broadcast — send a system message to ALL users.
func (s *Server) BroadcastMessage(c fiber.Ctx) error {
	form := new(broadcastForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if form.Body == "" {
		return fiber.NewError(fiber.StatusBadRequest, "body required")
	}

	users, err := s.store.GetUsers(c.Context())
	if err != nil {
		return err
	}

	subject := pgtype.Text{Valid: false}
	if form.Subject != "" {
		subject = pgtype.Text{String: form.Subject, Valid: true}
	}

	var count int
	for _, user := range users {
		if _, err := s.store.CreateMessage(c.Context(), dbgen.CreateMessageParams{
			Kind:         "system",
			FromName:     pgtype.Text{Valid: false}, // NULL = system
			ToName:       user.Name,
			Subject:      subject,
			Body:         form.Body,
			RefPostID:    pgtype.Int4{Valid: false},
			RefCommentID: pgtype.Int4{Valid: false},
		}); err == nil {
			count++
		}
	}

	return c.JSON(fiber.Map{"sent": count})
}

// sendReplyNotification creates a 'notification' message to the author of the
// parent comment when someone replies to their comment. It is best-effort;
// errors are intentionally ignored so they don't fail the comment creation.
func (s *Server) sendReplyNotification(parentCommentAuthor, replierName, replySnippet string, postID, commentID int32) {
	if parentCommentAuthor == "" || parentCommentAuthor == replierName {
		return
	}
	body := "@" + replierName + " replied to your comment"
	if replySnippet != "" {
		if len(replySnippet) > 120 {
			replySnippet = replySnippet[:120] + "…"
		}
		body += ": \u201c" + replySnippet + "\u201d"
	}
	_, _ = s.store.CreateMessage(context.Background(), dbgen.CreateMessageParams{
		Kind:         "notification",
		FromName:     pgtype.Text{String: replierName, Valid: true},
		ToName:       parentCommentAuthor,
		Subject:      pgtype.Text{Valid: false},
		Body:         body,
		RefPostID:    pgtype.Int4{Int32: postID, Valid: postID > 0},
		RefCommentID: pgtype.Int4{Int32: commentID, Valid: commentID > 0},
	})
}
