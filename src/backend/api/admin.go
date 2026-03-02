package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dbgen "wallium/db/gen"
	"wallium/utils"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ── Disk-usage helper ─────────────────────────────────────────────────────────

type diskUsage struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
}

func dirUsage(root, label string, excludeDirs ...string) diskUsage {
	d := diskUsage{Path: root, Label: label}
	_ = filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() {
			for _, excl := range excludeDirs {
				if path == excl {
					return fs.SkipDir
				}
			}
			return nil
		}
		info, statErr := e.Info()
		if statErr == nil {
			d.Bytes += info.Size()
			d.Files++
		}
		return nil
	})
	return d
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// GetAdminStats returns aggregate counts and disk usage for the admin panel.
func (s *Server) GetAdminStats(c fiber.Ctx) error {
	ctx := c.Context()

	posts, err := s.store.CountPosts(ctx)
	if err != nil {
		return err
	}
	comments, err := s.store.CountComments(ctx)
	if err != nil {
		return err
	}
	tags, err := s.store.CountTags(ctx)
	if err != nil {
		return err
	}
	users, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}

	// Pending imports (dirty posts not yet committed)
	dirty, err := s.store.CountDirtyPosts(ctx)
	if err != nil {
		dirty = 0 // non-fatal
	}

	disk := []diskUsage{
		dirUsage(s.dirs.Upload, "uploads"),
		// Exclude the thumbnails subdirectory so it is counted separately.
		dirUsage(s.dirs.Image, "post images", s.dirs.Thumbnail),
		dirUsage(s.dirs.Thumbnail, "thumbnails"),
		dirUsage(s.dirs.Video, "videos"),
	}

	totalBytes := int64(0)
	for _, d := range disk {
		totalBytes += d.Bytes
	}

	return c.JSON(fiber.Map{
		"counts": fiber.Map{
			"posts":          posts,
			"comments":       comments,
			"tags":           tags,
			"users":          users,
			"pending_import": dirty,
		},
		"disk": fiber.Map{
			"total_bytes": totalBytes,
			"breakdown":   disk,
		},
	})
}

// ListUsers returns all users (id, name, level, email, created_at) for the
// admin user management table.
func (s *Server) ListUsers(c fiber.Ctx) error {
	users, err := s.store.GetAllUsersAdmin(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"users": users})
}

