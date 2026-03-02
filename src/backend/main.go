package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"wallium/api"
	"wallium/cache"
	"wallium/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	log := buildLogger()

	// ── Config from environment ───────────────────────────────────────────────
	dbURL := getenv("DB_URL", "postgres://wallium:devpassword@localhost:5432/wallium?sslmode=disable")
	redisURL := getenv("REDIS_URL", "redis://localhost:6379")
	port := getenv("PORT", "3000")
	sessionSecret := getenv("SESSION_SECRET", "change-me-in-prod")

	// ── Database pool ─────────────────────────────────────────────────────────
	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer initCancel()

	pool, err := pgxpool.New(initCtx, dbURL)
	if err != nil {
		log.Error("cannot create pgx pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err = pool.Ping(initCtx); err != nil {
		log.Error("cannot reach postgres", "err", err)
		os.Exit(1)
	}
	log.Info("connected to postgres", "url", dbURL)

	// ── Redis client ──────────────────────────────────────────────────────────
	rdb, err := cache.NewClient(initCtx, redisURL)
	if err != nil {
		log.Error("cannot connect to redis", "err", err)
		os.Exit(1)
	}
	defer func() { _ = rdb.Close() }()
	log.Info("connected to redis", "url", redisURL)

	// Seed score cache from current DB aggregates so the first requests see
	// correct scores without a DB round-trip.
	if err = cache.PreloadScores(initCtx, rdb, pool, log); err != nil {
		log.Error("failed to preload vote scores", "err", err)
		// Non-fatal: live score reads will fall back to the DB value.
	}

	// ── Store + server ────────────────────────────────────────────────────────
	store := db.NewStore(pool)

	// Ensure the default admin user exists (creates it if absent, promotes if
	// level was downgraded).  This is idempotent and runs after every startup.
	adminPassword := getenv("ADMIN_PASSWORD", "admin")
	if seedErr := store.EnsureAdminUser(initCtx, adminPassword, log); seedErr != nil {
		log.Error("failed to seed admin user", "err", seedErr)
		// Non-fatal: the application still runs without admin seeding.
	}

	srv := api.NewServer(store, rdb, sessionSecret, log)

	// ── Flush worker + queue processor ───────────────────────────────────────
	// workerCtx is cancelled during graceful shutdown, triggering a final flush.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	flushDone := cache.StartFlushWorker(workerCtx, rdb, pool, 3*time.Second, log)

	// Start the dirty-post processing queue.
	srv.Start(workerCtx)

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		addr := fmt.Sprintf(":%s", port)
		log.Info("listening", "addr", addr)
		if listenErr := srv.App.Listen(addr); listenErr != nil {
			log.Error("server error", "err", listenErr)
		}
	}()

	<-quit
	log.Info("shutting down")

	// Stop accepting new requests first.
	if err = srv.App.Shutdown(); err != nil {
		log.Error("shutdown error", "err", err)
	}

	// Then drain the vote buffer into Postgres.
	workerCancel()
	<-flushDone
	log.Info("vote buffer flushed — bye")
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// buildLogger constructs an *slog.Logger from environment variables:
//
//	LOG_LEVEL   – debug | info | warn | error  (default: info)
//	LOG_FORMAT  – json | text                  (default: text)
//	LOG_FILE    – path to append log lines to  (default: stdout only)
func buildLogger() *slog.Logger {
	// ── Level ─────────────────────────────────────────────────────────────────
	var levelVar slog.LevelVar // default INFO

	switch strings.ToLower(getenv("LOG_LEVEL", "info")) {
	case "debug":
		levelVar.Set(slog.LevelDebug)
	case "warn", "warning":
		levelVar.Set(slog.LevelWarn)
	case "error":
		levelVar.Set(slog.LevelError)
	default:
		levelVar.Set(slog.LevelInfo)
	}

	// ── Output writer ─────────────────────────────────────────────────────────
	var out io.Writer = os.Stdout

	if logFile := os.Getenv("LOG_FILE"); logFile != "" {
		f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			out = io.MultiWriter(os.Stdout, f)
		} else {
			// Can't write to bootstrap a logger yet, fall back gracefully.
			_, _ = fmt.Fprintf(os.Stderr, "[logger] cannot open LOG_FILE %q: %v — logging to stdout only\n", logFile, err)
		}
	}

	// ── Handler ───────────────────────────────────────────────────────────────
	opts := &slog.HandlerOptions{
		Level:     &levelVar,
		AddSource: true,
	}

	var handler slog.Handler
	if strings.ToLower(getenv("LOG_FORMAT", "text")) == "json" {
		handler = slog.NewJSONHandler(out, opts)
	} else {
		handler = slog.NewTextHandler(out, opts)
	}

	return slog.New(handler)
}

