package profile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

const (
	DefaultLimit     = 33
	MaxLimit         = 120
	UserStatusActive = int16(0)
)

var ErrUserNotFound = errors.New("user not found")

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"createdAt"`
}

type Query struct {
	UserID int64
	Before int64
	Limit  int
}

type Page struct {
	User       User                `json:"user"`
	Posts      []model.PostSummary `json:"posts"`
	NextBefore int64               `json:"nextBefore,omitempty"`
}

type Store interface {
	LoadPublicUser(context.Context, int64) (User, error)
	ListProfilePosts(context.Context, Query) ([]model.PostSummary, error)
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

func (s *Service) Get(ctx context.Context, query Query) (Page, error) {
	if query.UserID <= 0 {
		return Page{}, fmt.Errorf("user id must be positive")
	}
	if query.Before < 0 {
		return Page{}, fmt.Errorf("profile cursor must not be negative")
	}
	query.Limit = normalizeLimit(query.Limit)

	user, err := s.store.LoadPublicUser(ctx, query.UserID)
	if err != nil {
		return Page{}, err
	}
	posts, err := s.store.ListProfilePosts(ctx, query)
	if err != nil {
		return Page{}, err
	}
	if posts == nil {
		posts = []model.PostSummary{}
	}

	page := Page{User: user, Posts: posts}
	if len(page.Posts) > query.Limit {
		page.Posts = page.Posts[:query.Limit]
		page.NextBefore = page.Posts[len(page.Posts)-1].ID
	}
	return page, nil
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