// AdminUpdateUserLevel promotes or demotes a user.
//
// PATCH /api/admin/users/:id/level   body: {"level": <int>}
func (s *Server) AdminUpdateUserLevel(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 32)
	if err != nil || id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}

	var body struct {
		Level int32 `json:"level"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if body.Level < LevelGuest {
		return fiber.NewError(fiber.StatusBadRequest, "invalid level value")
	}

	// Prevent self-demotion to avoid locking out the only admin.
	u, _ := s.sessionUser(c)
	if u != nil && u.ID == int32(id) && body.Level < LevelAdmin {
		return fiber.NewError(fiber.StatusBadRequest, "cannot demote yourself")
	}

	user, err := s.store.UpdateUserLevel(c.Context(), dbgen.UpdateUserLevelParams{
		Level: body.Level,
		ID:    int32(id),
	})
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"id": user.ID, "name": user.Name, "level": user.Level})
}

// AdminDeleteUser soft-deletes a user by id.
//
// DELETE /api/admin/users/:id
func (s *Server) AdminDeleteUser(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 32)
	if err != nil || id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}

	// Prevent self-deletion.
	u, _ := s.sessionUser(c)
	if u != nil && u.ID == int32(id) {
		return fiber.NewError(fiber.StatusBadRequest, "cannot delete yourself")
	}

	if err := s.store.DeleteUser(c.Context(), int32(id)); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// AdminDeletePost soft-deletes any post by id (regardless of ownership).
//
// DELETE /api/admin/posts/:id
func (s *Server) AdminDeletePost(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 32)
	if err != nil || id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid post id")
	}

	// Remove media files best-effort.
	post, dbErr := s.store.GetPostAdmin(c.Context(), int32(id))
	if dbErr == nil {
		removePostFiles(s.dirs, post.Filename, post.ThumbnailFilename)
	}

	if err := s.store.DeletePost(c.Context(), int32(id)); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// AdminDeleteComment soft-deletes any comment by id.
//
// DELETE /api/admin/comments/:id
func (s *Server) AdminDeleteComment(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 32)
	if err != nil || id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid comment id")
	}
	if err := s.store.DeleteComment(c.Context(), int32(id)); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// AdminDeleteTag hard-deletes a tag by id.  If the tag is a filter keyword
// (nsfp / nsfw / secret), the filter of every post that carried it is
// recalculated from the remaining tags.
//
// DELETE /api/admin/tags/:id
func (s *Server) AdminDeleteTag(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 32)
	if err != nil || id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid tag id")
	}

	// Look up the tag first so we know its name.
	tag, err := s.store.GetTag(c.Context(), int32(id))
	if err != nil {
		return err
	}

	// Collect posts affected by this delete before the FK cascade removes them.
	var affectedPostIDs []int32
	if filterTagPriority(tag.Name) >= 0 {
		affectedPostIDs, _ = s.store.GetPostIDsWithTagID(c.Context(), int32(id))
	}

	if err := s.store.DeleteTag(c.Context(), int32(id)); err != nil {
		return err
	}

	// Recalculate filter for every post that lost a filter-keyword tag.
	for _, postID := range affectedPostIDs {
		_ = s.recalcPostFilter(c.Context(), postID)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// AdminListComments returns all non-deleted comments for the moderation table.
//
// GET /api/admin/comments
func (s *Server) AdminListComments(c fiber.Ctx) error {
	comments, err := s.store.GetComments(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"comments": comments})
}

// ── helpers ───────────────────────────────────────────────────────────────────

// removePostFiles deletes media files associated with a post (best-effort).
func removePostFiles(dirs utils.Directories, filename, thumb string) {
	removeFiles(
		filepath.Join(dirs.Image, filename),
		filepath.Join(dirs.Thumbnail, thumb),
	)
}

func removeFiles(paths ...string) {
	for _, p := range paths {
		if p != "" {
			_ = os.Remove(p)
		}
	}
}

// BackfillPostDimensions reads every post that still has width=0 / height=0
// from disk, extracts its real dimensions, and writes them back to the DB.
// This is a one-time admin operation for content uploaded before the
// dimension columns were added; it is safe to call multiple times.
//
// The operation is registered as a tracked job so it's visible in the admin
// jobs panel with progress and ETA.
//
// POST /api/admin/posts/backfill-dimensions
func (s *Server) BackfillPostDimensions(c fiber.Ctx) error {
	ctx := c.Context()

	posts, err := s.store.GetPostsMissingDimensions(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "query failed: "+err.Error())
	}

	job := s.jobs.Register("Backfill post dimensions", JobOpts{
		Description: "Reading real dimensions from disk for posts missing width/height",
		Visibility:  VisibilityGlobal,
		Total:       len(posts),
	})
	job.Start()

	var updated, skipped, failed int
	for i, p := range posts {
		if job.Ctx().Err() != nil {
			break // cancelled
		}
		w, h, dimErr := dimensionsForPost(p, s.dirs)
		if dimErr != nil || w == 0 || h == 0 {
			failed++
			job.SetProgress(i+1, len(posts), fmt.Sprintf("updated %d · failed %d", updated, failed))
			continue
		}
		if updateErr := s.store.UpdatePostDimensions(ctx, p.ID, int32(w), int32(h)); updateErr != nil {
			failed++
			job.SetProgress(i+1, len(posts), fmt.Sprintf("updated %d · failed %d", updated, failed))
			continue
		}
		updated++
		job.SetProgress(i+1, len(posts), fmt.Sprintf("updated %d · failed %d", updated, failed))
	}
	skipped = len(posts) - updated - failed

	if job.Ctx().Err() != nil {
		// Job was cancelled — it's already marked by the manager.
	} else {
		job.Complete(fmt.Sprintf("updated %d · failed %d · skipped %d", updated, failed, skipped))
	}

	return c.JSON(fiber.Map{
		"total":   len(posts),
		"updated": updated,
		"failed":  failed,
		"skipped": skipped,
		"job_id":  job.ID,
	})
}

// ── SSE event types for RegenerateImages ─────────────────────────────────────

type regenStartEvent struct {
	Phase string `json:"phase"`
	Total int    `json:"total"`
}

type regenProgressEvent struct {
	Phase   string `json:"phase"`
	Total   int    `json:"total"`
	Current int    `json:"current"`
	Updated int    `json:"updated"`
	Failed  int    `json:"failed"`
	Skipped int    `json:"skipped"`
}

type regenDoneEvent struct {
	Phase   string `json:"phase"`
	Total   int    `json:"total"`
	Updated int    `json:"updated"`
	Failed  int    `json:"failed"`
	Skipped int    `json:"skipped"`
}

// regenQueueItem is pushed to the Redis list consumed by the Rust worker.
type regenQueueItem struct {
	PostID            int32  `json:"post_id"`
	Filename          string `json:"filename"`
	ThumbnailFilename string `json:"thumbnail_filename"`
	// JobKey namespaces per-job progress counters and lets the SSE handler
	// ignore Pub/Sub messages from concurrent or previous regen jobs.
	JobKey string `json:"job_key"`
}

// regenProgressMsg is the per-item progress event published by the Rust worker.
type regenProgressMsg struct {
	PostID int32  `json:"post_id"`
	JobKey string `json:"job_key"`
	OK     bool   `json:"ok"`
	Err    string `json:"err,omitempty"`
}

// RegenerateImages re-encodes every stored image via the Rust worker's fast
// SVT-AV1 pipeline (libjpeg-turbo decode + in-process SVT-AV1 encode) instead
// of spawning ffmpeg subprocesses in-process.
//
// Items are pushed to the Redis list "wallium:regen:queue"; the worker drains
// the list concurrently and publishes one progress event per item to the
// "wallium:regen:progress" Pub/Sub channel, which this handler relays as SSE.
//
// POST /api/admin/posts/regenerate-images
func (s *Server) RegenerateImages(c fiber.Ctx) error {
	posts, err := s.store.GetAllImagePosts(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "query failed: "+err.Error())
	}

	job := s.jobs.Register("Regenerate images as AVIF", JobOpts{
		Description: "Re-encoding every stored image via the fast Rust worker pipeline",
		Visibility:  VisibilityGlobal,
		Total:       len(posts),
	})

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	total := len(posts)
	rdb := s.rdb
	log := s.log

	log.DebugContext(c.Context(), "regenerate: handler entered",
		"total_posts", total)

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		ctx := context.Background()
		job.Start()

		// Unique key for this run — namespaces the Redis done-counter and
		// lets us ignore Pub/Sub messages from concurrent/previous jobs.
		jobKey := uuid.NewString()
		regenDoneKey := "wallium:regen:done:" + jobKey

		log.DebugContext(ctx, "regenerate: SSE writer started",
			"job_id", job.ID,
			"job_key", jobKey,
			"total", total,
			"regen_done_key", regenDoneKey)

		// Subscribe before pushing items so we never miss fast-path messages.
		// Buffer size = 2×total + 200 so concurrent encodes never fill it.
		bufSize := total*2 + 200
		if bufSize < 200 {
			bufSize = 200
		}
		sub := rdb.Subscribe(ctx, "wallium:regen:progress")
		defer func() { _ = sub.Close() }()
		ch := sub.Channel(redis.WithChannelSize(bufSize))

		log.DebugContext(ctx, "regenerate: subscribed to pubsub",
			"channel", "wallium:regen:progress",
			"channel_buf_size", bufSize)

		// Initialise the per-job counter with a 24-hour TTL.
		rdb.Set(ctx, regenDoneKey, 0, 24*time.Hour)
		log.DebugContext(ctx, "regenerate: counter initialised",
			"key", regenDoneKey)

		// Push all regen items atomically via a pipeline.
		pushStart := time.Now()
		pipe := rdb.Pipeline()
		for _, p := range posts {
			b, _ := json.Marshal(regenQueueItem{
				PostID:            p.ID,
				Filename:          p.Filename,
				ThumbnailFilename: p.ThumbnailFilename,
				JobKey:            jobKey,
			})
			pipe.RPush(ctx, "wallium:regen:queue", string(b))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			log.WarnContext(ctx, "regenerate: failed to push items to queue", "err", err)
			job.Fail("failed to enqueue items: " + err.Error())
			return
		}
		log.DebugContext(ctx, "regenerate: items pushed to Redis queue",
			"count", total,
			"push_elapsed_ms", time.Since(pushStart).Milliseconds())

		// Wake the worker.
		rdb.Publish(ctx, "wallium:regen:wake", "1")
		log.DebugContext(ctx, "regenerate: published wake signal to worker",
			"channel", "wallium:regen:wake")

		writeSSE(w, regenStartEvent{Phase: "start", Total: total})

		var updated, failed int
		// Heartbeat keeps the SSE connection alive through idle periods.
		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()
		// Poll ticker reads the authoritative Redis counter every 2 s as a
		// reliable fallback in case Pub/Sub messages are dropped under load.
		pollTick := time.NewTicker(2 * time.Second)
		defer pollTick.Stop()
		var lastPolledCount int

		// Stall detection: if no progress is observed for this long, assume
		// the worker has crashed or is permanently stuck, and abort the SSE
		// loop so the client isn't left hanging indefinitely.
		const stallTimeout = 10 * time.Minute
		lastProgressAt := time.Now()
		loopIter := 0

		done := total == 0
		exitReason := "unknown"
		if done {
			exitReason = "total=0"
		}
	loop:
		for !done {
			loopIter++
			select {
			case msg, ok := <-ch:
				if !ok {
					log.DebugContext(ctx, "regenerate: pubsub channel closed",
						"loop_iter", loopIter,
						"updated", updated, "failed", failed)
					exitReason = "pubsub_channel_closed"
					break loop
				}
				log.DebugContext(ctx, "regenerate: pubsub message received",
					"loop_iter", loopIter,
					"payload_len", len(msg.Payload),
					"payload_preview", truncate(msg.Payload, 200))

				var progress regenProgressMsg
				if jsonErr := json.Unmarshal([]byte(msg.Payload), &progress); jsonErr != nil {
					log.DebugContext(ctx, "regenerate: pubsub message JSON parse error — skipping",
						"err", jsonErr,
						"payload", truncate(msg.Payload, 200))
					continue
				}
				// Ignore messages from a different job (startup drain, concurrent run).
				if progress.JobKey != jobKey {
					log.DebugContext(ctx, "regenerate: pubsub message belongs to different job — skipping",
						"msg_job_key", progress.JobKey,
						"our_job_key", jobKey,
						"msg_post_id", progress.PostID)
					continue
				}
				if progress.OK {
					updated++
				} else {
					failed++
					log.DebugContext(ctx, "regenerate: worker reported item FAILED",
						"post_id", progress.PostID,
						"err", progress.Err,
						"total_failed", failed)
				}
				cur := updated + failed
				lastProgressAt = time.Now()
				log.DebugContext(ctx, "regenerate: progress via pubsub",
					"post_id", progress.PostID,
					"ok", progress.OK,
					"current", cur, "total", total,
					"updated", updated, "failed", failed)
				job.SetProgress(cur, total,
					fmt.Sprintf("updated %d · failed %d", updated, failed))
				writeSSE(w, regenProgressEvent{
					Phase:   "progress",
					Total:   total,
					Current: cur,
					Updated: updated,
					Failed:  failed,
					Skipped: 0,
				})
				if cur >= total {
					exitReason = "pubsub_complete"
					done = true
				}
			case <-pollTick.C:
				// The Redis INCR counter is the source of truth: it is
				// incremented atomically and never lost, even when Pub/Sub
				// messages are dropped due to buffer overflow.
				sinceProgress := time.Since(lastProgressAt)
				n, pollErr := rdb.Get(ctx, regenDoneKey).Int()
				log.DebugContext(ctx, "regenerate: poll tick fired",
					"loop_iter", loopIter,
					"counter_val", n,
					"counter_err", pollErr,
					"last_polled_count", lastPolledCount,
					"updated", updated, "failed", failed,
					"since_last_progress_s", int(sinceProgress.Seconds()))

				if pollErr != nil || n <= lastPolledCount {
					// No progress — check for stall.
					if sinceProgress > stallTimeout {
						log.WarnContext(ctx, "regenerate: aborting — no progress for 10 min",
							"total", total, "current", updated+failed,
							"stall_seconds", int(sinceProgress.Seconds()))
						exitReason = "stall_timeout"
						break loop
					}
					log.DebugContext(ctx, "regenerate: poll: no new progress",
						"counter_val", n, "last_polled", lastPolledCount,
						"stall_s", int(sinceProgress.Seconds()),
						"stall_limit_s", int(stallTimeout.Seconds()))
					continue
				}
				lastPolledCount = n
				lastProgressAt = time.Now()
				// Sync the local counters with the ground truth.
				cur := updated + failed
				if n > cur {
					// Some Pub/Sub messages were dropped; advance the counter.
					delta := n - cur
					// We cannot tell ok vs fail for dropped messages, so charge
					// them to failed conservatively.
					log.DebugContext(ctx, "regenerate: poll detected dropped pubsub messages",
						"counter_val", n,
						"local_cur", cur,
						"dropped_delta", delta)
					failed += delta
					cur = n
				}
				log.DebugContext(ctx, "regenerate: progress via poll counter",
					"counter_val", n, "current", cur, "total", total,
					"updated", updated, "failed", failed)
				job.SetProgress(cur, total,
					fmt.Sprintf("processed %d · updated %d · failed %d", cur, updated, failed))
				writeSSE(w, regenProgressEvent{
					Phase:   "progress",
					Total:   total,
					Current: cur,
					Updated: updated,
					Failed:  failed,
					Skipped: 0,
				})
				if cur >= total {
					exitReason = "poll_complete"
					done = true
				}
			case <-heartbeat.C:
				log.DebugContext(ctx, "regenerate: sending SSE heartbeat",
					"updated", updated, "failed", failed, "total", total)
				_, _ = fmt.Fprintf(w, ": heartbeat\n\n")
				_ = w.Flush()
			case <-job.Ctx().Done():
				log.DebugContext(ctx, "regenerate: job context cancelled",
					"updated", updated, "failed", failed)
				exitReason = "job_cancelled"
				break loop
			}
		}

		u, f := updated, failed
		log.InfoContext(ctx, "regenerate: SSE loop exited",
			"exit_reason", exitReason,
			"loop_iters", loopIter,
			"total", total, "updated", u, "failed", f)

		writeSSE(w, regenDoneEvent{
			Phase: "done", Total: total,
			Updated: u, Failed: f, Skipped: 0,
		})

		if job.Ctx().Err() != nil {
			// Cancelled — already marked by JobManager; leave remaining items
			// in the worker queue so the worker finishes them gracefully.
		} else {
			job.Complete(fmt.Sprintf("updated %d · failed %d", u, f))
		}

		log.InfoContext(ctx, "image regeneration complete",
			"total", total, "updated", u, "failed", f)
	})
	return nil
}

// dimensionsForPost derives (width, height) from a post's media file on disk.
// It uses ffprobe for all file types since the stored images are AVIF and
// Go's standard image.Decode does not support AVIF natively.
// It uses ffprobe for all file types since the stored images are AVIF and
// Go's standard image.Decode does not support AVIF natively.
func dimensionsForPost(p dbgen.Post, dirs utils.Directories) (int, int, error) {
	isVideo := strings.HasPrefix(p.ContentType, "video/")
	if isVideo {
		filePath := filepath.Join(dirs.Video, p.Filename)
		return utils.GetVideoDimensions(filePath)
	}
	// Images are stored as AVIF — ffprobe handles both images and videos.
	filePath := filepath.Join(dirs.Image, p.Filename)
	return utils.GetVideoDimensions(filePath)
}
