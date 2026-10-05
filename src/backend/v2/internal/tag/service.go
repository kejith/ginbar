package tag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxNameRunes = 80

	// Role values are the v2 user_roles values reserved for elevated tag removal.
	RoleModerator int16 = 1
	RoleAdmin     int16 = 2
)

var (
	ErrInvalidName          = errors.New("invalid tag name")
	ErrPostNotFound         = errors.New("post not found")
	ErrForbidden            = errors.New("tag removal forbidden")
	ErrCommitOutcomeUnknown = errors.New("transaction commit outcome unknown")
)

type Name struct {
	Display    string
	Normalized string
}

type Item struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Snapshot struct {
	PostID    int64  `json:"postId"`
	Tags      []Item `json:"tags"`
	CanRemove bool   `json:"canRemove"`
}

type Store interface {
	LoadPostTags(context.Context, int64, int64) (Snapshot, error)
	AddPostTag(context.Context, int64, int64, Name) (Snapshot, error)
	RemovePostTag(context.Context, int64, int64, int64) (Snapshot, error)
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

func (s *Service) List(ctx context.Context, postID, viewerUserID int64) (Snapshot, error) {
	if postID <= 0 {
		return Snapshot{}, fmt.Errorf("post id must be positive")
	}
	if viewerUserID < 0 {
		return Snapshot{}, fmt.Errorf("viewer user id must not be negative")
	}
	return s.store.LoadPostTags(ctx, postID, viewerUserID)
}

func (s *Service) Add(ctx context.Context, userID, postID int64, rawName string) (Snapshot, error) {
	if userID <= 0 {
		return Snapshot{}, fmt.Errorf("user id must be positive")
	}
	if postID <= 0 {
		return Snapshot{}, fmt.Errorf("post id must be positive")
	}
	name, err := NormalizeName(rawName)
	if err != nil {
		return Snapshot{}, err
	}
	return s.store.AddPostTag(ctx, userID, postID, name)
}

func (s *Service) Remove(ctx context.Context, userID, postID, tagID int64) (Snapshot, error) {
	if userID <= 0 {
		return Snapshot{}, fmt.Errorf("user id must be positive")
	}
	if postID <= 0 {
		return Snapshot{}, fmt.Errorf("post id must be positive")
	}
	if tagID <= 0 {
		return Snapshot{}, fmt.Errorf("tag id must be positive")
	}
	return s.store.RemovePostTag(ctx, userID, postID, tagID)
}

func NormalizeName(raw string) (Name, error) {
	if !utf8.ValidString(raw) || strings.IndexByte(raw, 0) >= 0 {
		return Name{}, ErrInvalidName
	}
	display := strings.TrimSpace(raw)
	if display == "" || utf8.RuneCountInString(display) > MaxNameRunes {
		return Name{}, ErrInvalidName
	}
	for _, r := range display {
		if unicode.IsControl(r) || r == '\\' || r == '"' {
			return Name{}, ErrInvalidName
		}
	}
	return Name{Display: display, Normalized: strings.ToLower(display)}, nil
}
