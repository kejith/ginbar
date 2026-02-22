package api

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"math"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wallium/db"
	dbgen "wallium/db/gen"
	"wallium/utils"

	"github.com/gofiber/fiber/v3"
)

// queuePollInterval is how often the processor wakes up to check for new dirty posts
// when it has nothing to do (fallback timer — the Notify() channel handles
// the common fast-path).
const queuePollInterval = 5 * time.Second

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
	Pending    int     `json:"pending"`     // waiting, not yet picked up
	Active     int     `json:"active"`      // currently being processed
	Total      int     `json:"total"`       // all items in current batch
	Processed  int     `json:"processed"`   // completed (success + fail) in batch
	Imported   int     `json:"imported"`    // successful so far
	Failed     int     `json:"failed"`      // failed so far
	RatePerSec float64 `json:"rate_per_sec"` // items/second (current batch)
	ETASec     int     `json:"eta_sec"`     // seconds to drain queue (-1=unknown)
	Running    bool    `json:"running"`
}

// ProcessQueue is a long-lived background worker that continuously drains
// dirty posts from the DB.  The pr0gramm import handler just inserts dirty
// rows and calls Notify(); member uploads do the same.
type ProcessQueue struct {
	srv *Server
	log *slog.Logger

	// notify wakes the drain loop immediately instead of waiting for the ticker.
	notify chan struct{}

	// SSE subscriber registry.
	mu   sync.RWMutex
	subs []chan QueueSnapshot

	// per-batch counters (reset at the start of each drain()).
	pending   atomic.Int32
	active    atomic.Int32
	total     atomic.Int32
	processed atomic.Int32
	imported  atomic.Int32
	failed    atomic.Int32
	running   atomic.Bool

	// rate estimation.
	rateMu     sync.Mutex
	batchStart time.Time

	// dupCache maps dirty post ID → potential duplicate entries for posts that
	// were rejected by the perceptual-hash dedup check.  Entries are evicted
	// automatically after 15 minutes so the map doesn't grow unboundedly.
	dupCache sync.Map // int32 -> []DuplicateEntry
}

func newProcessQueue(srv *Server, log *slog.Logger) *ProcessQueue {
	return &ProcessQueue{
		srv:    srv,
		log:    log,
		notify: make(chan struct{}, 16),
	}
}

// Notify wakes the processing loop immediately (non-blocking).
func (q *ProcessQueue) Notify() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// Subscribe returns a buffered channel that receives QueueSnapshot updates.
// Call Unsubscribe when the receiver is done.
func (q *ProcessQueue) Subscribe() chan QueueSnapshot {
	ch := make(chan QueueSnapshot, 8)
	q.mu.Lock()
	q.subs = append(q.subs, ch)
	q.mu.Unlock()
	return ch
}

// Unsubscribe removes the subscriber and closes its channel.
func (q *ProcessQueue) Unsubscribe(ch chan QueueSnapshot) {
	q.mu.Lock()
	for i, s := range q.subs {
		if s == ch {
			q.subs = append(q.subs[:i], q.subs[i+1:]...)
			break
		}
	}
	q.mu.Unlock()
	close(ch)
}

func (q *ProcessQueue) broadcast() {
	snap := q.Snapshot()
	q.mu.RLock()
	for _, ch := range q.subs {
		select {
		case ch <- snap:
		default:
		}
	}
	q.mu.RUnlock()
}

// Snapshot returns a consistent view of the current queue state.
func (q *ProcessQueue) Snapshot() QueueSnapshot {
	proc := int(q.processed.Load())
	pending := int(q.pending.Load())
	active := int(q.active.Load())

	var rate float64
	eta := -1

	q.rateMu.Lock()
	if !q.batchStart.IsZero() && proc > 0 {
		elapsed := time.Since(q.batchStart).Seconds()
		if elapsed > 0 {
			rate = float64(proc) / elapsed
		}
		remaining := pending + active
		if rate > 0 && remaining > 0 {
			eta = int(math.Ceil(float64(remaining) / rate))
		} else if remaining == 0 {
			eta = 0
		}
	}
	q.rateMu.Unlock()

	return QueueSnapshot{
		Pending:    pending,
		Active:     active,
		Total:      int(q.total.Load()),
		Processed:  proc,
		Imported:   int(q.imported.Load()),
		Failed:     int(q.failed.Load()),
		RatePerSec: math.Round(rate*100) / 100,
		ETASec:     eta,
		Running:    q.running.Load(),
	}
}

