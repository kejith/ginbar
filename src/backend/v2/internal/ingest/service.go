package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/model"
)

var (
	ErrCommitOutcomeUnknown = errors.New("ingestion commit outcome unknown")
	ErrEmptySource          = errors.New("media source is empty")
	ErrInvalidInput         = errors.New("invalid ingestion input")
	ErrSourceTooLarge       = errors.New("media source exceeds size limit")
	ErrUnsafeURL            = errors.New("media source URL is not allowed")
)

const (
	SourceUpload SourceType = iota
	SourceURL
)

const (
	maxOriginalNameBytes = 512
	maxDeclaredMIMEBytes = 255
	maxSourceURLBytes    = 4096
)

type SourceType int16

type Actor struct {
	UserID int64
}

type Upload struct {
	Body         io.Reader
	OriginalName string
	DeclaredMIME string
}

type StagedSource struct {
	StorageKey string
	ByteSize   int64
	SHA256     [32]byte
}

type FetchedSource struct {
	Body         io.ReadCloser
	EffectiveURL string
	DeclaredMIME string
}

type SourceRecord struct {
	Type         SourceType
	StorageKey   string
	URL          string
	OriginalName string
	DeclaredMIME string
	ByteSize     int64
	SHA256       [32]byte
}

type CreateRequest struct {
	AuthorUserID int64
	Filter       model.ContentFilter
	Source       SourceRecord
}

type Created struct {
	PostID int64
	JobID  int64
}

type Repository interface {
	CreateIngestion(context.Context, CreateRequest) (Created, error)
}

type Stager interface {
	Stage(context.Context, io.Reader) (StagedSource, error)
	Remove(context.Context, string) error
}

type URLFetcher interface {
	Fetch(context.Context, string) (FetchedSource, error)
}

type Config struct {
	StageTimeout   time.Duration
	DBTimeout      time.Duration
	CleanupTimeout time.Duration
	MaxConcurrent  int
}

type Service struct {
	repo    Repository
	stager  Stager
	fetcher URLFetcher
	cfg     Config
	slots   chan struct{}
}

func New(repo Repository, stager Stager, fetcher URLFetcher, cfg Config) (*Service, error) {
	if repo == nil || stager == nil {
		return nil, errors.New("ingest repository and stager are required")
	}
	if cfg.StageTimeout <= 0 || cfg.DBTimeout <= 0 || cfg.CleanupTimeout <= 0 {
		return nil, errors.New("ingest timeouts must be positive")
	}
	if cfg.MaxConcurrent <= 0 {
		return nil, errors.New("ingest MaxConcurrent must be positive")
	}
	return &Service{
		repo:    repo,
		stager:  stager,
		fetcher: fetcher,
		cfg:     cfg,
		slots:   make(chan struct{}, cfg.MaxConcurrent),
	}, nil
}

func (s *Service) CreateUpload(
	ctx context.Context,
	actor Actor,
	filter model.ContentFilter,
	upload Upload,
) (Created, error) {
	if upload.Body == nil {
		return Created{}, fmt.Errorf("%w: upload body is required", ErrInvalidInput)
	}
	if err := validateRequest(actor, filter); err != nil {
		return Created{}, err
	}
	if err := validateMetadata(upload.OriginalName, upload.DeclaredMIME, ""); err != nil {
		return Created{}, err
	}

	if err := s.acquire(ctx); err != nil {
		return Created{}, err
	}
	defer s.release()

	staged, err := s.stage(ctx, upload.Body)
	if err != nil {
		return Created{}, err
	}
	return s.persist(ctx, CreateRequest{
		AuthorUserID: actor.UserID,
		Filter:       filter,
		Source: SourceRecord{
			Type:         SourceUpload,
			StorageKey:   staged.StorageKey,
			OriginalName: upload.OriginalName,
			DeclaredMIME: upload.DeclaredMIME,
			ByteSize:     staged.ByteSize,
			SHA256:       staged.SHA256,
		},
	})
}

