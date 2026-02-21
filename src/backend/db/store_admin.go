package db

import (
	"context"
	"errors"
	"log/slog"

	gen "ginbar/db/gen"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const adminLevelValue = int32(10)

// EnsureAdminUser guarantees that an admin user named "admin" exists in the
// database with level >= adminLevelValue.  It is safe to call on every
// startup (including after a dev-clean); it is idempotent.
//
// The password is only applied when creating the user for the first time.  To
// change an existing admin's password use the normal password-update flow.
func (s *Store) EnsureAdminUser(ctx context.Context, password string, log *slog.Logger) error {
	user, err := s.GetUserByName(ctx, "admin")
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		// No admin user yet — create one.
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if hashErr != nil {
			return hashErr
		}

		created, createErr := s.CreateUser(ctx, gen.CreateUserParams{
			Name:     "admin",
			Email:    "admin@localhost",
			Password: string(hash),
		})
		if createErr != nil {
			return createErr
		}

		// Promote to admin level.
		_, updateErr := s.UpdateUserLevel(ctx, gen.UpdateUserLevelParams{
			Level: adminLevelValue,
			ID:    created.ID,
		})
		if updateErr != nil {
			return updateErr
		}

		log.Info("admin user created", "id", created.ID)
		return nil
	}

	// User exists — ensure level is at least adminLevelValue.
	if user.Level < adminLevelValue {
		_, updateErr := s.UpdateUserLevel(ctx, gen.UpdateUserLevelParams{
			Level: adminLevelValue,
			ID:    user.ID,
		})
		if updateErr != nil {
			return updateErr
		}
		log.Info("admin user promoted to level 10", "id", user.ID)
	} else {
		log.Info("admin user OK", "id", user.ID, "level", user.Level)
	}
	return nil
}
