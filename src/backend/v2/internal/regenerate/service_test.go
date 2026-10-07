package regenerate

import (
	"context"
	"errors"
	"testing"
)

type fakeRepository struct {
	actorUserID int64
	postID      int64
	result      Requested
	err         error
}

func (r *fakeRepository) RequestRegeneration(_ context.Context, actorUserID, postID int64) (Requested, error) {
	r.actorUserID = actorUserID
	r.postID = postID
	return r.result, r.err
}

func TestServiceRequestDelegatesValidatedActorAndPost(t *testing.T) {
	repo := &fakeRepository{result: Requested{JobID: 41, Outcome: OutcomeQueued}}
	service := New(repo)

	got, err := service.Request(context.Background(), 7, 17)
	if err != nil {
		t.Fatal(err)
	}
	if got != repo.result || repo.actorUserID != 7 || repo.postID != 17 {
		t.Fatalf("request = %#v, repo actor = %d, repo post = %d", got, repo.actorUserID, repo.postID)
	}
}

func TestServiceRequestRejectsInvalidIDsWithoutRepositoryCall(t *testing.T) {
	for _, tc := range []struct {
		name        string
		actorUserID int64
		postID      int64
	}{
		{name: "actor", actorUserID: 0, postID: 17},
		{name: "post", actorUserID: 7, postID: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{}
			service := New(repo)

			if _, err := service.Request(context.Background(), tc.actorUserID, tc.postID); err == nil {
				t.Fatal("invalid ids unexpectedly accepted")
			}
			if repo.actorUserID != 0 || repo.postID != 0 {
				t.Fatalf("repository called with actor=%d post=%d", repo.actorUserID, repo.postID)
			}
		})
	}
}

func TestServiceRequestPreservesRepositoryErrors(t *testing.T) {
	for _, wantErr := range []error{ErrForbidden, ErrNotRegenerable} {
		repo := &fakeRepository{err: wantErr}
		service := New(repo)

		_, err := service.Request(context.Background(), 7, 9)
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want %v", err, wantErr)
		}
	}
}
