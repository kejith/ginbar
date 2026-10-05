package comment

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type testStore struct {
	comments    []Comment
	listErr     error
	createErr   error
	lastQuery   Query
	lastCreate  CreateRequest
	createValue Comment
}

func (s *testStore) ListComments(_ context.Context, query Query) ([]Comment, error) {
	s.lastQuery = query
	return s.comments, s.listErr
}

func (s *testStore) CreateComment(_ context.Context, request CreateRequest) (Comment, error) {
	s.lastCreate = request
	if s.createErr != nil {
		return Comment{}, s.createErr
	}
	return s.createValue, nil
}

func TestListNormalizesLimitAndBuildsCursor(t *testing.T) {
	store := &testStore{comments: []Comment{{ID: 10}, {ID: 11}, {ID: 12}}}
	page, err := New(store).List(context.Background(), Query{PostID: 7, After: 9, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if store.lastQuery.Limit != 2 || store.lastQuery.After != 9 || store.lastQuery.PostID != 7 {
		t.Fatalf("query=%#v", store.lastQuery)
	}
	if len(page.Comments) != 2 || page.Comments[0].ID != 10 || page.Comments[1].ID != 11 || page.NextAfter != 11 {
		t.Fatalf("page=%#v", page)
	}
}

func TestListNormalizesNilCommentsToEmptySlice(t *testing.T) {
	page, err := New(&testStore{}).List(context.Background(), Query{PostID: 7})
	if err != nil {
		t.Fatal(err)
	}
	if page.Comments == nil || len(page.Comments) != 0 {
		t.Fatalf("comments=%#v", page.Comments)
	}
}

func TestListDefaultAndMaximumLimits(t *testing.T) {
	for _, tt := range []struct {
		name  string
		limit int
		want  int
	}{
		{name: "default", limit: 0, want: DefaultLimit},
		{name: "maximum", limit: MaxLimit + 100, want: MaxLimit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &testStore{}
			if _, err := New(store).List(context.Background(), Query{PostID: 1, Limit: tt.limit}); err != nil {
				t.Fatal(err)
			}
			if store.lastQuery.Limit != tt.want {
				t.Fatalf("limit=%d want=%d", store.lastQuery.Limit, tt.want)
			}
		})
	}
}

func TestListPropagatesPostNotFound(t *testing.T) {
	store := &testStore{listErr: ErrPostNotFound}
	_, err := New(store).List(context.Background(), Query{PostID: 999})
	if !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("error=%v", err)
	}
}

func TestCreateValidatesBodyAtUnicodeCharacterBoundary(t *testing.T) {
	validMax := strings.Repeat("😀", MaxBodyCharacters)
	for _, tt := range []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "one", body: "x"},
		{name: "max unicode", body: validMax},
		{name: "empty", body: "", wantErr: true},
		{name: "too long", body: validMax + "x", wantErr: true},
		{name: "invalid utf8", body: string([]byte{0xff}), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &testStore{createValue: Comment{ID: 1}}
			_, err := New(store).Create(context.Background(), CreateRequest{PostID: 2, UserID: 3, Body: tt.body})
			if tt.wantErr && !errors.Is(err, ErrInvalidBody) {
				t.Fatalf("error=%v", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCreateValidatesParentAndReturnsAuthoritativeStoreValue(t *testing.T) {
	invalidParent := int64(0)
	if _, err := New(&testStore{}).Create(context.Background(), CreateRequest{
		PostID: 1, UserID: 2, ParentCommentID: &invalidParent, Body: "reply",
	}); !errors.Is(err, ErrInvalidParentComment) {
		t.Fatalf("invalid parent error=%v", err)
	}

	parent := int64(8)
	body := "authoritative"
	created := Comment{ID: 9, PostID: 1, AuthorID: 2, ParentCommentID: &parent, Body: &body, Score: 0}
	store := &testStore{createValue: created}
	got, err := New(store).Create(context.Background(), CreateRequest{
		PostID: 1, UserID: 2, ParentCommentID: &parent, Body: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.PostID != 1 || got.AuthorID != 2 || got.ParentCommentID == nil || *got.ParentCommentID != parent || got.Body == nil || *got.Body != body {
		t.Fatalf("created=%#v", got)
	}
}
