package mediajobadmin

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testStore struct {
	records []Record
	err     error
	actorID int64
	before  int64
	limit   int
}

func (s *testStore) ListMediaJobs(_ context.Context, actorID, before int64, limit int) ([]Record, error) {
	s.actorID = actorID
	s.before = before
	s.limit = limit
	return s.records, s.err
}

func TestListBoundsPageAndMapsAuthoritativeState(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	lastError := "temporary failure"
	store := &testStore{records: []Record{
		{
			ID: 30, PostID: 3, State: StateFailed, Attempts: 5, MaxAttempts: 5,
			AvailableAt: now, LastError: &lastError, LastErrorTruncated: true,
		},
		{ID: 29, PostID: 2, State: StateRunning, Attempts: 1, MaxAttempts: 5, AvailableAt: now},
		{ID: 28, PostID: 1, State: StatePending, Attempts: 2, MaxAttempts: 5, AvailableAt: now},
	}}
	page, err := New(store).List(context.Background(), Query{ActorUserID: 42, Before: 31, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if store.actorID != 42 || store.before != 31 || store.limit != 3 {
		t.Fatalf("store query actor=%d before=%d limit=%d", store.actorID, store.before, store.limit)
	}
	if len(page.Jobs) != 2 || page.Jobs[0].ID != 30 || page.Jobs[0].State != "failed" || page.Jobs[1].State != "running" || page.NextBefore != 29 {
		t.Fatalf("page=%#v", page)
	}
	if page.Jobs[0].LastError == nil || *page.Jobs[0].LastError != lastError || !page.Jobs[0].LastErrorTruncated {
		t.Fatalf("last error metadata=%#v", page.Jobs[0])
	}
}

func TestListNormalizesLimitAndValidatesInput(t *testing.T) {
	store := &testStore{}
	if _, err := New(store).List(context.Background(), Query{ActorUserID: 42}); err != nil {
		t.Fatal(err)
	}
	if store.limit != DefaultLimit+1 {
		t.Fatalf("default store limit=%d", store.limit)
	}
	if _, err := New(store).List(context.Background(), Query{ActorUserID: 42, Limit: MaxLimit + 50}); err != nil {
		t.Fatal(err)
	}
	if store.limit != MaxLimit+1 {
		t.Fatalf("max store limit=%d", store.limit)
	}
	if _, err := New(store).List(context.Background(), Query{ActorUserID: 0}); err == nil {
		t.Fatal("zero actor unexpectedly accepted")
	}
	if _, err := New(store).List(context.Background(), Query{ActorUserID: 42, Before: -1}); err == nil {
		t.Fatal("negative cursor unexpectedly accepted")
	}

	store.err = ErrForbidden
	if _, err := New(store).List(context.Background(), Query{ActorUserID: 42}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("forbidden error=%v", err)
	}
}

func TestListRejectsUnknownDatabaseState(t *testing.T) {
	store := &testStore{records: []Record{{ID: 1, State: 99}}}
	if _, err := New(store).List(context.Background(), Query{ActorUserID: 42, Limit: 1}); err == nil {
		t.Fatal("unknown state unexpectedly accepted")
	}
}
