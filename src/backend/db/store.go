package db

import (
	"context"
	"fmt"

	gen "ginbar/db/gen"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store composes the sqlc-generated Queries with a connection pool and adds
// transaction support.
type Store struct {
	*gen.Queries
	pool *pgxpool.Pool
}

// NewStore creates a Store from a pgxpool.Pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Queries: gen.New(pool),
		pool:    pool,
	}
}

// Pool returns the underlying connection pool (useful for health checks, etc.).
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// ExecTx runs fn inside a single serialisable transaction.
// If fn returns an error the transaction is rolled back; otherwise it is
// committed.
func (s *Store) ExecTx(ctx context.Context, fn func(*gen.Queries) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	q := gen.New(tx)
	if err = fn(q); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("tx err: %w, rollback err: %v", err, rbErr)
		}
		return err
	}
	return tx.Commit(ctx)
}
