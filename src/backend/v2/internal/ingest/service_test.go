package ingest

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

type fakeRepository struct {
	create func(context.Context, CreateRequest) (Created, error)
}

func (r fakeRepository) CreateIngestion(ctx context.Context, request CreateRequest) (Created, error) {
	return r.create(ctx, request)
}

type fakeStager struct {
	stage  func(context.Context, io.Reader) (StagedSource, error)
	remove func(context.Context, string) error
}

func (s fakeStager) Stage(ctx context.Context, reader io.Reader) (StagedSource, error) {
	return s.stage(ctx, reader)
}

func (s fakeStager) Remove(ctx context.Context, key string) error {
	if s.remove == nil {
		return nil
	}
	return s.remove(ctx, key)
}

type fakeFetcher struct {
	fetch func(context.Context, string) (FetchedSource, error)
}

func (f fakeFetcher) Fetch(ctx context.Context, rawURL string) (FetchedSource, error) {
	return f.fetch(ctx, rawURL)
}

func TestCreateUploadPersistsAuthenticatedUnreleasedWorkflow(t *testing.T) {
	var got CreateRequest
	service := mustService(t,
		fakeRepository{create: func(_ context.Context, request CreateRequest) (Created, error) {
			got = request
			return Created{PostID: 41, JobID: 52}, nil
		}},
		fakeStager{stage: func(_ context.Context, reader io.Reader) (StagedSource, error) {
			body, err := io.ReadAll(reader)
			if err != nil {
				return StagedSource{}, err
			}
			if string(body) != "body" {
				t.Fatalf("body = %q", body)
			}
			return stagedFixture("sources/aa/upload"), nil
		}},
		nil,
	)

	created, err := service.CreateUpload(context.Background(), Actor{UserID: 7}, model.FilterNSFW, Upload{
		Body:         strings.NewReader("body"),
		OriginalName: "clip.mp4",
		DeclaredMIME: "video/mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created != (Created{PostID: 41, JobID: 52}) {
		t.Fatalf("created = %#v", created)
	}
	if got.AuthorUserID != 7 || got.Filter != model.FilterNSFW {
		t.Fatalf("request identity/filter = %#v", got)
	}
	if got.Source.Type != SourceUpload || got.Source.URL != "" || got.Source.OriginalName != "clip.mp4" || got.Source.DeclaredMIME != "video/mp4" {
		t.Fatalf("source metadata = %#v", got.Source)
	}
}

func TestCreateUploadRequiresAuthenticatedActorBeforeStaging(t *testing.T) {
	var stageCalls atomic.Int32
	service := mustService(t,
		fakeRepository{create: func(context.Context, CreateRequest) (Created, error) {
			t.Fatal("repository should not be called")
			return Created{}, nil
		}},
		fakeStager{stage: func(context.Context, io.Reader) (StagedSource, error) {
			stageCalls.Add(1)
			return StagedSource{}, nil
		}},
		nil,
	)
	_, err := service.CreateUpload(context.Background(), Actor{}, model.FilterSFW, Upload{Body: strings.NewReader("x")})
	if err == nil {
		t.Fatal("anonymous upload unexpectedly succeeded")
	}
	if stageCalls.Load() != 0 {
		t.Fatalf("stage calls = %d", stageCalls.Load())
	}
}

func TestDefiniteRepositoryFailureRemovesStagedFileAfterCallerCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	var removed bool
	service := mustService(t,
		fakeRepository{create: func(context.Context, CreateRequest) (Created, error) {
			cancelParent()
			return Created{}, errors.New("foreign key failure")
		}},
		fakeStager{
			stage: func(context.Context, io.Reader) (StagedSource, error) {
				return stagedFixture("sources/aa/failure"), nil
			},
			remove: func(ctx context.Context, key string) error {
				if ctx.Err() != nil {
					t.Fatalf("cleanup inherited canceled context: %v", ctx.Err())
				}
				if key != "sources/aa/failure" {
					t.Fatalf("removed key = %q", key)
				}
				removed = true
				return nil
			},
		},
		nil,
	)
	_, err := service.CreateUpload(parent, Actor{UserID: 1}, model.FilterSFW, Upload{Body: strings.NewReader("x")})
	if err == nil || !removed {
		t.Fatalf("err = %v removed = %v", err, removed)
	}
}