func (s *Service) CreateFromURL(
	ctx context.Context,
	actor Actor,
	filter model.ContentFilter,
	rawURL string,
) (Created, error) {
	if err := validateRequest(actor, filter); err != nil {
		return Created{}, err
	}
	if len(rawURL) == 0 || len(rawURL) > maxSourceURLBytes {
		return Created{}, fmt.Errorf("%w: source URL length must be between 1 and %d bytes", ErrInvalidInput, maxSourceURLBytes)
	}
	if s.fetcher == nil {
		return Created{}, errors.New("URL ingestion is not configured")
	}

	if err := s.acquire(ctx); err != nil {
		return Created{}, err
	}
	defer s.release()

	stageCtx, cancel := context.WithTimeout(ctx, s.cfg.StageTimeout)
	fetched, err := s.fetcher.Fetch(stageCtx, rawURL)
	if err != nil {
		cancel()
		return Created{}, err
	}
	if _, err := validateRemoteURL(fetched.EffectiveURL); err != nil {
		_ = fetched.Body.Close()
		cancel()
		return Created{}, err
	}
	if err := validateMetadata("", fetched.DeclaredMIME, fetched.EffectiveURL); err != nil {
		_ = fetched.Body.Close()
		cancel()
		return Created{}, err
	}
	staged, stageErr := s.stager.Stage(stageCtx, fetched.Body)
	_ = fetched.Body.Close()
	cancel()
	if stageErr != nil {
		return Created{}, stageErr
	}

	return s.persist(ctx, CreateRequest{
		AuthorUserID: actor.UserID,
		Filter:       filter,
		Source: SourceRecord{
			Type:         SourceURL,
			StorageKey:   staged.StorageKey,
			URL:          fetched.EffectiveURL,
			DeclaredMIME: fetched.DeclaredMIME,
			ByteSize:     staged.ByteSize,
			SHA256:       staged.SHA256,
		},
	})
}

func (s *Service) stage(ctx context.Context, body io.Reader) (StagedSource, error) {
	stageCtx, cancel := context.WithTimeout(ctx, s.cfg.StageTimeout)
	defer cancel()
	return s.stager.Stage(stageCtx, body)
}

func (s *Service) persist(ctx context.Context, request CreateRequest) (Created, error) {
	dbCtx, cancel := context.WithTimeout(ctx, s.cfg.DBTimeout)
	created, err := s.repo.CreateIngestion(dbCtx, request)
	cancel()
	if err == nil {
		return created, nil
	}
	if errors.Is(err, ErrCommitOutcomeUnknown) {
		return Created{}, err
	}
	if cleanupErr := s.cleanup(ctx, request.Source.StorageKey); cleanupErr != nil {
		return Created{}, errors.Join(err, cleanupErr)
	}
	return Created{}, err
}

func (s *Service) cleanup(parent context.Context, storageKey string) error {
	base := context.WithoutCancel(parent)
	cleanupCtx, cancel := context.WithTimeout(base, s.cfg.CleanupTimeout)
	defer cancel()
	if err := s.stager.Remove(cleanupCtx, storageKey); err != nil {
		return fmt.Errorf("remove staged source %q: %w", storageKey, err)
	}
	return nil
}

func (s *Service) acquire(ctx context.Context) error {
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) release() { <-s.slots }

func validateRequest(actor Actor, filter model.ContentFilter) error {
	if actor.UserID <= 0 {
		return fmt.Errorf("%w: authenticated actor user id is required", ErrInvalidInput)
	}
	if !filter.Valid() {
		return fmt.Errorf("%w: invalid content filter", ErrInvalidInput)
	}
	return nil
}

func validateMetadata(originalName, declaredMIME, sourceURL string) error {
	if len(originalName) > maxOriginalNameBytes {
		return fmt.Errorf("%w: original name exceeds %d bytes", ErrInvalidInput, maxOriginalNameBytes)
	}
	if len(declaredMIME) > maxDeclaredMIMEBytes {
		return fmt.Errorf("%w: declared MIME type exceeds %d bytes", ErrInvalidInput, maxDeclaredMIMEBytes)
	}
	if len(sourceURL) > maxSourceURLBytes {
		return fmt.Errorf("%w: source URL exceeds %d bytes", ErrInvalidInput, maxSourceURLBytes)
	}
	return nil
}
