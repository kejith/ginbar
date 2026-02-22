package db

import (
	gen "wallium/db/gen"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store composes the sqlc-generated Queries with a connection pool.
type Store struct {
	*gen.Queries
	Pool *pgxpool.Pool
}

// NewStore creates a Store from a pgxpool.Pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		Queries: gen.New(pool),
		Pool:    pool,
	}
}
