package postgres

import (
	"context"
	"fmt"
	"testing"

	"github.com/kejith/ginbar/backend/v2/internal/duplicate"
	"github.com/kejith/ginbar/backend/v2/internal/model"
)

func TestExactDuplicateCandidatesAreBoundedAndVisibilitySafe(t *testing.T) {
	store, cleanup := testIngestionStore(t)
	defer cleanup()
	ctx := context.Background()

	var userID int64
	if err := store.pool.QueryRow(ctx, "INSERT INTO users (username) VALUES ('duplicate-user') RETURNING id").Scan(&userID); err != nil {
		t.Fatal(err)
	}

	insertPost := func(filter model.ContentFilter, released bool) int64 {
		t.Helper()
		var id int64
		if released {
			if err := store.pool.QueryRow(ctx,
				"INSERT INTO posts (author_user_id, content_filter, release_state, released_at) VALUES ($1, $2, 1, now()) RETURNING id",
				userID, int16(filter),
			).Scan(&id); err != nil {
				t.Fatal(err)
			}
		} else if err := store.pool.QueryRow(ctx,
			"INSERT INTO posts (author_user_id, content_filter) VALUES ($1, $2) RETURNING id",
			userID, int16(filter),
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	insertMedia := func(postID int64, kind int16, state int16, hash any) {
		t.Helper()
		mime := "image/avif"
		if kind == 1 {
			mime = "video/mp4"
		}
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO media (
				post_id, kind, processing_state, storage_key, mime_type,
				width, height, duration_ms, byte_size, sha256, perceptual_hash
			) VALUES ($1, $2, $3, $4, $5, 32, 32, 0, 1, $6, $7)
		`, postID, kind, state, fmt.Sprintf("media/test/%d", postID), mime, make([]byte, 32), hash); err != nil {
			t.Fatal(err)
		}
	}

	const hash int64 = -0x1234_5678_7654_321
	source := insertPost(model.FilterSFW, true)
	insertMedia(source, 0, 1, hash)

	imageCandidate := insertPost(model.FilterSFW, true)
	insertMedia(imageCandidate, 0, 1, hash)
	videoCandidate := insertPost(model.FilterSFW, true)
	insertMedia(videoCandidate, 1, 1, hash)

	nsfwCandidate := insertPost(model.FilterNSFW, true)
	insertMedia(nsfwCandidate, 0, 1, hash)
	unreleasedCandidate := insertPost(model.FilterSFW, false)
	insertMedia(unreleasedCandidate, 0, 1, hash)
	deletedCandidate := insertPost(model.FilterSFW, true)
	insertMedia(deletedCandidate, 0, 1, hash)
	if _, err := store.pool.Exec(ctx, "UPDATE posts SET deleted_at = now() WHERE id = $1", deletedCandidate); err != nil {
		t.Fatal(err)
	}
	notReadyCandidate := insertPost(model.FilterSFW, true)
	insertMedia(notReadyCandidate, 0, 0, hash)
	differentCandidate := insertPost(model.FilterSFW, true)
	insertMedia(differentCandidate, 0, 1, hash+1)

	service := duplicate.New(store)
	got, err := service.List(ctx, duplicate.Query{
		PostID:  source,
		Limit:   10,
		Filters: []model.ContentFilter{model.FilterSFW},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []duplicate.Candidate{{PostID: videoCandidate}, {PostID: imageCandidate}}
	if len(got) != len(want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates = %#v, want %#v", got, want)
		}
	}

	bounded, err := service.List(ctx, duplicate.Query{
		PostID:  source,
		Limit:   1,
		Filters: []model.ContentFilter{model.FilterSFW},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded) != 1 || bounded[0].PostID != videoCandidate {
		t.Fatalf("bounded candidates = %#v", bounded)
	}

	allVisible, err := service.List(ctx, duplicate.Query{
		PostID:  source,
		Limit:   10,
		Filters: []model.ContentFilter{model.FilterSFW, model.FilterNSFW},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(allVisible) != 3 || allVisible[0].PostID != nsfwCandidate {
		t.Fatalf("expanded visibility candidates = %#v", allVisible)
	}

	if _, err := store.pool.Exec(ctx, "UPDATE media SET perceptual_hash = NULL WHERE post_id = $1", source); err != nil {
		t.Fatal(err)
	}
	none, err := service.List(ctx, duplicate.Query{PostID: source, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("null source hash returned candidates: %#v", none)
	}
}
