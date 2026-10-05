package tag

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type serviceStore struct {
	added   Name
	removed int64
}

func (s *serviceStore) LoadPostTags(context.Context, int64, int64) (Snapshot, error) {
	return Snapshot{PostID: 42, Tags: []Item{}}, nil
}

func (s *serviceStore) AddPostTag(_ context.Context, _, postID int64, name Name) (Snapshot, error) {
	s.added = name
	return Snapshot{PostID: postID, Tags: []Item{{ID: 7, Name: name.Display}}}, nil
}

func (s *serviceStore) RemovePostTag(_ context.Context, _, postID, tagID int64) (Snapshot, error) {
	s.removed = tagID
	return Snapshot{PostID: postID, Tags: []Item{}}, nil
}

func TestNormalizeNameMatchesSearchSemantics(t *testing.T) {
	got, err := NormalizeName("  CaT Photo  ")
	if err != nil {
		t.Fatal(err)
	}
	if got.Display != "CaT Photo" || got.Normalized != "cat photo" {
		t.Fatalf("name=%#v", got)
	}
}

func TestNormalizeNameRejectsInvalidOrUnsearchableNames(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	for _, raw := range []string{
		"",
		"   ",
		strings.Repeat("x", MaxNameRunes+1),
		"bad\x00tag",
		"line\nbreak",
		`bad\tag`,
		`bad"tag`,
		invalidUTF8,
	} {
		if _, err := NormalizeName(raw); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("raw=%q error=%v", raw, err)
		}
	}
}

func TestServiceAddPassesNormalizedName(t *testing.T) {
	store := &serviceStore{}
	service := New(store)
	got, err := service.Add(context.Background(), 9, 42, "  Mixed Case  ")
	if err != nil {
		t.Fatal(err)
	}
	if store.added.Display != "Mixed Case" || store.added.Normalized != "mixed case" {
		t.Fatalf("stored name=%#v", store.added)
	}
	if got.PostID != 42 || len(got.Tags) != 1 {
		t.Fatalf("snapshot=%#v", got)
	}
}

func TestServiceRejectsInvalidIdentifiersBeforeStore(t *testing.T) {
	service := New(&serviceStore{})
	if _, err := service.List(context.Background(), 0, 0); err == nil {
		t.Fatal("list accepted invalid post id")
	}
	if _, err := service.Add(context.Background(), 0, 42, "tag"); err == nil {
		t.Fatal("add accepted invalid user id")
	}
	if _, err := service.Remove(context.Background(), 9, 42, 0); err == nil {
		t.Fatal("remove accepted invalid tag id")
	}
}
