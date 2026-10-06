package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/httpapi"
	"github.com/kejith/ginbar/backend/v2/internal/ingest"
	"github.com/kejith/ginbar/backend/v2/internal/postgres"
)

const (
	defaultMediaSourceMaxBytes      int64 = 256 << 20
	defaultIngestMaxConcurrent            = 4
	defaultIngestRequestTimeout           = 2 * time.Minute
	defaultIngestStageTimeout             = 90 * time.Second
	defaultIngestDBTimeout                = 5 * time.Second
	defaultIngestCleanupTimeout           = 5 * time.Second
	defaultURLConnectTimeout              = 5 * time.Second
	defaultURLResponseHeaderTimeout       = 10 * time.Second
	defaultURLMaxRedirects                = 5
)

type runtimeConfig struct {
	databaseURL     string
	dbMaxConns      int32
	listenAddr      string
	mediaSourceRoot string
	mediaSourceMax  int64
	api             httpapi.Config
	ingest          ingest.Config
	urlFetcher      ingest.URLFetcherConfig
}

func main() {
	if err := run(); err != nil {
		slog.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadRuntimeConfig(os.Getenv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := postgres.Open(ctx, postgres.Config{URL: cfg.databaseURL, MaxConns: cfg.dbMaxConns})
	if err != nil {
		return err
	}
	defer store.Close()

	localStore, err := ingest.NewLocalStore(cfg.mediaSourceRoot, cfg.mediaSourceMax)
	if err != nil {
		return fmt.Errorf("configure ingestion source storage: %w", err)
	}
	fetcher, err := ingest.NewHTTPURLFetcher(cfg.urlFetcher)
	if err != nil {
		return fmt.Errorf("configure URL ingestion: %w", err)
	}
	ingestService, err := ingest.New(store, localStore, fetcher, cfg.ingest)
	if err != nil {
		return fmt.Errorf("configure ingestion service: %w", err)
	}
	cfg.api.Ingest = ingestService

	server := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           httpapi.NewWithConfig(store, cfg.api).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		WriteTimeout:      ingestWriteTimeout(cfg.api.IngestRequestTimeout),
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening",
			"addr", cfg.listenAddr,
			"dbMaxConns", cfg.dbMaxConns,
			"ingestMaxConcurrent", cfg.ingest.MaxConcurrent,
			"mediaSourceMaxBytes", cfg.mediaSourceMax,
		)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func loadRuntimeConfig(getenv func(string) string) (runtimeConfig, error) {
	cfg := runtimeConfig{
		dbMaxConns:     8,
		listenAddr:     ":8080",
		mediaSourceMax: defaultMediaSourceMaxBytes,
		api:            httpapi.DefaultConfig(),
		ingest:         ingest.Config{StageTimeout: defaultIngestStageTimeout, DBTimeout: defaultIngestDBTimeout, CleanupTimeout: defaultIngestCleanupTimeout, MaxConcurrent: defaultIngestMaxConcurrent},
		urlFetcher:     ingest.URLFetcherConfig{MaxBytes: defaultMediaSourceMaxBytes, ConnectTimeout: defaultURLConnectTimeout, ResponseHeaderTimeout: defaultURLResponseHeaderTimeout, MaxRedirects: defaultURLMaxRedirects},
	}
	cfg.api.IngestRequestTimeout = defaultIngestRequestTimeout

	cfg.databaseURL = getenv("DATABASE_URL")
	if cfg.databaseURL == "" {
		return runtimeConfig{}, errors.New("DATABASE_URL is required")
	}
	cfg.mediaSourceRoot = getenv("GINBAR_MEDIA_SOURCE_ROOT")
	if cfg.mediaSourceRoot == "" {
		return runtimeConfig{}, errors.New("GINBAR_MEDIA_SOURCE_ROOT is required")
	}
	if raw := getenv("LISTEN_ADDR"); raw != "" {
		cfg.listenAddr = raw
	}

	var err error
	if cfg.dbMaxConns, err = envPositiveInt32(getenv, "DB_MAX_CONNS", cfg.dbMaxConns); err != nil {
		return runtimeConfig{}, err
	}
	if cfg.mediaSourceMax, err = envPositiveInt64(getenv, "GINBAR_MEDIA_SOURCE_MAX_BYTES", cfg.mediaSourceMax); err != nil {
		return runtimeConfig{}, err
	}
	cfg.urlFetcher.MaxBytes = cfg.mediaSourceMax
	if cfg.ingest.MaxConcurrent, err = envPositiveInt(getenv, "GINBAR_INGEST_MAX_CONCURRENT", cfg.ingest.MaxConcurrent); err != nil {
		return runtimeConfig{}, err
	}
	if cfg.api.IngestRequestTimeout, err = envPositiveDuration(getenv, "GINBAR_INGEST_REQUEST_TIMEOUT", cfg.api.IngestRequestTimeout); err != nil {
		return runtimeConfig{}, err
	}
	if cfg.ingest.StageTimeout, err = envPositiveDuration(getenv, "GINBAR_INGEST_STAGE_TIMEOUT", cfg.ingest.StageTimeout); err != nil {
		return runtimeConfig{}, err
	}
	if cfg.ingest.DBTimeout, err = envPositiveDuration(getenv, "GINBAR_INGEST_DB_TIMEOUT", cfg.ingest.DBTimeout); err != nil {
		return runtimeConfig{}, err
	}
	if cfg.ingest.CleanupTimeout, err = envPositiveDuration(getenv, "GINBAR_INGEST_CLEANUP_TIMEOUT", cfg.ingest.CleanupTimeout); err != nil {
		return runtimeConfig{}, err
	}
	if cfg.urlFetcher.ConnectTimeout, err = envPositiveDuration(getenv, "GINBAR_URL_CONNECT_TIMEOUT", cfg.urlFetcher.ConnectTimeout); err != nil {
		return runtimeConfig{}, err
	}
	if cfg.urlFetcher.ResponseHeaderTimeout, err = envPositiveDuration(getenv, "GINBAR_URL_RESPONSE_HEADER_TIMEOUT", cfg.urlFetcher.ResponseHeaderTimeout); err != nil {
		return runtimeConfig{}, err
	}
	if cfg.urlFetcher.MaxRedirects, err = envNonNegativeInt(getenv, "GINBAR_URL_MAX_REDIRECTS", cfg.urlFetcher.MaxRedirects); err != nil {
		return runtimeConfig{}, err
	}
	if raw := getenv("GINBAR_SESSION_COOKIE_SECURE"); raw != "" {
		secure, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return runtimeConfig{}, errors.New("GINBAR_SESSION_COOKIE_SECURE must be a boolean")
		}
		cfg.api.CookieSecure = secure
	}

	if cfg.ingest.DBTimeout >= cfg.api.IngestRequestTimeout || cfg.ingest.StageTimeout >= cfg.api.IngestRequestTimeout-cfg.ingest.DBTimeout {
		return runtimeConfig{}, errors.New("GINBAR_INGEST_REQUEST_TIMEOUT must exceed GINBAR_INGEST_STAGE_TIMEOUT plus GINBAR_INGEST_DB_TIMEOUT")
	}
	if cfg.urlFetcher.ConnectTimeout > cfg.ingest.StageTimeout {
		return runtimeConfig{}, errors.New("GINBAR_URL_CONNECT_TIMEOUT must not exceed GINBAR_INGEST_STAGE_TIMEOUT")
	}
	if cfg.urlFetcher.ResponseHeaderTimeout > cfg.ingest.StageTimeout {
		return runtimeConfig{}, errors.New("GINBAR_URL_RESPONSE_HEADER_TIMEOUT must not exceed GINBAR_INGEST_STAGE_TIMEOUT")
	}
	return cfg, nil
}

func envPositiveInt(getenv func(string) string, key string, fallback int) (int, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func envNonNegativeInt(getenv func(string) string, key string, fallback int) (int, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return value, nil
}

func envPositiveInt32(getenv func(string) string, key string, fallback int32) (int32, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return int32(value), nil
}

func envPositiveInt64(getenv func(string) string, key string, fallback int64) (int64, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func envPositiveDuration(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func ingestWriteTimeout(requestTimeout time.Duration) time.Duration {
	const minimum = 10 * time.Second
	const responseSlack = 5 * time.Second
	if requestTimeout > time.Duration(1<<63-1)-responseSlack {
		return requestTimeout
	}
	value := requestTimeout + responseSlack
	if value < minimum {
		return minimum
	}
	return value
}