func TestAmbiguousCommitPreservesStagedFile(t *testing.T) {
	var removeCalls atomic.Int32
	service := mustService(t,
		fakeRepository{create: func(context.Context, CreateRequest) (Created, error) {
			return Created{}, ErrCommitOutcomeUnknown
		}},
		fakeStager{
			stage: func(context.Context, io.Reader) (StagedSource, error) {
				return stagedFixture("sources/aa/ambiguous"), nil
			},
			remove: func(context.Context, string) error {
				removeCalls.Add(1)
				return nil
			},
		},
		nil,
	)
	_, err := service.CreateUpload(context.Background(), Actor{UserID: 1}, model.FilterSFW, Upload{Body: strings.NewReader("x")})
	if !errors.Is(err, ErrCommitOutcomeUnknown) {
		t.Fatalf("err = %v", err)
	}
	if removeCalls.Load() != 0 {
		t.Fatalf("ambiguous commit removed staged source %d times", removeCalls.Load())
	}
}

func TestCreateFromURLPersistsEffectiveValidatedURL(t *testing.T) {
	var got CreateRequest
	service := mustService(t,
		fakeRepository{create: func(_ context.Context, request CreateRequest) (Created, error) {
			got = request
			return Created{PostID: 9, JobID: 10}, nil
		}},
		fakeStager{stage: func(_ context.Context, reader io.Reader) (StagedSource, error) {
			if _, err := io.ReadAll(reader); err != nil {
				return StagedSource{}, err
			}
			return stagedFixture("sources/bb/url"), nil
		}},
		fakeFetcher{fetch: func(context.Context, string) (FetchedSource, error) {
			return FetchedSource{
				Body:         io.NopCloser(strings.NewReader("remote")),
				EffectiveURL: "https://cdn.example.test/media.jpg",
				DeclaredMIME: "image/jpeg",
			}, nil
		}},
	)
	_, err := service.CreateFromURL(context.Background(), Actor{UserID: 3}, model.FilterSFW, "https://example.test/a")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source.Type != SourceURL || got.Source.URL != "https://cdn.example.test/media.jpg" || got.Source.DeclaredMIME != "image/jpeg" {
		t.Fatalf("URL source = %#v", got.Source)
	}
}

func TestMaxConcurrentBoundsStagingAndCanceledWaiterDoesNotStart(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var stageCalls atomic.Int32
	service, err := New(
		fakeRepository{create: func(context.Context, CreateRequest) (Created, error) { return Created{PostID: 1, JobID: 1}, nil }},
		fakeStager{stage: func(ctx context.Context, _ io.Reader) (StagedSource, error) {
			stageCalls.Add(1)
			once.Do(func() { close(entered) })
			select {
			case <-release:
				return stagedFixture("sources/cc/slot"), nil
			case <-ctx.Done():
				return StagedSource{}, ctx.Err()
			}
		}},
		nil,
		Config{StageTimeout: time.Second, DBTimeout: time.Second, CleanupTimeout: time.Second, MaxConcurrent: 1},
	)
	if err != nil {
		t.Fatal(err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := service.CreateUpload(context.Background(), Actor{UserID: 1}, model.FilterSFW, Upload{Body: strings.NewReader("first")})
		firstDone <- err
	}()
	<-entered

	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, waitErr := service.CreateUpload(waitCtx, Actor{UserID: 2}, model.FilterSFW, Upload{Body: strings.NewReader("second")})
	if !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("second call error = %v", waitErr)
	}
	if stageCalls.Load() != 1 {
		t.Fatalf("stage calls while slot occupied = %d", stageCalls.Load())
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestStageTimeoutCancelsSlowStager(t *testing.T) {
	service, err := New(
		fakeRepository{create: func(context.Context, CreateRequest) (Created, error) {
			t.Fatal("repository should not be called")
			return Created{}, nil
		}},
		fakeStager{stage: func(ctx context.Context, _ io.Reader) (StagedSource, error) {
			<-ctx.Done()
			return StagedSource{}, ctx.Err()
		}},
		nil,
		Config{StageTimeout: 10 * time.Millisecond, DBTimeout: time.Second, CleanupTimeout: time.Second, MaxConcurrent: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateUpload(context.Background(), Actor{UserID: 1}, model.FilterSFW, Upload{Body: strings.NewReader("x")})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func mustService(t *testing.T, repo Repository, stager Stager, fetcher URLFetcher) *Service {
	t.Helper()
	service, err := New(repo, stager, fetcher, Config{
		StageTimeout:   time.Second,
		DBTimeout:      time.Second,
		CleanupTimeout: time.Second,
		MaxConcurrent:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func stagedFixture(key string) StagedSource {
	var hash [32]byte
	hash[0] = 1
	return StagedSource{StorageKey: key, ByteSize: 4, SHA256: hash}
}
