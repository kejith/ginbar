package api

import (
"io/fs"
"os"
"path/filepath"
"strconv"

dbgen "ginbar/db/gen"

"github.com/gofiber/fiber/v3"
)

// ── Disk-usage helper ─────────────────────────────────────────────────────────

// diskCategory holds size metrics for a single directory.
type diskCategory struct {
Label string `json:"label"`
Bytes int64  `json:"bytes"`
Files int64  `json:"files"`
}

// dirSize walks root recursively and accumulates total size and file count.
func dirSize(root, label string) diskCategory {
cat := diskCategory{Label: label}
_ = filepath.WalkDir(root, func(_ string, e fs.DirEntry, err error) error {
if err != nil || e.IsDir() {
return nil
}
info, statErr := e.Info()
if statErr == nil {
cat.Bytes += info.Size()
cat.Files++
}
return nil
})
return cat
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// GetAdminStats returns aggregate counts and per-category disk usage.
//
// GET /api/admin/stats
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
dirty, _ := s.store.CountDirtyPosts(ctx) // non-fatal if missing

// Disk usage per media category.
// NOTE: s.dirs.Image = public/images which includes thumbnails subdir.
upload := dirSize(s.dirs.Upload, "uploads")
images := dirSize(s.dirs.Image, "images")
videos := dirSize(s.dirs.Video, "videos")
totalBytes := upload.Bytes + images.Bytes + videos.Bytes

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
"breakdown":   []diskCategory{upload, images, videos},
},
})
}

// ListUsers returns all users (id, name, email, level, created_at) for the
// admin user management table.
//
// GET /api/admin/users
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

// Prevent self-demotion so the acting admin cannot lock themselves out.
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

// AdminDeletePost soft-deletes any post by id (regardless of ownership) and
// removes the associated media files from disk (best-effort).
//
// DELETE /api/admin/posts/:id
func (s *Server) AdminDeletePost(c fiber.Ctx) error {
id, err := strconv.ParseInt(c.Params("id"), 10, 32)
if err != nil || id == 0 {
return fiber.NewError(fiber.StatusBadRequest, "invalid post id")
}

// Best-effort: fetch post before deleting to remove media files.
post, dbErr := s.store.GetPostAdmin(c.Context(), int32(id))
if dbErr == nil {
_ = os.Remove(filepath.Join(s.dirs.Image, post.Filename))
_ = os.Remove(filepath.Join(s.dirs.Thumbnail, post.ThumbnailFilename))
_ = os.Remove(filepath.Join(s.dirs.Upload, post.UploadedFilename))
_ = os.Remove(filepath.Join(s.dirs.Video, post.Filename))
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
