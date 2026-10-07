package mediajobadmin

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	DefaultLimit = 50
	MaxLimit     = 100

	StatePending   int16 = 0
	StateRunning   int16 = 1
	StateSucceeded int16 = 2
	StateFailed    int16 = 3
)

var ErrForbidden = errors.New("media job inspection forbidden")

type Query struct {
	ActorUserID int64
	Before      int64
	Limit       int
}

type Record struct {
	ID              int64
	PostID          int64
	Kind            int16
	State           int16
	Priority        int16
	Attempts        int32
	MaxAttempts     int32
	AvailableAt     time.Time
	ClaimedAt       *time.Time
	ClaimedBy       *string
	LeaseExpiresAt  *time.Time
	LeaseGeneration int64
	LastError       *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Job struct {
	ID              int64      `json:"id"`
	PostID          int64      `json:"postId"`
	Kind            int16      `json:"kind"`
	State           string     `json:"state"`
	Priority        int16      `json:"priority"`
	Attempts        int32      `json:"attempts"`
	MaxAttempts     int32      `json:"maxAttempts"`
	AvailableAt     time.Time  `json:"availableAt"`
	ClaimedAt       *time.Time `json:"claimedAt,omitempty"`
	ClaimedBy       *string    `json:"claimedBy,omitempty"`
	LeaseExpiresAt  *time.Time `json:"leaseExpiresAt,omitempty"`
	LeaseGeneration int64      `json:"leaseGeneration"`
	LastError       *string    `json:"lastError,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type Page struct {
	Jobs       []Job `json:"jobs"`
	NextBefore int64 `json:"nextBefore,omitempty"`
}

type Store interface {
	ListMediaJobs(context.Context, int64, int64, int) ([]Record, error)
}

type Service struct {
	store Store
}

func New(store Store) *Service {
	return &Service{store: store}
}

func (s *Service) List(ctx context.Context, query Query) (Page, error) {
	if query.ActorUserID <= 0 {
		return Page{}, fmt.Errorf("actor user id must be positive")
	}
	if query.Before < 0 {
		return Page{}, fmt.Errorf("cursor must not be negative")
	}
	query.Limit = normalizeLimit(query.Limit)

	records, err := s.store.ListMediaJobs(ctx, query.ActorUserID, query.Before, query.Limit+1)
	if err != nil {
		return Page{}, err
	}

	jobs := make([]Job, 0, min(len(records), query.Limit))
	for _, record := range records {
		state, err := stateName(record.State)
		if err != nil {
			return Page{}, err
		}
		jobs = append(jobs, Job{
			ID:              record.ID,
			PostID:          record.PostID,
			Kind:            record.Kind,
			State:           state,
			Priority:        record.Priority,
			Attempts:        record.Attempts,
			MaxAttempts:     record.MaxAttempts,
			AvailableAt:     record.AvailableAt,
			ClaimedAt:       record.ClaimedAt,
			ClaimedBy:       record.ClaimedBy,
			LeaseExpiresAt:  record.LeaseExpiresAt,
			LeaseGeneration: record.LeaseGeneration,
			LastError:       record.LastError,
			CreatedAt:       record.CreatedAt,
			UpdatedAt:       record.UpdatedAt,
		})
	}

	page := Page{Jobs: jobs}
	if len(page.Jobs) > query.Limit {
		page.Jobs = page.Jobs[:query.Limit]
		page.NextBefore = page.Jobs[len(page.Jobs)-1].ID
	}
	return page, nil
}

func normalizeLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	default:
		return limit
	}
}

func stateName(state int16) (string, error) {
	switch state {
	case StatePending:
		return "pending", nil
	case StateRunning:
		return "running", nil
	case StateSucceeded:
		return "succeeded", nil
	case StateFailed:
		return "failed", nil
	default:
		return "", fmt.Errorf("unknown media job state %d", state)
	}
}
