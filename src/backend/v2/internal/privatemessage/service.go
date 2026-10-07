package privatemessage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultLimit      = 50
	MaxLimit          = 100
	MaxBodyCharacters = 10000
	UserStatusActive  = int16(0)
)

var (
	ErrRecipientUnavailable = errors.New("recipient unavailable")
	ErrInvalidBody          = errors.New("invalid message body")
	ErrSelfMessage          = errors.New("self message forbidden")
)

type Message struct {
	ID          int64     `json:"id"`
	SenderID    int64     `json:"senderId"`
	RecipientID int64     `json:"recipientId"`
	Body        string    `json:"body"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Query struct {
	ActorUserID int64
	PeerUserID  int64
	Before      int64
	Limit       int
}

type Page struct {
	Messages   []Message `json:"messages"`
	NextBefore int64     `json:"nextBefore,omitempty"`
}

type InboxQuery struct {
	ActorUserID int64
	Before      int64
	Limit       int
}

type InboxPeer struct {
	ID        int64  `json:"id"`
	Username  string `json:"username,omitempty"`
	Available bool   `json:"available"`
}

type LatestMessageSummary struct {
	ID        int64     `json:"id"`
	SenderID  int64     `json:"senderId"`
	CreatedAt time.Time `json:"createdAt"`
}

type ConversationSummary struct {
	Peer          InboxPeer            `json:"peer"`
	LatestMessage LatestMessageSummary `json:"latestMessage"`
}

type InboxPage struct {
	Conversations []ConversationSummary `json:"conversations"`
	NextBefore    int64                 `json:"nextBefore,omitempty"`
}

type SendRequest struct {
	SenderUserID    int64
	RecipientUserID int64
	Body            string
}

type Store interface {
	SendPrivateMessage(context.Context, SendRequest) (Message, error)
	ListPrivateMessages(context.Context, Query) ([]Message, error)
	ListPrivateMessageInbox(context.Context, InboxQuery) ([]ConversationSummary, error)
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

func (s *Service) Send(ctx context.Context, request SendRequest) (Message, error) {
	if request.SenderUserID <= 0 {
		return Message{}, fmt.Errorf("sender user id must be positive")
	}
	if request.RecipientUserID <= 0 {
		return Message{}, fmt.Errorf("recipient user id must be positive")
	}
	if request.SenderUserID == request.RecipientUserID {
		return Message{}, ErrSelfMessage
	}
	if !validBody(request.Body) {
		return Message{}, ErrInvalidBody
	}
	return s.store.SendPrivateMessage(ctx, request)
}

func (s *Service) List(ctx context.Context, query Query) (Page, error) {
	if query.ActorUserID <= 0 {
		return Page{}, fmt.Errorf("actor user id must be positive")
	}
	if query.PeerUserID <= 0 {
		return Page{}, fmt.Errorf("peer user id must be positive")
	}
	if query.ActorUserID == query.PeerUserID {
		return Page{}, ErrSelfMessage
	}
	if query.Before < 0 {
		return Page{}, fmt.Errorf("message cursor must not be negative")
	}
	query.Limit = normalizeLimit(query.Limit)
	messages, err := s.store.ListPrivateMessages(ctx, query)
	if err != nil {
		return Page{}, err
	}
	if messages == nil {
		messages = []Message{}
	}

	page := Page{Messages: messages}
	if len(page.Messages) > query.Limit {
		page.Messages = page.Messages[:query.Limit]
		page.NextBefore = page.Messages[len(page.Messages)-1].ID
	}
	return page, nil
}

func (s *Service) Inbox(ctx context.Context, query InboxQuery) (InboxPage, error) {
	if query.ActorUserID <= 0 {
		return InboxPage{}, fmt.Errorf("actor user id must be positive")
	}
	if query.Before < 0 {
		return InboxPage{}, fmt.Errorf("inbox cursor must not be negative")
	}
	query.Limit = normalizeLimit(query.Limit)
	conversations, err := s.store.ListPrivateMessageInbox(ctx, query)
	if err != nil {
		return InboxPage{}, err
	}
	if conversations == nil {
		conversations = []ConversationSummary{}
	}

	page := InboxPage{Conversations: conversations}
	if len(page.Conversations) > query.Limit {
		page.Conversations = page.Conversations[:query.Limit]
		page.NextBefore = page.Conversations[len(page.Conversations)-1].LatestMessage.ID
	}
	return page, nil
}

func validBody(body string) bool {
	if !utf8.ValidString(body) || strings.IndexByte(body, 0) >= 0 {
		return false
	}
	length := utf8.RuneCountInString(body)
	return length >= 1 && length <= MaxBodyCharacters
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}
