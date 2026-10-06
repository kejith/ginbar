package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/profile"
	"github.com/kejith/ginbar/backend/v2/internal/querysql"
)

func (s *Store) LoadPublicUser(ctx context.Context, userID int64) (profile.User, error) {
	var user profile.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, created_at
		FROM users
		WHERE id = $1 AND status = $2
	`, userID, profile.UserStatusActive).Scan(&user.ID, &user.Username, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return profile.User{}, profile.ErrUserNotFound
	}
	if err != nil {
		return profile.User{}, fmt.Errorf("load public profile user: %w", err)
	}
	return user, nil
}

func (s *Store) ListProfilePosts(ctx context.Context, query profile.Query) ([]model.PostSummary, error) {
	sql, args := querysql.BuildProfilePosts(query)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query profile posts: %w", err)
	}
	defer rows.Close()

	posts := make([]model.PostSummary, 0, query.Limit+1)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, fmt.Errorf("scan profile post: %w", err)
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read profile posts: %w", err)
	}
	return posts, nil
}
