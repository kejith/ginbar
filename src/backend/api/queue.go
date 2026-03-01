package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/redis/go-redis/v9"

	"wallium/db"
)

const (
	// redisQueueChannel is the Redis Pub/Sub channel the Rust worker listens on.
	redisQueueChannel = "wallium:queue:wake"
	// redisQueueStatusKey is the Redis key where the worker publishes its status.
	redisQueueStatusKey = "wallium:queue:status"
	// redisDupKeyPrefix is the Redis key prefix for duplicate-post info.
	redisDupKeyPrefix = "dup:post:"
)

// DuplicateEntry holds minimal info about an existing post that is a
// near-duplicate of a rejected upload, returned to the frontend so the user
// can inspect the matching posts.
type DuplicateEntry struct {
	ID                int32  `json:"id"`
	ThumbnailFilename string `json:"thumbnail_filename"`
	HammingDistance   int32  `json:"hamming_distance"`
}

// QueueSnapshot is the JSON-serialisable state of the processing queue.
// Sent via SSE to the admin panel and returned by the per-post status endpoint.
type QueueSnapshot struct {
	Pending    int     `json:"pending"`
	Active     int     `json:"active"`
	Total      int     `json:"total"`
	Processed  int     `json:"processed"`
	Imported   int     `json:"imported"`
	Failed     int     `json:"failed"`
	RatePerSec float64 `json:"rate_per_sec"`
	ETASec     int     `json:"eta_sec"`
	Running    bool    `json:"running"`
}

// ProcessQueue is a lightweight proxy that communicates with the external Rust
// worker via Redis.  It no longer runs processing itself — it only:
//   - publishes wake-up notifications via Redis Pub/Sub
//   - reads queue status from a Redis key written by the worker
//   - reads duplicate-post info from Redis keys written by the worker
//   - falls back to counting dirty posts in the DB when the worker hasn't
//     published status yet
type ProcessQueue struct {
	rdb   *redis.Client
	store *db.Store
	log   *slog.Logger
}

func newProcessQueue(rdb *redis.Client, store *db.Store, log *slog.Logger) *ProcessQueue {
	return &ProcessQueue{
		rdb:   rdb,
		store: store,
		log:   log,
	}
}

// Notify publishes a wake-up message to the Rust worker via Redis Pub/Sub.
func (q *ProcessQueue) Notify() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := q.rdb.Publish(ctx, redisQueueChannel, "wake").Err(); err != nil {
		q.log.Warn("queue: failed to publish notify", slog.Any("err", err))
	}
}

// Snapshot reads the current queue state from Redis (written by the Rust worker).
// When the worker hasn't published status (key missing / expired), it falls
// back to counting dirty posts in the database so the admin panel still
// reflects the actual queue depth.
func (q *ProcessQueue) Snapshot() QueueSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	val, err := q.rdb.Get(ctx, redisQueueStatusKey).Result()
	if err != nil {
		// No worker status in Redis — fall back to a DB count.
		return q.dbFallbackSnapshot(ctx)
	}

	var snap QueueSnapshot
	if err := json.Unmarshal([]byte(val), &snap); err != nil {
		return q.dbFallbackSnapshot(ctx)
	}

	// Compute ETA from the snapshot if the worker didn't provide it.
	if snap.ETASec == 0 && snap.Running && snap.RatePerSec > 0 {
		remaining := snap.Pending + snap.Active
		if remaining > 0 {
			snap.ETASec = int(math.Ceil(float64(remaining) / snap.RatePerSec))
		}
	}

	return snap
}

