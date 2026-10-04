package querysql

import (
	"github.com/kejith/ginbar/backend/v2/internal/duplicate"
)

const exactDuplicateCandidatesSQL = `SELECT m.post_id
FROM media m
JOIN posts p ON p.id = m.post_id
WHERE m.perceptual_hash = (
    SELECT perceptual_hash
    FROM media
    WHERE post_id = $1
      AND processing_state = 1
      AND perceptual_hash IS NOT NULL
)
  AND m.perceptual_hash IS NOT NULL
  AND m.post_id <> $1
  AND m.processing_state = 1
  AND p.release_state = 1
  AND p.deleted_at IS NULL
  AND p.content_filter = ANY($2::smallint[])
ORDER BY m.post_id DESC
LIMIT $3`

func BuildExactDuplicateCandidates(q duplicate.Query) (string, []any) {
	filters := make([]int16, len(q.Filters))
	for i, filter := range q.Filters {
		filters[i] = int16(filter)
	}
	return exactDuplicateCandidatesSQL, []any{q.PostID, filters, q.Limit}
}
