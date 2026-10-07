package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kejith/ginbar/backend/v2/internal/adminbootstrap"
	"github.com/kejith/ginbar/backend/v2/internal/postgres"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		slog.Error("bootstrap admin failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	flags := flag.NewFlagSet("bootstrap-admin", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	userID := flags.Int64("user-id", 0, "existing numeric user ID to establish as the first admin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *userID <= 0 {
		return errors.New("--user-id must be a positive integer and no positional arguments are accepted")
	}

	databaseURL := getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	store, err := postgres.Open(ctx, postgres.Config{URL: databaseURL, MaxConns: 1})
	if err != nil {
		return err
	}
	defer store.Close()

	state, err := adminbootstrap.New(store).Bootstrap(ctx, *userID)
	if err != nil {
		return err
	}
	if !state.Admin || state.AdminGrantedByUserID == nil || *state.AdminGrantedByUserID != *userID {
		return errors.New("bootstrap returned incomplete authoritative admin state")
	}
	_, err = fmt.Fprintf(
		stdout,
		"first admin established: user_id=%d granted_by_user_id=%d\n",
		state.UserID,
		*state.AdminGrantedByUserID,
	)
	return err
}