// Run is the main loop — launch as a goroutine from Server.Start().
func (q *ProcessQueue) Run(ctx context.Context) {
	q.log.InfoContext(ctx, "process queue started")
	ticker := time.NewTicker(queuePollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			q.log.InfoContext(ctx, "process queue stopping")
			return
		case <-q.notify:
			// drain any extra pending notifications to avoid redundant runs.
		drainNotify:
			for {
				select {
				case <-q.notify:
				default:
					break drainNotify
				}
			}
			q.drain(ctx)
		case <-ticker.C:
			q.drain(ctx)
		}
	}
}

// drain fetches all current dirty posts and processes them concurrently.
func (q *ProcessQueue) drain(ctx context.Context) {
	if q.srv.store == nil {
		return
	}

	dirty, err := q.srv.store.GetDirtyPosts(ctx)
	if err != nil {
		q.log.WarnContext(ctx, "queue: failed to fetch dirty posts", slog.Any("err", err))
		return
	}

	if len(dirty) == 0 {
		if q.running.Load() {
			q.running.Store(false)
			q.pending.Store(0)
			q.active.Store(0)
			q.broadcast()
		}
		return
	}

	n := int32(len(dirty))
	q.pending.Store(n)
	q.total.Store(n)
	q.processed.Store(0)
	q.imported.Store(0)
	q.failed.Store(0)
	q.active.Store(0)
	q.running.Store(true)

	q.rateMu.Lock()
	q.batchStart = time.Now()
	q.rateMu.Unlock()

	q.broadcast()

	sem := make(chan struct{}, importConcurrency)
	var wg sync.WaitGroup

	for _, dp := range dirty {
		dp := dp
		wg.Add(1)
		q.pending.Add(-1)
		q.active.Add(1)
		sem <- struct{}{}

		go func() {
			defer wg.Done()
			defer func() {
				<-sem
				q.active.Add(-1)
			}()

			procErr := q.processQueuedPost(ctx, dp)
			q.processed.Add(1)
			if procErr != nil {
				q.failed.Add(1)
				q.log.DebugContext(ctx, "queue: finalize failed",
					slog.Int("post_id", int(dp.ID)),
					slog.Any("err", procErr),
				)
			} else {
				q.imported.Add(1)
			}
			q.broadcast()
		}()
	}

	wg.Wait()
	q.running.Store(false)
	q.pending.Store(0)
	q.active.Store(0)
	q.broadcast()

	q.log.InfoContext(ctx, "queue: batch complete",
		slog.Int("total", int(n)),
		slog.Int("imported", int(q.imported.Load())),
		slog.Int("failed", int(q.failed.Load())),
	)
}

