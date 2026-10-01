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
	if !strings.Contains(sql, "p.id IN (") || !strings.Contains(sql, "pt.tag_id = (") || !strings.Contains(sql, "t.normalized_name = $4") {
		t.Fatalf("missing tag-id resolved include filter: %s", sql)
	}
	if strings.Contains(sql, "JOIN tags t ON t.id = pt.tag_id\n    WHERE pt.removed_at IS NULL\n      AND t.normalized_name = $4") {
		t.Fatalf("include filter still joins tags inside the post-tag scan: %s", sql)
	}
	if got, want := len(args), 7; got != want {
		t.Fatalf("args=%d want %d (%#v)", got, want, args)
	}
}

func TestBuildAroundKeepsSelectedPostOutsideSearchButInsideVisibility(t *testing.T) {
	sql, args := BuildAround(feed.AroundQuery{
		PostID:  5000,
		Radius:  30,
		Filters: []model.ContentFilter{model.FilterSFW},
		Search: search.Query{
			IncludeTags: []string{"tag-42"},
			Score:       &search.ScorePredicate{Op: search.ScoreGTE, Value: 100},
		},
	})
	if !strings.Contains(sql, "p.id > $1") || !strings.Contains(sql, "p.id < $1") || strings.Contains(sql, "p.id <= $1") {
		t.Fatalf("missing strict around-post bounds: %s", sql)
	}
	if strings.Count(sql, "LIMIT $2") != 2 {
		t.Fatalf("around query should bound both context sides by radius: %s", sql)
	}
	selectedStart := strings.Index(sql, ", selected AS (")
	olderStart := strings.Index(sql, ", older AS (")
	if selectedStart < 0 || olderStart <= selectedStart {
		t.Fatalf("missing selected-post branch: %s", sql)
	}
	selectedSQL := sql[selectedStart:olderStart]
	if !strings.Contains(selectedSQL, "p.id = $1") {
		t.Fatalf("selected branch does not target the canonical post: %s", selectedSQL)
	}
	if !strings.Contains(selectedSQL, "AND p.content_filter IN ($3)") {
		t.Fatalf("selected post must remain constrained by allowed visibility: %s", selectedSQL)
	}
	if strings.Contains(selectedSQL, "pt.tag_id") || strings.Contains(selectedSQL, "AND p.score") {
		t.Fatalf("selected post must not be hidden by surrounding search predicates: %s", selectedSQL)
	}
	if strings.Count(sql, "AND p.content_filter IN ($3)") != 3 {
		t.Fatalf("allowed visibility must apply to newer, selected, and older branches: %s", sql)
	}
	if strings.Count(sql, "pt.tag_id = (") != 2 || strings.Count(sql, "AND p.score >= $5") != 2 {
		t.Fatalf("search predicates should apply to newer and older branches only: %s", sql)
	}
	if strings.Contains(sql, ") window") || !strings.Contains(sql, ") combined_posts") {
		t.Fatalf("invalid around-post derived-table alias: %s", sql)
	}
	if strings.Count(sql, "JOIN LATERAL") != 3 {
		t.Fatalf("around query should bound media lookups for newer, selected, and older branches: %s", sql)
	}
	if got, want := len(args), 5; got != want {
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
