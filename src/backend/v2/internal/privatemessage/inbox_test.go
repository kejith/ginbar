package privatemessage

import (
	"context"
	"errors"
	"testing"
)

func (s *testStore) ListPrivateMessageInbox(_ context.Context, _ InboxQuery) ([]ConversationSummary, error) {
	return nil, s.err
}

type inboxTestStore struct {
	inboxQuery InboxQuery
	items      []ConversationSummary
	err        error
}

func (s *inboxTestStore) SendPrivateMessage(context.Context, SendRequest) (Message, error) {
	return Message{}, errors.New("unexpected send")
}

func (s *inboxTestStore) ListPrivateMessages(context.Context, Query) ([]Message, error) {
	return nil, errors.New("unexpected thread read")
}

func (s *inboxTestStore) ListPrivateMessageInbox(_ context.Context, query InboxQuery) ([]ConversationSummary, error) {
	s.inboxQuery = query
	return s.items, s.err
}

func TestInboxNormalizesLimitsAndBuildsCursorPage(t *testing.T) {
	store := &inboxTestStore{items: []ConversationSummary{
		{LatestMessage: LatestMessageSummary{ID: 30}},
		{LatestMessage: LatestMessageSummary{ID: 29}},
		{LatestMessage: LatestMessageSummary{ID: 28}},
	}}
	page, err := New(store).Inbox(context.Background(), InboxQuery{ActorUserID: 7, Before: 31, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if store.inboxQuery.ActorUserID != 7 || store.inboxQuery.Before != 31 || store.inboxQuery.Limit != 2 {
		t.Fatalf("query=%#v", store.inboxQuery)
	}
	if len(page.Conversations) != 2 || page.Conversations[0].LatestMessage.ID != 30 || page.Conversations[1].LatestMessage.ID != 29 || page.NextBefore != 29 {
		t.Fatalf("page=%#v", page)
	}

	store = &inboxTestStore{}
	if _, err := New(store).Inbox(context.Background(), InboxQuery{ActorUserID: 7}); err != nil {
		t.Fatal(err)
	}
	if store.inboxQuery.Limit != DefaultLimit {
		t.Fatalf("default limit=%d", store.inboxQuery.Limit)
	}
	store = &inboxTestStore{}
	if _, err := New(store).Inbox(context.Background(), InboxQuery{ActorUserID: 7, Limit: MaxLimit + 1}); err != nil {
		t.Fatal(err)
	}
	if store.inboxQuery.Limit != MaxLimit {
		t.Fatalf("max limit=%d", store.inboxQuery.Limit)
	}
}

func TestInboxRejectsInvalidActorAndCursorBeforeStore(t *testing.T) {
	for _, query := range []InboxQuery{
		{ActorUserID: 0},
		{ActorUserID: -1},
		{ActorUserID: 7, Before: -1},
	} {
		store := &inboxTestStore{}
		if _, err := New(store).Inbox(context.Background(), query); err == nil {
			t.Fatalf("query=%#v unexpectedly succeeded", query)
		}
		if store.inboxQuery.ActorUserID != 0 {
			t.Fatalf("invalid query reached store: %#v", store.inboxQuery)
		}
	}
}

func TestInboxPreservesStoreErrorsAndReturnsNonNilEmptySlice(t *testing.T) {
	store := &inboxTestStore{err: errors.New("database unavailable")}
	if _, err := New(store).Inbox(context.Background(), InboxQuery{ActorUserID: 7}); err == nil {
		t.Fatal("expected store error")
	}

	store = &inboxTestStore{}
	page, err := New(store).Inbox(context.Background(), InboxQuery{ActorUserID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if page.Conversations == nil || len(page.Conversations) != 0 || page.NextBefore != 0 {
		t.Fatalf("page=%#v", page)
	}
}
