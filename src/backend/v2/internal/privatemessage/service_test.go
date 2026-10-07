package privatemessage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type testStore struct {
	sendRequest SendRequest
	query       Query
	message     Message
	messages    []Message
	err         error
}

func (s *testStore) SendPrivateMessage(_ context.Context, request SendRequest) (Message, error) {
	s.sendRequest = request
	return s.message, s.err
}

func (s *testStore) ListPrivateMessages(_ context.Context, query Query) ([]Message, error) {
	s.query = query
	return s.messages, s.err
}

func TestSendValidatesIdentitySelfPolicyAndBodyBeforeStore(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	for _, request := range []SendRequest{
		{SenderUserID: 0, RecipientUserID: 2, Body: "ok"},
		{SenderUserID: 1, RecipientUserID: 0, Body: "ok"},
		{SenderUserID: 1, RecipientUserID: 1, Body: "ok"},
		{SenderUserID: 1, RecipientUserID: 2, Body: ""},
		{SenderUserID: 1, RecipientUserID: 2, Body: "a\x00b"},
		{SenderUserID: 1, RecipientUserID: 2, Body: invalidUTF8},
		{SenderUserID: 1, RecipientUserID: 2, Body: strings.Repeat("😀", MaxBodyCharacters+1)},
	} {
		store := &testStore{}
		_, err := New(store).Send(context.Background(), request)
		if err == nil {
			t.Fatalf("request=%#v unexpectedly succeeded", request)
		}
		if store.sendRequest.SenderUserID != 0 {
			t.Fatalf("invalid request reached store: %#v", store.sendRequest)
		}
	}

	store := &testStore{message: Message{ID: 9, SenderID: 1, RecipientID: 2, Body: strings.Repeat("😀", MaxBodyCharacters)}}
	got, err := New(store).Send(context.Background(), SendRequest{SenderUserID: 1, RecipientUserID: 2, Body: store.message.Body})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 9 || store.sendRequest.SenderUserID != 1 || store.sendRequest.RecipientUserID != 2 {
		t.Fatalf("message=%#v request=%#v", got, store.sendRequest)
	}
}

func TestSendPreservesStoreErrors(t *testing.T) {
	store := &testStore{err: ErrRecipientUnavailable}
	_, err := New(store).Send(context.Background(), SendRequest{SenderUserID: 1, RecipientUserID: 2, Body: "hello"})
	if !errors.Is(err, ErrRecipientUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestListNormalizesLimitsAndBuildsDescendingCursorPage(t *testing.T) {
	messages := []Message{{ID: 30}, {ID: 29}, {ID: 28}}
	store := &testStore{messages: messages}
	page, err := New(store).List(context.Background(), Query{ActorUserID: 1, PeerUserID: 2, Before: 31, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if store.query.Before != 31 || store.query.Limit != 2 || len(page.Messages) != 2 || page.Messages[0].ID != 30 || page.Messages[1].ID != 29 || page.NextBefore != 29 {
		t.Fatalf("query=%#v page=%#v", store.query, page)
	}

	store = &testStore{}
	if _, err := New(store).List(context.Background(), Query{ActorUserID: 1, PeerUserID: 2}); err != nil {
		t.Fatal(err)
	}
	if store.query.Limit != DefaultLimit {
		t.Fatalf("default limit=%d", store.query.Limit)
	}
	store = &testStore{}
	if _, err := New(store).List(context.Background(), Query{ActorUserID: 1, PeerUserID: 2, Limit: MaxLimit + 1}); err != nil {
		t.Fatal(err)
	}
	if store.query.Limit != MaxLimit {
		t.Fatalf("max limit=%d", store.query.Limit)
	}
}

func TestListRejectsInvalidIdentityCursorAndSelfThreadBeforeStore(t *testing.T) {
	for _, query := range []Query{
		{ActorUserID: 0, PeerUserID: 2},
		{ActorUserID: 1, PeerUserID: 0},
		{ActorUserID: 1, PeerUserID: 1},
		{ActorUserID: 1, PeerUserID: 2, Before: -1},
	} {
		store := &testStore{}
		if _, err := New(store).List(context.Background(), query); err == nil {
			t.Fatalf("query=%#v unexpectedly succeeded", query)
		}
		if store.query.ActorUserID != 0 {
			t.Fatalf("invalid query reached store: %#v", store.query)
		}
	}
}
