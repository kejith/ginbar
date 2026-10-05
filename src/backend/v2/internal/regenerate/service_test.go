package regenerate

import (
	"context"
	"errors"
	"testing"
)

type fakeRepository struct {
	postID int64
	result Requested
	err    error
}

func (r *fakeRepository) RequestRegeneration(_ context.Context, postID int64) (Requested, error) {
	r.postID = postID
	return r.result, r.err
}

func TestServiceRequestDelegatesValidatedPost(t *testing.T) {
	repo := &fakeRepository{result: Requested{JobID: 41, Outcome: OutcomeQueued}}
	service, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}

	got, err := service.Request(context.Background(), 17)
	if err != nil {
		t.Fatal(err)
	}
	if got != repo.result || repo.postID != 17 {
		t.Fatalf("request = %#v, repo post id = %d", got, repo.postID)
	}
}

func TestServiceRequestRejectsInvalidPostWithoutRepositoryCall(t *testing.T) {
	repo := &fakeRepository{}
	service, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.Request(context.Background(), 0); err == nil {
		t.Fatal("zero post id unexpectedly accepted")
	}
	if repo.postID != 0 {
		t.Fatalf("repository called with post id %d", repo.postID)
	}
}

func TestServiceRequestPreservesRepositoryError(t *testing.T) {
	repo := &fakeRepository{err: ErrNotRegenerable}
	service, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Request(context.Background(), 9)
	if !errors.Is(err, ErrNotRegenerable) {
		t.Fatalf("error = %v", err)
	}
}
