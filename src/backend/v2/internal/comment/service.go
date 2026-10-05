package comment

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

const (
	DefaultLimit      = 100
	MaxLimit          = 200
	MaxBodyCharacters = 10000
)

var (
	ErrPostNotFound          = errors.New("post not found")
	ErrParentCommentNotFound = errors.New("parent comment not found")
	ErrParentCommentDeleted  = errors.New("parent comment deleted")
	ErrInvalidBody           = errors.New("invalid comment body")
	ErrInvalidParentComment  = errors.New("invalid parent comment id")
)

type Comment struct {
	ID              int64          `json:"id"`
	PostID          int64          `json:"postId"`
	AuthorID        int64          `json:"authorId"`
	ParentCommentID *int64         `json:"parentCommentId"`
	Body            *string        `json:"body,omitempty"`
	Score           int32          `json:"score"`
	UserVote        model.PostVote `json:"userVote"`
	CreatedAt       time.Time      `json:"createdAt"`
	Deleted         bool           `json:"deleted"`
}

type Query struct {
	PostID       int64
	After        int64
	Limit        int
	ViewerUserID int64
}

type Page struct {
	Comments  []Comment `json:"comments"`
	NextAfter int64     `json:"nextAfter,omitempty"`
}

type CreateRequest struct {
	PostID          int64
	UserID          int64
	ParentCommentID *int64
	Body            string
}

type Store interface {
	ListComments(context.Context, Query) ([]Comment, error)
	CreateComment(context.Context, CreateRequest) (Comment, error)
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

func (s *Service) List(ctx context.Context, q Query) (Page, error) {
	if q.PostID <= 0 {
		return Page{}, fmt.Errorf("post id must be positive")
	}
	if q.After < 0 {
		return Page{}, fmt.Errorf("comment cursor must not be negative")
	}
	if q.ViewerUserID < 0 {
		return Page{}, fmt.Errorf("viewer user id must not be negative")
	}
	q.Limit = normalizeLimit(q.Limit)
	comments, err := s.store.ListComments(ctx, q)
	if err != nil {
		return Page{}, err
	}
	if comments == nil {
		comments = []Comment{}
	}
	page := Page{Comments: comments}
	if len(page.Comments) > q.Limit {
		page.Comments = page.Comments[:q.Limit]
		page.NextAfter = page.Comments[len(page.Comments)-1].ID
	}
	return page, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (Comment, error) {
	if request.PostID <= 0 {
		return Comment{}, fmt.Errorf("post id must be positive")
	}
	if request.UserID <= 0 {
		return Comment{}, fmt.Errorf("user id must be positive")
	}
	if request.ParentCommentID != nil && *request.ParentCommentID <= 0 {
		return Comment{}, ErrInvalidParentComment
	}
	if !utf8.ValidString(request.Body) {
		return Comment{}, ErrInvalidBody
	}
	length := utf8.RuneCountInString(request.Body)
	if length < 1 || length > MaxBodyCharacters {
		return Comment{}, ErrInvalidBody
	}
	return s.store.CreateComment(ctx, request)
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