// dbFallbackSnapshot queries the database for the number of unprocessed posts
// so the admin panel can display the queue depth even when the Rust worker
// hasn't started or has crashed.
func (q *ProcessQueue) dbFallbackSnapshot(ctx context.Context) QueueSnapshot {
	count, err := q.store.CountDirtyPosts(ctx)
	if err != nil {
		q.log.Warn("queue: db fallback failed", slog.Any("err", err))
		return QueueSnapshot{ETASec: -1}
	}
	if count == 0 {
		return QueueSnapshot{ETASec: -1}
	}
	return QueueSnapshot{
		Pending: int(count),
		Total:   int(count),
		ETASec:  -1,
		Running: false,
	}
}

// GetDupCache reads duplicate-post info for a specific post from Redis.
func (q *ProcessQueue) GetDupCache(postID int32) ([]DuplicateEntry, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	key := fmt.Sprintf("%s%d", redisDupKeyPrefix, postID)
	val, err := q.rdb.Get(ctx, key).Result()
	if err != nil {
		return nil, false
	}

	var entries []DuplicateEntry
	if err := json.Unmarshal([]byte(val), &entries); err != nil {
		return nil, false
	}
	return entries, len(entries) > 0
}

// ── HTTP Handlers ─────────────────────────────────────────────────────────────

// AdminQueueStream streams QueueSnapshot events as SSE to admin clients.
// Polls the Redis status key written by the Rust worker.
// GET /api/admin/queue/stream
func (s *Server) AdminQueueStream(c fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// Send initial snapshot immediately.
		writeSSE(w, s.queue.Snapshot())

		poll := time.NewTicker(2 * time.Second)
		defer poll.Stop()
		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-poll.C:
				writeSSE(w, s.queue.Snapshot())
			case <-heartbeat.C:
				fmt.Fprintf(w, ": heartbeat\n\n")
				w.Flush()
			}
		}
	})
	return nil
}

// GetMyQueueStatus returns the calling user's current dirty post (if any).
// GET /api/post/my-queue
func (s *Server) GetMyQueueStatus(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	post, err := s.store.GetUserDirtyPost(c.Context(), u.Name)
	if err != nil {
		return err
	}
	if post == nil {
		return c.JSON(fiber.Map{"has_post": false})
	}

	pos, _ := s.store.CountDirtyPostsBeforeID(c.Context(), post.ID)
	snap := s.queue.Snapshot()
	eta := -1
	if snap.RatePerSec > 0 {
		if pos == 0 {
			eta = snap.ETASec
		} else {
			eta = int(math.Ceil(float64(pos) / snap.RatePerSec))
		}
	}

	return c.JSON(fiber.Map{
		"has_post":       true,
		"post_id":        post.ID,
		"dirty":          post.Dirty,
		"needs_release":  !post.Released,
		"queue_position": pos,
		"eta_sec":        eta,
	})
}

// GetPostQueueStatus returns queue position and ETA for a specific dirty post.
// GET /api/post/queue/:post_id
func (s *Server) GetPostQueueStatus(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("post_id"), 10, 32)
	if err != nil || id <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid post_id")
	}

	ctx := c.Context()

	dirty, err := s.store.IsPostDirty(ctx, int32(id))
	if err != nil {
		return err
	}
	if !dirty {
		post, postErr := s.store.GetPostAdmin(ctx, int32(id))
		if postErr != nil {
			return postErr
		}
		resp := fiber.Map{
			"dirty":          false,
			"needs_release":  !post.Released,
			"queue_position": 0,
			"eta_sec":        0,
		}
		if entries, ok := s.queue.GetDupCache(int32(id)); ok {
			resp["duplicates"] = entries
		}
		return c.JSON(resp)
	}

	pos, _ := s.store.CountDirtyPostsBeforeID(ctx, int32(id))
	snap := s.queue.Snapshot()

	eta := -1
	if snap.RatePerSec > 0 {
		if pos == 0 {
			eta = snap.ETASec
		} else {
			eta = int(math.Ceil(float64(pos) / snap.RatePerSec))
		}
	}

	return c.JSON(fiber.Map{
		"dirty":          true,
		"queue_position": pos,
		"eta_sec":        eta,
	})
}
