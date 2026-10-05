package mediastatus

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	JobStatePending   int16 = 0
	JobStateRunning   int16 = 1
	JobStateSucceeded int16 = 2
	JobStateFailed    int16 = 3
)

const releasedPostState int16 = 1

type Phase string

const (
	PhaseWaiting    Phase = "waiting"
	PhaseProcessing Phase = "processing"
	PhaseRetrying   Phase = "retrying"
	PhaseFailed     Phase = "failed"
	PhaseReady      Phase = "ready"
)

type Operation string

const (
	OperationInitial      Operation = "initial"
	OperationRegeneration Operation = "regeneration"
)

var (
	ErrPostNotFound      = errors.New("post not found")
	ErrInconsistentState = errors.New("inconsistent media status state")
)

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Status struct {
	PostID      int64      `json:"postId"`
	Phase       Phase      `json:"phase"`
	Operation   Operation  `json:"operation,omitempty"`
	UsableMedia bool       `json:"usableMedia"`
	RetryAt     *time.Time `json:"retryAt,omitempty"`
	Failure     *Failure   `json:"failure,omitempty"`
}

type JobSnapshot struct {
	State          int16
	Attempts       int32
	MaxAttempts    int32
	AvailableAt    time.Time
	LeaseExpiresAt *time.Time
}

type Snapshot struct {
	PostID       int64
	ReleaseState int16
	Deleted      bool
	MediaReady   bool
	Job          *JobSnapshot
}

type Store interface {
	LoadMediaStatus(context.Context, int64) (Snapshot, error)
}

type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

func (s *Service) Get(ctx context.Context, postID int64) (Status, error) {
	if postID <= 0 {
		return Status{}, fmt.Errorf("post id must be positive")
	}
	snapshot, err := s.store.LoadMediaStatus(ctx, postID)
	if err != nil {
		return Status{}, err
	}
	return Derive(snapshot, time.Now())
}

// Public exposes status only for an already-public post with last-known-good media.
// Initial ingestion stays private until M4 adds authenticated owner visibility.
func (s *Service) Public(ctx context.Context, postID int64) (Status, error) {
	if postID <= 0 {
		return Status{}, fmt.Errorf("post id must be positive")
	}
	snapshot, err := s.store.LoadMediaStatus(ctx, postID)
	if err != nil {
		return Status{}, err
	}
	if snapshot.Deleted || snapshot.ReleaseState != releasedPostState || !snapshot.MediaReady {
		return Status{}, ErrPostNotFound
	}
	return Derive(snapshot, time.Now())
}

func Derive(snapshot Snapshot, now time.Time) (Status, error) {
	if snapshot.PostID <= 0 {
		return Status{}, fmt.Errorf("%w: invalid post id", ErrInconsistentState)
	}
	usableMedia := !snapshot.Deleted && snapshot.ReleaseState == releasedPostState && snapshot.MediaReady
	status := Status{PostID: snapshot.PostID, UsableMedia: usableMedia}

	if snapshot.Job == nil {
		if usableMedia {
			status.Phase = PhaseReady
			return status, nil
		}
		return Status{}, fmt.Errorf("%w: post has neither a media job nor usable media", ErrInconsistentState)
	}

	job := snapshot.Job
	if job.Attempts < 0 || job.MaxAttempts <= 0 || job.Attempts > job.MaxAttempts {
		return Status{}, fmt.Errorf("%w: invalid attempt counters", ErrInconsistentState)
	}
	operation := OperationInitial
	if usableMedia {
		operation = OperationRegeneration
	}

	switch job.State {
	case JobStatePending:
		if job.Attempts >= job.MaxAttempts {
			return Status{}, fmt.Errorf("%w: pending job exhausted attempts", ErrInconsistentState)
		}
		status.Operation = operation
		if job.Attempts == 0 {
			status.Phase = PhaseWaiting
			return status, nil
		}
		status.Phase = PhaseRetrying
		if job.AvailableAt.After(now) {
			retryAt := job.AvailableAt
			status.RetryAt = &retryAt
		}
		return status, nil

	case JobStateRunning:
		if job.Attempts == 0 || job.LeaseExpiresAt == nil {
			return Status{}, fmt.Errorf("%w: running job lacks an active attempt/lease", ErrInconsistentState)
		}
		status.Operation = operation
		if job.LeaseExpiresAt.After(now) {
			status.Phase = PhaseProcessing
			return status, nil
		}
		if job.Attempts >= job.MaxAttempts {
			return failedStatus(status, operation), nil
		}
		status.Phase = PhaseRetrying
		return status, nil

	case JobStateSucceeded:
		if !usableMedia {
			return Status{}, fmt.Errorf("%w: succeeded media job has no usable publication", ErrInconsistentState)
		}
		status.Phase = PhaseReady
		return status, nil

	case JobStateFailed:
		status.Operation = operation
		return failedStatus(status, operation), nil

	default:
		return Status{}, fmt.Errorf("%w: unknown media job state %d", ErrInconsistentState, job.State)
	}
}

func failedStatus(status Status, operation Operation) Status {
	status.Phase = PhaseFailed
	status.Operation = operation
	message := "Media processing failed."
	if operation == OperationRegeneration {
		message = "Replacement processing failed; existing media is still available."
	}
	status.Failure = &Failure{Code: "processing_failed", Message: message}
	status.RetryAt = nil
	return status
}
