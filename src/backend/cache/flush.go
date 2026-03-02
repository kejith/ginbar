package cache

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// entityConfig describes the DB tables involved in one entity type's vote
// synchronisation.
type entityConfig struct {
	// EntityType matches the EntityPost/EntityComment/EntityPostTag constants.
	EntityType string
	// voteTable is the join table that stores individual votes.
	VoteTable string
	// entityCol is the column in voteTable that references the parent entity.
	EntityCol string
	// parentTable holds the denormalised score column.
	ParentTable string
}

var entityConfigs = []entityConfig{
	{EntityType: EntityPost, VoteTable: "post_votes", EntityCol: "post_id", ParentTable: "posts"},
	{EntityType: EntityComment, VoteTable: "comment_votes", EntityCol: "comment_id", ParentTable: "comments"},
	{EntityType: EntityPostTag, VoteTable: "post_tag_votes", EntityCol: "post_tag_id", ParentTable: "post_tags"},
}

// StartFlushWorker launches a background goroutine that periodically drains
// the Redis vote buffer into PostgreSQL.
//
// It returns a channel that is closed once the worker has stopped (including
// the final blocking flush on ctx cancellation).  Callers should wait on this
// channel during graceful shutdown to avoid losing in-flight votes.
func StartFlushWorker(ctx context.Context, rdb *redis.Client, pool *pgxpool.Pool, interval time.Duration, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})

	go func() {
		defer close(done)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				// Final flush on shutdown.
				flushAll(context.Background(), rdb, pool, log)
				return
			case <-ticker.C:
				flushAll(ctx, rdb, pool, log)
			}
		}
	}()

	return done
}

// PreloadScores seeds the Redis score cache from the current DB aggregate
// totals.  Run once at startup so the first requests see correct scores
// without hitting Postgres.
func PreloadScores(ctx context.Context, rdb *redis.Client, pool *pgxpool.Pool, log *slog.Logger) error {
	for _, cfg := range entityConfigs {
		sql := fmt.Sprintf(
			`SELECT %s, COALESCE(SUM(vote), 0)::bigint AS total
			 FROM %s GROUP BY %s`,
			cfg.EntityCol, cfg.VoteTable, cfg.EntityCol,
		)

		rows, err := pool.Query(ctx, sql)
		if err != nil {
			return fmt.Errorf("preload scores %s: %w", cfg.EntityType, err)
		}

		pipe := rdb.Pipeline()
		count := 0
		for rows.Next() {
			var id int32
			var total int64
			if err = rows.Scan(&id, &total); err != nil {
				rows.Close()
				return fmt.Errorf("preload scores %s scan: %w", cfg.EntityType, err)
			}
			idStr := strconv.FormatInt(int64(id), 10)
			// SetNX so we never overwrite a live Redis value with stale DB data.
			pipe.SetNX(ctx, scoreKey(cfg.EntityType, idStr), total, 0)
			count++
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return fmt.Errorf("preload scores %s rows: %w", cfg.EntityType, err)
		}

		if count > 0 {
			if _, err = pipe.Exec(ctx); err != nil {
				return fmt.Errorf("preload scores %s pipeline: %w", cfg.EntityType, err)
			}
		}
		log.Info("preloaded vote scores", "entity", cfg.EntityType, "rows", count)
	}
	return nil
}

// ── Internal flush logic ──────────────────────────────────────────────────────

func flushAll(ctx context.Context, rdb *redis.Client, pool *pgxpool.Pool, log *slog.Logger) {
	for _, cfg := range entityConfigs {
		if err := flushEntityType(ctx, rdb, pool, cfg); err != nil {
			log.Error("vote flush error", "entity", cfg.EntityType, "err", err)
		}
	}
}

