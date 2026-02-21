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

// GetPostsMissingDimensions returns all non-deleted, non-dirty posts where
// width or height is still 0 (i.e. uploaded before the dimension columns were
// added).
func (s *Store) GetPostsMissingDimensions(ctx context.Context) ([]gen.Post, error) {
	const q = `
SELECT id, url, uploaded_filename, filename, thumbnail_filename, content_type,
       score, user_level, p_hash_0, p_hash_1, p_hash_2, p_hash_3,
       user_name, created_at, deleted_at, dirty, width, height
FROM posts
WHERE (width = 0 OR height = 0) AND deleted_at IS NULL AND dirty = FALSE
ORDER BY id
`
	rows, err := s.Pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []gen.Post
	for rows.Next() {
		var p gen.Post
		if err := rows.Scan(
			&p.ID, &p.Url, &p.UploadedFilename, &p.Filename, &p.ThumbnailFilename,
			&p.ContentType, &p.Score, &p.UserLevel,
			&p.PHash0, &p.PHash1, &p.PHash2, &p.PHash3,
			&p.UserName, &p.CreatedAt, &p.DeletedAt, &p.Dirty,
			&p.Width, &p.Height,
		); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

// UpdatePostDimensions sets width and height for a single post.
func (s *Store) UpdatePostDimensions(ctx context.Context, id, width, height int32) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE posts SET width = $1, height = $2 WHERE id = $3`,
		width, height, id,
	)
	return err
}
