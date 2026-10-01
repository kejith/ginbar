package querysql

import (
	"strings"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/feed"
	"github.com/kejith/ginbar/backend/v2/internal/model"
	"github.com/kejith/ginbar/backend/v2/internal/search"
)

func TestBuildFeedIsCursorBasedFilteredAndParameterized(t *testing.T) {
	sql, args := BuildFeed(feed.Query{
		Before:  1000,
		Limit:   60,
		Filters: []model.ContentFilter{model.FilterSFW, model.FilterNSFP},
		Search: search.Query{
			IncludeTags: []string{"cat' OR true --"},
			ExcludeTags: []string{"anime"},
			Score:       &search.ScorePredicate{Op: search.ScoreGTE, Value: 100},
		},
	})
	if strings.Contains(strings.ToLower(sql), "offset") {
		t.Fatalf("feed query must not use OFFSET: %s", sql)
	}
	if strings.Contains(sql, "cat' OR true --") {
		t.Fatalf("tag was interpolated into SQL: %s", sql)
	}
	if !strings.Contains(sql, "p.id < $3") || !strings.Contains(sql, "ORDER BY p.id DESC") {
		t.Fatalf("missing cursor/order shape: %s", sql)
	}
	if !strings.Contains(sql, "p.content_filter IN ($1, $2)") {
		t.Fatalf("missing filter predicate: %s", sql)
	}
	if !strings.Contains(sql, "JOIN LATERAL") || !strings.Contains(sql, "m.post_id = p.id") || !strings.Contains(sql, "LIMIT 1") {
		t.Fatalf("missing bounded media lookup: %s", sql)
	}
	if !strings.Contains(sql, "p.id IN (") || !strings.Contains(sql, "t.normalized_name = $4") {
		t.Fatalf("missing tag-led include filter: %s", sql)
	}
	if got, want := len(args), 7; got != want {
		t.Fatalf("args=%d want %d (%#v)", got, want, args)
	}
}

func TestBuildAroundUsesBoundedSides(t *testing.T) {
	sql, args := BuildAround(feed.AroundQuery{PostID: 5000, Radius: 30, Filters: []model.ContentFilter{model.FilterSFW}})
	if !strings.Contains(sql, "p.id > $1") || !strings.Contains(sql, "p.id <= $1") {
		t.Fatalf("missing around-post bounds: %s", sql)
	}
	if !strings.Contains(sql, "LIMIT $2") || !strings.Contains(sql, "LIMIT $3") {
		t.Fatalf("missing bounded side limits: %s", sql)
	}
	if strings.Contains(sql, ") window") || !strings.Contains(sql, ") combined_posts") {
		t.Fatalf("invalid around-post derived-table alias: %s", sql)
	}
	if strings.Count(sql, "JOIN LATERAL") != 2 {
		t.Fatalf("around query should bound media lookups on both sides: %s", sql)
	}
	if got, want := len(args), 4; got != want {
		t.Fatalf("args=%d want %d (%#v)", got, want, args)
	}
}

func TestScoreOperatorIsWhitelisted(t *testing.T) {
	for op, want := range map[search.ScoreOp]string{
		search.ScoreEQ: "=", search.ScoreGT: ">", search.ScoreGTE: ">=", search.ScoreLT: "<", search.ScoreLTE: "<=",
	} {
		if got := scoreOperator(op); got != want {
			t.Fatalf("op %d: got %q want %q", op, got, want)
		}
	}
}
