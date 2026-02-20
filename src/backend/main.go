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
	"ginbar/db"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// ── Config from environment ───────────────────────────────────────────────
	dbURL := getenv("DB_URL", "postgres://ginbar:devpassword@localhost:5432/ginbar?sslmode=disable")
	port := getenv("PORT", "3000")
	sessionSecret := getenv("SESSION_SECRET", "change-me-in-prod")

	// ── Database pool ─────────────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Error("cannot create pgx pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err = pool.Ping(ctx); err != nil {
		log.Error("cannot reach postgres", "err", err)
		os.Exit(1)
	}
	log.Info("connected to postgres", "url", dbURL)

	// ── Store + server ────────────────────────────────────────────────────────
	store := db.NewStore(pool)
	srv := api.NewServer(store, sessionSecret, log)

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
	if err = srv.App.Shutdown(); err != nil {
		log.Error("shutdown error", "err", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