// processQueuedPost handles a single dirty post end-to-end:
//   - If UploadedFilename is set: process that local file (member file upload).
//   - If URL starts with pr0gramm CDN: download with pr0gramm headers.
//   - Otherwise: download with the generic downloader.
//
// On any error the dirty row is deleted so the URL is not permanently blocked.
func (q *ProcessQueue) processQueuedPost(ctx context.Context, post dbgen.Post) error {
	store := q.srv.store
	dirs := q.srv.dirs

	var tmpPath string
	var isTemp bool // whether we own the file and must remove it

	if post.UploadedFilename != "" {
		// File was already saved locally by the upload handler.
		tmpPath = post.UploadedFilename
		isTemp = true // clean up the staged file after processing
	} else if post.Url != "" {
		var err error
		if strings.HasPrefix(post.Url, pr0grammImgBase) {
			tmpPath, err = downloadPr0grammFile(post.Url, dirs.Tmp)
		} else {
			tmpPath, err = utils.DownloadFile(post.Url, dirs.Tmp)
		}
		if err != nil {
			if store != nil {
				_ = store.DeleteDirtyPost(ctx, post.ID)
			}
			return fmt.Errorf("download failed: %w", err)
		}
		isTemp = true
	} else {
		if store != nil {
			_ = store.DeleteDirtyPost(ctx, post.ID)
		}
		return fmt.Errorf("dirty post %d has neither URL nor uploaded file", post.ID)
	}

	if isTemp {
		defer os.Remove(tmpPath)
	}

	mimeType := mime.TypeByExtension(filepath.Ext(tmpPath))
	fileType := strings.SplitN(mimeType, "/", 2)[0]

	switch fileType {
	case "image":
		res, err := utils.ProcessImage(tmpPath, dirs)
		if err != nil {
			if store != nil {
				_ = store.DeleteDirtyPost(ctx, post.ID)
			}
			return fmt.Errorf("image processing failed: %w", err)
		}

		h := res.PerceptionHash.GetHash()
		if store != nil {
			// Perceptual-hash duplicate check — only against finalized (non-dirty) posts.
			dups, _ := store.GetPossibleDuplicatePosts(ctx, dbgen.GetPossibleDuplicatePostsParams{
				Column1: int64(h[0]),
				Column2: int64(h[1]),
				Column3: int64(h[2]),
				Column4: int64(h[3]),
			})
			var entries []DuplicateEntry
			for _, d := range dups {
				if d.ID == post.ID || d.Dirty {
					continue
				}
				entries = append(entries, DuplicateEntry{
					ID:                d.ID,
					ThumbnailFilename: d.ThumbnailFilename,
					HammingDistance:   d.HammingDistance,
				})
			}
			if len(entries) > 0 {
				// Clean up the files that were already written to disk.
				_ = os.Remove(filepath.Join(dirs.Image, res.Filename))
				_ = os.Remove(filepath.Join(dirs.Thumbnail, res.ThumbnailFilename))
				// Cache the duplicate info so the status endpoint can return it.
				q.dupCache.Store(post.ID, entries)
				go func(id int32) {
					time.Sleep(15 * time.Minute)
					q.dupCache.Delete(id)
				}(post.ID)
				_ = store.DeleteDirtyPost(ctx, post.ID)
				return fmt.Errorf("duplicate: found %d similar post(s)", len(entries))
			}

			if err := store.FinalizePost(ctx, db.FinalizePostParams{
				ID:                post.ID,
				Filename:          res.Filename,
				ThumbnailFilename: res.ThumbnailFilename,
				UploadedFilename:  res.UploadedFilename,
				ContentType:       "image",
				PHash0:            int64(h[0]),
				PHash1:            int64(h[1]),
				PHash2:            int64(h[2]),
				PHash3:            int64(h[3]),
				Width:             int32(res.Width),
				Height:            int32(res.Height),
			}); err != nil {
				_ = store.DeleteDirtyPost(ctx, post.ID)
				return fmt.Errorf("finalize post: %w", err)
			}
		}

	case "video":
		vres, err := utils.ProcessVideo(tmpPath, mimeType, dirs)
		if err != nil {
			if store != nil {
				_ = store.DeleteDirtyPost(ctx, post.ID)
			}
			return fmt.Errorf("video processing failed: %w", err)
		}
		if store != nil {
			if err := store.FinalizePost(ctx, db.FinalizePostParams{
				ID:                post.ID,
				Filename:          filepath.Base(vres.Filename),
				ThumbnailFilename: filepath.Base(vres.ThumbnailFilename),
				UploadedFilename:  filepath.Base(tmpPath),
				ContentType:       mimeType,
				Width:             int32(vres.Width),
				Height:            int32(vres.Height),
			}); err != nil {
				_ = store.DeleteDirtyPost(ctx, post.ID)
				return fmt.Errorf("finalize post: %w", err)
			}
		}

	default:
		if store != nil {
			_ = store.DeleteDirtyPost(ctx, post.ID)
		}
		return fmt.Errorf("unsupported file type: %s", mimeType)
	}

	return nil
}

// ── HTTP Handlers ─────────────────────────────────────────────────────────────

// AdminQueueStream streams QueueSnapshot events as SSE to admin clients.
// GET /api/admin/queue/stream
func (s *Server) AdminQueueStream(c fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	ch := s.queue.Subscribe()
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer s.queue.Unsubscribe(ch)

		// Send initial snapshot immediately.
		writeSSE(w, s.queue.Snapshot())

		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case snap, ok := <-ch:
				if !ok {
					return
				}
				writeSSE(w, snap)
			case <-heartbeat.C:
				// SSE comment line keeps the connection alive through proxies.
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
		// Check whether the post is awaiting release by its owner.
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
		if v, ok := s.queue.dupCache.Load(int32(id)); ok {
			resp["duplicates"] = v
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
