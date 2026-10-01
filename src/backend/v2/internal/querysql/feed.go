package querysql

import (
	"fmt"
	"strings"

	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/search"
)

const postProjection = `
p.id,
	p.author_user_id,
	p.content_filter,
	p.score,
	p.created_at,
	m.kind,
	m.storage_key,
	m.mime_type,
	m.width,
	m.height,
	m.duration_ms`

const readyMediaJoin = `
JOIN LATERAL (
    SELECT m.kind, m.storage_key, m.mime_type, m.width, m.height, m.duration_ms
    FROM media m
    WHERE m.post_id = p.id AND m.processing_state = 1
    LIMIT 1
) m ON true`

func BuildFeed(q feed.Query) (string, []any) {
	args := make([]any, 0, 8+len(q.Search.IncludeTags)+len(q.Search.ExcludeTags))
	var b strings.Builder
	b.WriteString("SELECT ")
	b.WriteString(postProjection)
	b.WriteString("\nFROM posts p")
	b.WriteString(readyMediaJoin)
	b.WriteString(`
WHERE p.release_state = 1 AND p.deleted_at IS NULL`)
	appendFilterSQL(&b, &args, q.Filters)
	if q.Before > 0 {
		args = append(args, q.Before)
		fmt.Fprintf(&b, "\nAND p.id < $%d", len(args))
	}
	appendSearchSQL(&b, &args, q.Search)
	args = append(args, q.Limit+1)
	fmt.Fprintf(&b, "\nORDER BY p.id DESC\nLIMIT $%d", len(args))
	return b.String(), args
}

func BuildAround(q feed.AroundQuery) (string, []any) {
	args := []any{q.PostID, q.Radius}
	var filters strings.Builder
	appendFilterSQL(&filters, &args, q.Filters)
	appendSearchSQL(&filters, &args, q.Search)
	filterSQL := filters.String()

	sql := `WITH newer AS (
    SELECT ` + postProjection + `
    FROM posts p` + readyMediaJoin + `
    WHERE p.release_state = 1 AND p.deleted_at IS NULL
      AND p.id > $1` + filterSQL + `
    ORDER BY p.id ASC
    LIMIT $2
), selected AS (
    SELECT ` + postProjection + `
    FROM posts p` + readyMediaJoin + `
    WHERE p.release_state = 1 AND p.deleted_at IS NULL
      AND p.id = $1
), older AS (
    SELECT ` + postProjection + `
    FROM posts p` + readyMediaJoin + `
    WHERE p.release_state = 1 AND p.deleted_at IS NULL
      AND p.id < $1` + filterSQL + `
    ORDER BY p.id DESC
    LIMIT $2
)
SELECT * FROM (
    SELECT * FROM newer
    UNION ALL
    SELECT * FROM selected
    UNION ALL
    SELECT * FROM older
) combined_posts
ORDER BY id DESC`
	return sql, args
}

func appendFilterSQL(b *strings.Builder, args *[]any, filters []model.ContentFilter) {
	if len(filters) == 0 {
		filters = []model.ContentFilter{model.FilterSFW}
	}
	b.WriteString("\nAND p.content_filter IN (")
	for i, filter := range filters {
		if i > 0 {
			b.WriteString(", ")
		}
		*args = append(*args, int16(filter))
		fmt.Fprintf(b, "$%d", len(*args))
	}
	b.WriteByte(')')
}

func appendSearchSQL(b *strings.Builder, args *[]any, query search.Query) {
	for _, tag := range query.IncludeTags {
		*args = append(*args, tag)
		fmt.Fprintf(b, `
AND p.id IN (
    SELECT pt.post_id
    FROM post_tags pt
    WHERE pt.removed_at IS NULL
      AND pt.tag_id = (
          SELECT t.id
          FROM tags t
          WHERE t.normalized_name = $%d
      )
)`, len(*args))
	}
	if len(query.ExcludeTags) > 0 {
		*args = append(*args, query.ExcludeTags)
		fmt.Fprintf(b, `
AND NOT EXISTS (
    SELECT 1
    FROM post_tags pt
    JOIN tags t ON t.id = pt.tag_id
    WHERE pt.post_id = p.id
      AND pt.removed_at IS NULL
      AND t.normalized_name = ANY($%d::text[])
)`, len(*args))
	}
	if query.Score != nil {
		*args = append(*args, query.Score.Value)
		fmt.Fprintf(b, "\nAND p.score %s $%d", scoreOperator(query.Score.Op), len(*args))
	}
}

func scoreOperator(op search.ScoreOp) string {
	switch op {
	case search.ScoreEQ:
		return "="
	case search.ScoreGT:
		return ">"
	case search.ScoreGTE:
		return ">="
	case search.ScoreLT:
		return "<"
	case search.ScoreLTE:
		return "<="
	default:
		panic("invalid score operator")
	}
}
