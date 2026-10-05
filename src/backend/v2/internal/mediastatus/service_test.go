package mediastatus

import (
	"context"
	"errors"
	"testing"
	"time"
)

type statusStore struct {
	snapshot Snapshot
	err      error
}

func (s *statusStore) LoadMediaStatus(context.Context, int64) (Snapshot, error) {
	return s.snapshot, s.err
}

func TestDeriveMediaStatusLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	future := now.Add(8 * time.Second)
	activeLease := now.Add(30 * time.Second)
	expiredLease := now.Add(-time.Second)

	tests := []struct {
		name       string
		snapshot   Snapshot
		phase      Phase
		operation  Operation
		usable     bool
		retryAt    *time.Time
		failureMsg string
	}{
		{
			name:      "initial pending",
			snapshot:  Snapshot{PostID: 1, Job: &JobSnapshot{State: JobStatePending, MaxAttempts: 5, AvailableAt: now}},
			phase:     PhaseWaiting,
			operation: OperationInitial,
		},
		{
			name:      "initial running",
			snapshot:  Snapshot{PostID: 2, Job: &JobSnapshot{State: JobStateRunning, Attempts: 1, MaxAttempts: 5, LeaseExpiresAt: &activeLease}},
			phase:     PhaseProcessing,
			operation: OperationInitial,
		},
		{
			name:      "delayed retry",
			snapshot:  Snapshot{PostID: 3, Job: &JobSnapshot{State: JobStatePending, Attempts: 1, MaxAttempts: 5, AvailableAt: future}},
			phase:     PhaseRetrying,
			operation: OperationInitial,
			retryAt:   &future,
		},
		{
			name:      "expired lease retries",
			snapshot:  Snapshot{PostID: 4, Job: &JobSnapshot{State: JobStateRunning, Attempts: 2, MaxAttempts: 5, LeaseExpiresAt: &expiredLease}},
			phase:     PhaseRetrying,
			operation: OperationInitial,
		},
		{
			name:       "terminal initial failure",
			snapshot:   Snapshot{PostID: 5, Job: &JobSnapshot{State: JobStateFailed, Attempts: 2, MaxAttempts: 5}},
			phase:      PhaseFailed,
			operation:  OperationInitial,
			failureMsg: "Media processing failed.",
		},
		{
			name:       "expired final lease is terminal",
			snapshot:   Snapshot{PostID: 6, Job: &JobSnapshot{State: JobStateRunning, Attempts: 5, MaxAttempts: 5, LeaseExpiresAt: &expiredLease}},
			phase:      PhaseFailed,
			operation:  OperationInitial,
			failureMsg: "Media processing failed.",
		},
		{
			name:      "released regeneration pending keeps media",
			snapshot:  Snapshot{PostID: 7, ReleaseState: releasedPostState, MediaReady: true, Job: &JobSnapshot{State: JobStatePending, MaxAttempts: 5, AvailableAt: now}},
			phase:     PhaseWaiting,
			operation: OperationRegeneration,
			usable:    true,
		},
		{
			name:       "released regeneration failure keeps media",
			snapshot:   Snapshot{PostID: 8, ReleaseState: releasedPostState, MediaReady: true, Job: &JobSnapshot{State: JobStateFailed, Attempts: 1, MaxAttempts: 5}},
			phase:      PhaseFailed,
			operation:  OperationRegeneration,
			usable:     true,
			failureMsg: "Replacement processing failed; existing media is still available.",
		},
		{
			name:      "successful publication is ready",
			snapshot:  Snapshot{PostID: 9, ReleaseState: releasedPostState, MediaReady: true, Job: &JobSnapshot{State: JobStateSucceeded, Attempts: 1, MaxAttempts: 5}},
			phase:     PhaseReady,
			usable:    true,
		},
		{
			name:     "released imported media without job is ready",
			snapshot: Snapshot{PostID: 10, ReleaseState: releasedPostState, MediaReady: true},
			phase:    PhaseReady,
			usable:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Derive(test.snapshot, now)
			if err != nil {
				t.Fatal(err)
			}
			if got.Phase != test.phase || got.Operation != test.operation || got.UsableMedia != test.usable {
				t.Fatalf("status = %#v", got)
			}
			if (got.RetryAt == nil) != (test.retryAt == nil) || got.RetryAt != nil && !got.RetryAt.Equal(*test.retryAt) {
				t.Fatalf("retryAt = %v, want %v", got.RetryAt, test.retryAt)
			}
			if test.failureMsg == "" {
				if got.Failure != nil {
					t.Fatalf("unexpected failure = %#v", got.Failure)
				}
			} else if got.Failure == nil || got.Failure.Code != "processing_failed" || got.Failure.Message != test.failureMsg {
				t.Fatalf("failure = %#v", got.Failure)
			}
		})
	}
}

func TestDeriveRejectsImpossibleSucceededInitialState(t *testing.T) {
	_, err := Derive(Snapshot{
		PostID: 11,
		Job:    &JobSnapshot{State: JobStateSucceeded, Attempts: 1, MaxAttempts: 5},
	}, time.Now())
	if !errors.Is(err, ErrInconsistentState) {
		t.Fatalf("error = %v", err)
	}
}

func TestPublicStatusHidesNonPublicPosts(t *testing.T) {
	tests := []Snapshot{
		{PostID: 12, Job: &JobSnapshot{State: JobStatePending, MaxAttempts: 5}},
		{PostID: 13, ReleaseState: releasedPostState, Job: &JobSnapshot{State: JobStateFailed, MaxAttempts: 5}},
		{PostID: 14, ReleaseState: releasedPostState, Deleted: true, MediaReady: true, Job: &JobSnapshot{State: JobStateFailed, MaxAttempts: 5}},
	}
	for _, snapshot := range tests {
		_, err := New(&statusStore{snapshot: snapshot}).Public(context.Background(), snapshot.PostID)
		if !errors.Is(err, ErrPostNotFound) {
			t.Fatalf("snapshot %#v error = %v", snapshot, err)
		}
	}
}

func TestInternalGetRetainsInitialIngestionStatus(t *testing.T) {
	service := New(&statusStore{snapshot: Snapshot{
		PostID: 15,
		Job:    &JobSnapshot{State: JobStatePending, MaxAttempts: 5},
	}})
	status, err := service.Get(context.Background(), 15)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != PhaseWaiting || status.Operation != OperationInitial || status.UsableMedia {
		t.Fatalf("status = %#v", status)
	}
}
