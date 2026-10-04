package postgres

import (
	"context"
	"fmt"

	"github.com/kejith/ginbar/backend/v2/internal/duplicate"
	"github.com/kejith/ginbar/backend/v2/internal/querysql"
)

func (s *Store) ListDuplicateCandidates(ctx context.Context, q duplicate.Query) ([]duplicate.Candidate, error) {
	sql, args := querysql.BuildExactDuplicateCandidates(q)
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query duplicate candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]duplicate.Candidate, 0, q.Limit)
	for rows.Next() {
		var candidate duplicate.Candidate
		if err := rows.Scan(&candidate.PostID); err != nil {
			return nil, fmt.Errorf("scan duplicate candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read duplicate candidates: %w", err)
	}
	return candidates, nil
}
