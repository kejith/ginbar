package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/querysql"
)

type Config struct {
	URL      string
	MaxConns int32
}

type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, cfg Config) (*Store, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = cfg.MaxConns
	}
	poolConfig.MinConns = 0
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) ListFeed(ctx context.Context, query feed.Query) ([]model.PostSummary, error) {
	sql, args := querysql.BuildFeed(query)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query feed: %w", err)
	}
	defer rows.Close()

	posts := make([]model.PostSummary, 0, query.Limit+1)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, fmt.Errorf("scan feed row: %w", err)
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read feed rows: %w", err)
	}
	return posts, nil
}

func (s *Store) AroundPost(ctx context.Context, query feed.AroundQuery) ([]model.PostSummary, error) {
	sql, args := querysql.BuildAround(query)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query around post: %w", err)
	}
	defer rows.Close()

	posts := make([]model.PostSummary, 0, query.Radius*2+1)
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, fmt.Errorf("scan around-post row: %w", err)
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read around-post rows: %w", err)
	}
	return posts, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanPost(row rowScanner) (model.PostSummary, error) {
	var post model.PostSummary
	var filter int16
	var userVote int16
	var kind int16
	if err := row.Scan(
		&post.ID,
		&post.AuthorID,
		&filter,
		&post.Score,
		&userVote,
		&post.CreatedAt,
		&kind,
		&post.Media.StorageKey,
		&post.Media.MIMEType,
		&post.Media.Width,
		&post.Media.Height,
		&post.Media.DurationMS,
	); err != nil {
		return model.PostSummary{}, err
	}
	post.Filter = model.ContentFilter(filter)
	post.UserVote = model.PostVote(userVote)
	post.Media.Kind = model.MediaKind(kind)
	return post, nil
}
