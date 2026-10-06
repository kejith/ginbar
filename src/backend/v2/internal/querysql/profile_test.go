package querysql

import (
	"strings"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/profile"
)

func TestBuildProfilePostsIsAuthorBoundedAndCursorPaged(t *testing.T) {
	sql, args := BuildProfilePosts(profile.Query{UserID: 42, Before: 100, Limit: 33})
	for _, fragment := range []string{
		"p.author_user_id = $1",
		"p.release_state = 1",
		"p.deleted_at IS NULL",
		"p.content_filter = $2",
		"p.id < $3",
		"ORDER BY p.id DESC",
		"LIMIT $4",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("SQL missing %q:\n%s", fragment, sql)
		}
	}
	if len(args) != 4 || args[0] != int64(42) || args[1] != int16(0) || args[2] != int64(100) || args[3] != 34 {
		t.Fatalf("args=%#v", args)
	}
}

func TestBuildProfilePostsWithoutCursorUsesThreeArguments(t *testing.T) {
	_, args := BuildProfilePosts(profile.Query{UserID: 42, Limit: 120})
	if len(args) != 3 || args[2] != 121 {
		t.Fatalf("args=%#v", args)
	}
}
