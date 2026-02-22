package api

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	dbgen "wallium/db/gen"
	"wallium/utils"

	"github.com/gofiber/fiber/v3"
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
// POST /api/admin/posts/backfill-dimensions
func (s *Server) BackfillPostDimensions(c fiber.Ctx) error {
	ctx := c.Context()

	posts, err := s.store.GetPostsMissingDimensions(ctx)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "query failed: "+err.Error())
	}

	var updated, skipped, failed int
	for _, p := range posts {
		w, h, dimErr := dimensionsForPost(p, s.dirs)
		if dimErr != nil || w == 0 || h == 0 {
			failed++
			continue
		}
		if updateErr := s.store.UpdatePostDimensions(ctx, p.ID, int32(w), int32(h)); updateErr != nil {
			failed++
			continue
		}
		updated++
	}
	skipped = len(posts) - updated - failed

	return c.JSON(fiber.Map{
		"total":   len(posts),
		"updated": updated,
		"failed":  failed,
		"skipped": skipped,
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

// RegenerateImages re-encodes every stored image as a high-quality AVIF and
// rebuilds its thumbnail at current quality settings.  Old files are removed
// after a successful update.  The operation is safe to re-run.
//
// Images are processed in parallel: runtime.NumCPU()/4 workers (min 2, max 8)
// so that SVT-AV1's own multi-threading doesn't over-subscribe the machine.
// Progress is streamed via SSE — first event carries the total count.
//
// POST /api/admin/posts/regenerate-images
func (s *Server) RegenerateImages(c fiber.Ctx) error {
	posts, err := s.store.GetAllImagePosts(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "query failed: "+err.Error())
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	dirs := s.dirs
	log := s.log
	store := s.store

	// workers = NumCPU/4, bounded to [2, 8].
	// Each worker runs two SVT-AV1 encodes; limiting workers lets SVT-AV1 use
	// its own threading without over-saturating the CPU.
	workers := runtime.NumCPU() / 4
	if workers < 2 {
		workers = 2
	}
	if workers > 8 {
		workers = 8
	}

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		ctx := context.Background()

		total := len(posts)
		writeSSE(w, regenStartEvent{Phase: "start", Total: total})

		var (
			updated atomic.Int32
			failed  atomic.Int32
			skipped atomic.Int32
			current atomic.Int32
			mu      sync.Mutex // serialises SSE writes
		)

		sendProgress := func() {
			mu.Lock()
			writeSSE(w, regenProgressEvent{
				Phase:   "progress",
				Total:   total,
				Current: int(current.Load()),
				Updated: int(updated.Load()),
				Failed:  int(failed.Load()),
				Skipped: int(skipped.Load()),
			})
			mu.Unlock()
		}

		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup

		for _, p := range posts {
			wg.Add(1)
			p := p
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				srcPath := filepath.Join(dirs.Image, p.Filename)
				if _, statErr := os.Stat(srcPath); statErr != nil {
					skipped.Add(1)
					current.Add(1)
					sendProgress()
					return
				}

				// Unique filenames avoid clashing with source or sibling workers.
				newBase := utils.GenerateFilename("")
				newFilename := newBase + ".avif"
				newThumbFilename := newBase + "_thumb.avif"
				newFilePath := filepath.Join(dirs.Image, newFilename)
				newThumbPath := filepath.Join(dirs.Thumbnail, newThumbFilename)

				// Full-res encode: CRF 18 / preset 4 (≈ visually lossless), scaled down
				// to 920 px wide only when the source is wider than that.
				if encErr := utils.ConvertImageToAvif(srcPath, newFilePath, 18, 4, 920); encErr != nil {
					log.WarnContext(ctx, "regenerate: encode failed", "post", p.ID, "err", encErr)
					failed.Add(1)
					current.Add(1)
					sendProgress()
					return
				}

				// Decode freshly-encoded AVIF → JPEG so Go's image decoder can
				// read it for smartcrop (Go cannot natively decode AVIF).
				jpegPath, normErr := utils.NormalizeImageToJPEG(newFilePath, filepath.Join(dirs.Tmp, "thumbnails"))
				if normErr != nil {
					log.WarnContext(ctx, "regenerate: normalize failed", "post", p.ID, "err", normErr)
					removeFiles(newFilePath)
					failed.Add(1)
					current.Add(1)
					sendProgress()
					return
				}

				img, loadErr := utils.LoadImageFile(jpegPath)
				_ = os.Remove(jpegPath)
				if loadErr != nil {
					log.WarnContext(ctx, "regenerate: load failed", "post", p.ID, "err", loadErr)
					removeFiles(newFilePath)
					failed.Add(1)
					current.Add(1)
					sendProgress()
					return
				}

				// Thumbnail: CRF 30 / preset 6 (great quality at 150 px, fast).
				if thumbErr := utils.CreateThumbnailFromImage(img, newThumbPath, dirs); thumbErr != nil {
					log.WarnContext(ctx, "regenerate: thumbnail failed", "post", p.ID, "err", thumbErr)
					removeFiles(newFilePath)
					failed.Add(1)
					current.Add(1)
					sendProgress()
					return
				}

				w2, h2, _ := utils.GetVideoDimensions(newFilePath)

				if dbErr := store.UpdatePostFiles(ctx, p.ID, newFilename, newThumbFilename, int32(w2), int32(h2)); dbErr != nil {
					log.WarnContext(ctx, "regenerate: db update failed", "post", p.ID, "err", dbErr)
					removeFiles(newFilePath, newThumbPath)
					failed.Add(1)
					current.Add(1)
					sendProgress()
					return
				}

				// Remove old files only after the DB is committed.
				removePostFiles(dirs, p.Filename, p.ThumbnailFilename)
				updated.Add(1)
				current.Add(1)
				sendProgress()
			}()
		}
		wg.Wait()

		u, f, sk := int(updated.Load()), int(failed.Load()), int(skipped.Load())
		writeSSE(w, regenDoneEvent{
			Phase: "done", Total: total,
			Updated: u, Failed: f, Skipped: sk,
		})
		log.InfoContext(ctx, "image regeneration complete",
			"total", total, "updated", u, "failed", f, "skipped", sk,
			"workers", workers,
		)
	})
	return nil
}

// dimensionsForPost derives (width, height) from a post's media file on disk.
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
