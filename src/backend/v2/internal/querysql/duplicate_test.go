package querysql

import (
	"strings"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/duplicate"
)

func TestExactDuplicateCandidatesSQLUsesScalarSourceHash(t *testing.T) {
	sql, args := BuildExactDuplicateCandidates(duplicate.Query{PostID: 42, Limit: 20})

	for _, required := range []string{
		"m.perceptual_hash = (\n    SELECT perceptual_hash\n    FROM media",
		"m.perceptual_hash IS NOT NULL",
		"ORDER BY m.post_id DESC",
		"LIMIT $3",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("duplicate query missing %q", required)
		}
	}
	if strings.Contains(sql, "JOIN media m ON m.perceptual_hash") {
		t.Fatal("duplicate query must keep the source hash scalar so the ordered hash index remains usable")
	}
	if len(args) != 3 || args[0] != int64(42) || args[2] != 20 {
		t.Fatalf("unexpected args: %#v", args)
	}
}
