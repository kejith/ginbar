package api

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	dbgen "ginbar/db/gen"
	"ginbar/utils"

	"github.com/gofiber/fiber/v3"
)

// ── Disk-usage helper ─────────────────────────────────────────────────────────

type diskUsage struct {
	Path  string `json:"path"`
	Label string `json:"label"`
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
}

func dirUsage(root, label string) diskUsage {
	d := diskUsage{Path: root, Label: label}
	_ = filepath.WalkDir(root, func(_ string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
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
		dirUsage(s.dirs.Image, "images"),
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

// AdminDeleteTag hard-deletes a tag by id.
//
// DELETE /api/admin/tags/:id
func (s *Server) AdminDeleteTag(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 32)
	if err != nil || id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid tag id")
	}
	if err := s.store.DeleteTag(c.Context(), int32(id)); err != nil {
		return err
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

// dimensionsForPost derives (width, height) from a post's media file on disk.
// It uses ffprobe for all file types since the stored images are WebP and
// Go's standard image.Decode does not support WebP natively.
func dimensionsForPost(p dbgen.Post, dirs utils.Directories) (int, int, error) {
	isVideo := strings.HasPrefix(p.ContentType, "video/")
	if isVideo {
		filePath := filepath.Join(dirs.Video, p.Filename)
		return utils.GetVideoDimensions(filePath)
	}
	// Images are stored as .webp — use ffprobe which handles both.
	filePath := filepath.Join(dirs.Image, p.Filename)
	return utils.GetVideoDimensions(filePath)
}
