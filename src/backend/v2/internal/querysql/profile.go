package querysql

import (
	"fmt"
	"strings"

	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/profile"
)

func BuildProfilePosts(query profile.Query) (string, []any) {
	args := []any{query.UserID, int16(model.FilterSFW)}
	var builder strings.Builder
	builder.WriteString("SELECT ")
	builder.WriteString(signedOutPostProjection)
	builder.WriteString("\nFROM posts p")
	builder.WriteString(readyMediaJoin)
	builder.WriteString(`
WHERE p.author_user_id = $1
  AND p.release_state = 1
  AND p.deleted_at IS NULL
  AND p.content_filter = $2`)
	if query.Before > 0 {
		args = append(args, query.Before)
		fmt.Fprintf(&builder, "\n  AND p.id < $%d", len(args))
	}
	args = append(args, query.Limit+1)
	fmt.Fprintf(&builder, "\nORDER BY p.id DESC\nLIMIT $%d", len(args))
	return builder.String(), args
}
