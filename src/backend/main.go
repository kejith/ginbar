package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ginbar/api"
	"ginbar/cache"
	"ginbar/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// ── Config from environment ───────────────────────────────────────────────
	dbURL := getenv("DB_URL", "postgres://ginbar:devpassword@localhost:5432/ginbar?sslmode=disable")
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
	defer rdb.Close()
	log.Info("connected to redis", "url", redisURL)

	// Seed score cache from current DB aggregates so the first requests see
	// correct scores without a DB round-trip.
	if err = cache.PreloadScores(initCtx, rdb, pool, log); err != nil {
		log.Error("failed to preload vote scores", "err", err)
		// Non-fatal: live score reads will fall back to the DB value.
	}

	// ── Store + server ────────────────────────────────────────────────────────
	store := db.NewStore(pool)
	srv := api.NewServer(store, rdb, sessionSecret, log)

	// ── Flush worker ──────────────────────────────────────────────────────────
	// workerCtx is cancelled during graceful shutdown, triggering a final flush.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	flushDone := cache.StartFlushWorker(workerCtx, rdb, pool, 3*time.Second, log)

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

