package db

import (
	"context"
	"slices"

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

// PostWithVote embeds Post and adds the requesting user's current vote (0 for anon).
// This is the wire type for all cursor-based and around-post API responses.
type PostWithVote struct {
	gen.Post
	Vote int16 `json:"vote"`
}

// postWithVoteCols is the SELECT column list for a PostWithVote scan.
// Uses positional parameters: $1 = userID (for the LEFT JOIN).
const postWithVoteCols = `
    p.id, p.url, p.uploaded_filename, p.filename, p.thumbnail_filename,
    p.content_type, p.score, p.user_level, p.filter,
    p.p_hash_0, p.p_hash_1, p.p_hash_2, p.p_hash_3,
    p.user_name, p.created_at, p.deleted_at, p.dirty, p.width, p.height, p.released,
    COALESCE(pv.vote, 0)::smallint AS vote`

const postWithVoteJoin = `
    FROM posts p
    LEFT JOIN post_votes pv ON pv.post_id = p.id AND pv.user_id = $1`

func scanPostsWithVote(pool *pgxpool.Pool, ctx context.Context, q string, args ...any) ([]PostWithVote, error) {
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PostWithVote
	for rows.Next() {
		var p PostWithVote
		if err := rows.Scan(
			&p.ID, &p.Url, &p.UploadedFilename, &p.Filename, &p.ThumbnailFilename,
			&p.ContentType, &p.Score, &p.UserLevel, &p.Filter,
			&p.PHash0, &p.PHash1, &p.PHash2, &p.PHash3,
			&p.UserName, &p.CreatedAt, &p.DeletedAt, &p.Dirty, &p.Width, &p.Height, &p.Released,
			&p.Vote,
		); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

// GetPostsOlderThan returns up to limit posts with id < beforeID, ordered newest-first (DESC).
// $1=userID (0 for anon), $2=beforeID, $3=filters (text[]), $4=limit.
func (s *Store) GetPostsOlderThan(ctx context.Context, beforeID, userID int32, filters []string, limit int32) ([]PostWithVote, error) {
	q := `SELECT` + postWithVoteCols + postWithVoteJoin + `
    WHERE p.id < $2 AND p.deleted_at IS NULL AND p.dirty = FALSE AND p.released = TRUE AND p.filter = ANY($3::text[])
    ORDER BY p.id DESC
    LIMIT $4`
	return scanPostsWithVote(s.Pool, ctx, q, userID, beforeID, filters, limit)
}

// GetPostsNewerThan returns up to limit posts with id > afterID, ordered newest-first (DESC).
// Internally queries ASC and reverses so the result is DESC-ordered.
// $1=userID (0 for anon), $2=afterID, $3=filters (text[]), $4=limit.
func (s *Store) GetPostsNewerThan(ctx context.Context, afterID, userID int32, filters []string, limit int32) ([]PostWithVote, error) {
	q := `SELECT` + postWithVoteCols + postWithVoteJoin + `
    WHERE p.id > $2 AND p.deleted_at IS NULL AND p.dirty = FALSE AND p.released = TRUE AND p.filter = ANY($3::text[])
    ORDER BY p.id ASC
    LIMIT $4`
	posts, err := scanPostsWithVote(s.Pool, ctx, q, userID, afterID, filters, limit)
	if err != nil {
		return nil, err
	}
	slices.Reverse(posts) // convert ASC → DESC
	return posts, nil
}

// GetPostsAround fetches a window of posts centered on postID.
// Returns (newerDesc, target, olderDesc, hasNewer, hasOlder, err).
// newerDesc and olderDesc are both in DESC order; concat them with target in
// the middle to get the full list in DESC order.
func (s *Store) GetPostsAround(ctx context.Context, postID, userID int32, filters []string, limit int32) (
	newer []PostWithVote, target PostWithVote, older []PostWithVote,
	hasNewer bool, hasOlder bool, err error,
) {
	// Fetch limit+1 in each direction to detect has_newer / has_older.
	fetchLimit := limit + 1

	newer, err = s.GetPostsNewerThan(ctx, postID, userID, filters, fetchLimit)
	if err != nil {
		return
	}
	if len(newer) > int(limit) {
		hasNewer = true
		newer = newer[:limit] // keep DESC order, trim oldest of the newer batch
	}

	older, err = s.GetPostsOlderThan(ctx, postID, userID, filters, fetchLimit)
	if err != nil {
		return
	}
	if len(older) > int(limit) {
		hasOlder = true
		older = older[:limit]
	}

	// Fetch the target post itself.
	tq := `SELECT` + postWithVoteCols + postWithVoteJoin + `
    WHERE p.id = $2 AND p.deleted_at IS NULL AND p.dirty = FALSE AND p.released = TRUE AND p.filter = ANY($3::text[])`
	rows, qErr := s.Pool.Query(ctx, tq, userID, postID, filters)
	if qErr != nil {
		err = qErr
		return
	}
	defer rows.Close()
	if rows.Next() {
		if scanErr := rows.Scan(
			&target.ID, &target.Url, &target.UploadedFilename, &target.Filename, &target.ThumbnailFilename,
			&target.ContentType, &target.Score, &target.UserLevel, &target.Filter,
			&target.PHash0, &target.PHash1, &target.PHash2, &target.PHash3,
			&target.UserName, &target.CreatedAt, &target.DeletedAt, &target.Dirty, &target.Width, &target.Height, &target.Released,
			&target.Vote,
		); scanErr != nil {
			err = scanErr
			return
		}
	}
	err = rows.Err()
	return
}

// GetPostOffset returns the 0-based position of the given post in the default
// list (ORDER BY id DESC). It counts how many visible posts have a higher id,
// which equals the post's 0-based offset in the sorted list.
func (s *Store) GetPostOffset(ctx context.Context, postID int32, filters []string) (int32, error) {
	const q = `
SELECT COUNT(*)::int
FROM posts
WHERE id > $1
  AND deleted_at IS NULL
  AND dirty = FALSE
  AND released = TRUE
  AND filter = ANY($2::text[])
`
	var n int32
	err := s.Pool.QueryRow(ctx, q, postID, filters).Scan(&n)
	return n, err
}

// GetPostIDsWithTagID returns the IDs of all posts that have the given tag.
func (s *Store) GetPostIDsWithTagID(ctx context.Context, tagID int32) ([]int32, error) {
	rows, err := s.Pool.Query(ctx,
		"SELECT post_id FROM post_tags WHERE tag_id = $1",
		tagID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int32
	for rows.Next() {
		var id int32
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
