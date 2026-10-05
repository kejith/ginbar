package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kejith/ginbar/backend/v2/internal/tag"
)

const signedOutPostTagsSQL = `
SELECT COALESCE(t.id, 0), COALESCE(t.name, ''), false AS can_remove
FROM posts p
LEFT JOIN post_tags pt
  ON pt.post_id = p.id
 AND pt.removed_at IS NULL
LEFT JOIN tags t ON t.id = pt.tag_id
WHERE p.id = $1
  AND p.release_state = 1
  AND p.deleted_at IS NULL
ORDER BY t.normalized_name NULLS LAST, t.id`

const signedInPostTagsSQL = `
SELECT COALESCE(t.id, 0), COALESCE(t.name, ''),
       EXISTS (
           SELECT 1
           FROM user_roles ur
           WHERE ur.user_id = $2
             AND ur.role IN ($3, $4)
       ) AS can_remove
FROM posts p
LEFT JOIN post_tags pt
  ON pt.post_id = p.id
 AND pt.removed_at IS NULL
LEFT JOIN tags t ON t.id = pt.tag_id
WHERE p.id = $1
  AND p.release_state = 1
  AND p.deleted_at IS NULL
ORDER BY t.normalized_name NULLS LAST, t.id`

type tagQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *Store) LoadPostTags(ctx context.Context, postID, viewerUserID int64) (tag.Snapshot, error) {
	return loadPostTags(ctx, s.pool, postID, viewerUserID)
}

func (s *Store) AddPostTag(ctx context.Context, userID, postID int64, name tag.Name) (tag.Snapshot, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return tag.Snapshot{}, fmt.Errorf("begin add-tag transaction: %w", err)
	}
	defer rollbackWithTimeout(tx)

	if err := lockTaggablePost(ctx, tx, postID); err != nil {
		return tag.Snapshot{}, err
	}

	tagID, err := resolveTagID(ctx, tx, userID, name)
	if err != nil {
		return tag.Snapshot{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_tags (post_id, tag_id, added_by_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (post_id, tag_id) DO UPDATE
		SET added_by_user_id = EXCLUDED.added_by_user_id,
		    removed_by_user_id = NULL,
		    removed_at = NULL,
		    created_at = now()
		WHERE post_tags.removed_at IS NOT NULL
	`, postID, tagID, userID); err != nil {
		return tag.Snapshot{}, fmt.Errorf("attach tag to post: %w", err)
	}

	snapshot, err := loadPostTags(ctx, tx, postID, userID)
	if err != nil {
		return tag.Snapshot{}, err
	}
	if err := commitTagTransaction(ctx, tx, "add tag"); err != nil {
		return tag.Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Store) RemovePostTag(ctx context.Context, userID, postID, tagID int64) (tag.Snapshot, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return tag.Snapshot{}, fmt.Errorf("begin remove-tag transaction: %w", err)
	}
	defer rollbackWithTimeout(tx)

	var role int16
	if err := tx.QueryRow(ctx, `
		SELECT role
		FROM user_roles
		WHERE user_id = $1
		  AND role IN ($2, $3)
		ORDER BY role DESC
		LIMIT 1
		FOR SHARE
	`, userID, tag.RoleModerator, tag.RoleAdmin).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tag.Snapshot{}, tag.ErrForbidden
		}
		return tag.Snapshot{}, fmt.Errorf("authorize tag removal: %w", err)
	}

	if err := lockTaggablePost(ctx, tx, postID); err != nil {
		return tag.Snapshot{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE post_tags
		SET removed_by_user_id = $1,
		    removed_at = now()
		WHERE post_id = $2
		  AND tag_id = $3
		  AND removed_at IS NULL
	`, userID, postID, tagID); err != nil {
		return tag.Snapshot{}, fmt.Errorf("remove tag from post: %w", err)
	}

	snapshot, err := loadPostTags(ctx, tx, postID, userID)
	if err != nil {
		return tag.Snapshot{}, err
	}
	if err := commitTagTransaction(ctx, tx, "remove tag"); err != nil {
		return tag.Snapshot{}, err
	}
	return snapshot, nil
}

func lockTaggablePost(ctx context.Context, tx pgx.Tx, postID int64) error {
	var lockedID int64
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM posts
		WHERE id = $1
		  AND release_state = 1
		  AND deleted_at IS NULL
		FOR SHARE
	`, postID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return tag.ErrPostNotFound
		}
		return fmt.Errorf("lock post for tag mutation: %w", err)
	}
	return nil
}

func resolveTagID(ctx context.Context, tx pgx.Tx, userID int64, name tag.Name) (int64, error) {
	var tagID int64
	err := tx.QueryRow(ctx, `
		SELECT id
		FROM tags
		WHERE normalized_name = $1
	`, name.Normalized).Scan(&tagID)
	if err == nil {
		return tagID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("lookup tag: %w", err)
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO tags (name, normalized_name, created_by_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (normalized_name) DO NOTHING
		RETURNING id
	`, name.Display, name.Normalized, userID).Scan(&tagID)
	if err == nil {
		return tagID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("create tag: %w", err)
	}

	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM tags
		WHERE normalized_name = $1
	`, name.Normalized).Scan(&tagID); err != nil {
		return 0, fmt.Errorf("reload concurrently created tag: %w", err)
	}
	return tagID, nil
}

func loadPostTags(ctx context.Context, queryer tagQueryer, postID, viewerUserID int64) (tag.Snapshot, error) {
	sql := signedOutPostTagsSQL
	args := []any{postID}
	if viewerUserID > 0 {
		sql = signedInPostTagsSQL
		args = append(args, viewerUserID, tag.RoleModerator, tag.RoleAdmin)
	}
	rows, err := queryer.Query(ctx, sql, args...)
	if err != nil {
		return tag.Snapshot{}, fmt.Errorf("query post tags: %w", err)
	}
	defer rows.Close()

	snapshot := tag.Snapshot{PostID: postID, Tags: make([]tag.Item, 0, 8)}
	foundPost := false
	for rows.Next() {
		foundPost = true
		var item tag.Item
		var canRemove bool
		if err := rows.Scan(&item.ID, &item.Name, &canRemove); err != nil {
			return tag.Snapshot{}, fmt.Errorf("scan post tag: %w", err)
		}
		snapshot.CanRemove = canRemove
		if item.ID > 0 {
			snapshot.Tags = append(snapshot.Tags, item)
		}
	}
	if err := rows.Err(); err != nil {
		return tag.Snapshot{}, fmt.Errorf("read post tags: %w", err)
	}
	if !foundPost {
		return tag.Snapshot{}, tag.ErrPostNotFound
	}
	return snapshot, nil
}

func commitTagTransaction(ctx context.Context, tx pgx.Tx, operation string) error {
	if err := tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return fmt.Errorf("commit %s transaction rolled back: %w", operation, err)
		}
		return fmt.Errorf("%w: commit %s transaction: %v", tag.ErrCommitOutcomeUnknown, operation, err)
	}
	return nil
}
