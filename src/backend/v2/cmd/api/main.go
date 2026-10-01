package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kejith/ginbar/backend/v2/internal/httpapi"
	"github.com/kejith/ginbar/backend/v2/internal/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	maxConns := int32(8)
	if raw := os.Getenv("DB_MAX_CONNS"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 1 {
			return errors.New("DB_MAX_CONNS must be a positive integer")
		}
		maxConns = int32(parsed)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, MaxConns: maxConns})
	if err != nil {
		return err
	}
	defer store.Close()

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           httpapi.New(store).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", addr, "dbMaxConns", maxConns)
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