func flushEntityType(ctx context.Context, rdb *redis.Client, pool *pgxpool.Pool, cfg entityConfig) error {
	dk := dirtyKey(cfg.EntityType)

	// 1. Grab all dirty entity IDs.
	ids, err := rdb.SMembers(ctx, dk).Result()
	if err != nil || len(ids) == 0 {
		return err
	}

	// 2. Atomically remove them from the dirty set BEFORE reading vote data.
	//    Any vote that arrives after this point will re-add the ID — it will be
	//    picked up by the next flush tick.
	members := make([]interface{}, len(ids))
	for i, id := range ids {
		members[i] = id
	}
	rdb.SRem(ctx, dk, members...)

	// 3. Pipeline-read current votes + score + pending deletes for each entity.
	pipe := rdb.Pipeline()
	type cmds struct {
		votes   *redis.MapStringStringCmd
		score   *redis.StringCmd
		deleted *redis.StringSliceCmd
	}
	cmdMap := make(map[string]cmds, len(ids))
	for _, id := range ids {
		cmdMap[id] = cmds{
			votes:   pipe.HGetAll(ctx, votesKey(cfg.EntityType, id)),
			score:   pipe.Get(ctx, scoreKey(cfg.EntityType, id)),
			deleted: pipe.SMembers(ctx, deletedKey(cfg.EntityType, id)),
		}
	}
	if _, err = pipe.Exec(ctx); err != nil && err != redis.Nil {
		// Re-add all to dirty for retry next tick.
		rdb.SAdd(ctx, dk, members...)
		return fmt.Errorf("flush pipeline read: %w", err)
	}

	// 4. Write each entity to Postgres inside its own transaction.
	upsertVoteSQL := fmt.Sprintf(
		`INSERT INTO %s (%s, user_id, vote)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (%s, user_id) DO UPDATE SET vote = EXCLUDED.vote`,
		cfg.VoteTable, cfg.EntityCol, cfg.EntityCol,
	)
	deleteVoteSQL := fmt.Sprintf(
		`DELETE FROM %s WHERE %s = $1 AND user_id = $2`,
		cfg.VoteTable, cfg.EntityCol,
	)
	updateScoreSQL := fmt.Sprintf(
		`UPDATE %s SET score = $1 WHERE id = $2`,
		cfg.ParentTable,
	)

	for _, id := range ids {
		c := cmdMap[id]

		votes, _ := c.votes.Result()
		score, _ := c.score.Int64()
		deletedUsers, _ := c.deleted.Result()
		entityID, _ := strconv.ParseInt(id, 10, 32)

		if err = writeEntityTx(ctx, pool, entityID, score, votes, deletedUsers,
			upsertVoteSQL, deleteVoteSQL, updateScoreSQL); err != nil {

			// Re-mark as dirty so next tick retries this entity.
			rdb.SAdd(ctx, dk, id)
			// Log but continue with remaining entities.
			continue
		}

		// 5. Remove only the deleted-user entries we actually processed.
		if len(deletedUsers) > 0 {
			dKey := deletedKey(cfg.EntityType, id)
			del := make([]interface{}, len(deletedUsers))
			for i, u := range deletedUsers {
				del[i] = u
			}
			rdb.SRem(ctx, dKey, del...)
		}
	}
	return nil
}

// writeEntityTx writes one entity's votes and score inside a single pgx
// transaction.  Uses a pgx.Batch to minimise round-trips.
func writeEntityTx(
	ctx context.Context,
	pool *pgxpool.Pool,
	entityID, score int64,
	votes map[string]string,
	deletedUsers []string,
	upsertSQL, deleteSQL, updateScoreSQL string,
) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	batch := &pgx.Batch{}

	// Upsert non-zero votes.
	for userIDStr, voteStr := range votes {
		userID, _ := strconv.ParseInt(userIDStr, 10, 32)
		vote, _ := strconv.ParseInt(voteStr, 10, 16)
		batch.Queue(upsertSQL, entityID, userID, int16(vote))
	}

	// Delete retracted votes.
	for _, userIDStr := range deletedUsers {
		userID, _ := strconv.ParseInt(userIDStr, 10, 32)
		batch.Queue(deleteSQL, entityID, userID)
	}

	// Update the denormalised score.
	batch.Queue(updateScoreSQL, score, entityID)

	br := tx.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		if _, execErr := br.Exec(); execErr != nil {
			_ = br.Close()
			return fmt.Errorf("batch exec[%d]: %w", i, execErr)
		}
	}
	if err = br.Close(); err != nil {
		return fmt.Errorf("batch close: %w", err)
	}

	return tx.Commit(ctx)
}
