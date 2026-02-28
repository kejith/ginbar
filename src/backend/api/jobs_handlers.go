package api

import (
	"bufio"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
)

// ── Admin job handlers ────────────────────────────────────────────────────────

// ListAllJobs returns every registered job (admin-only).
// GET /api/admin/jobs
func (s *Server) ListAllJobs(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"jobs": s.jobs.ListAll()})
}

// CancelJob requests cancellation of a job by ID (admin-only).
// POST /api/admin/jobs/:id/cancel
func (s *Server) CancelJob(c fiber.Ctx) error {
	id := c.Params("id")
	if err := s.jobs.Cancel(id); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	j := s.jobs.Get(id)
	if j == nil {
		return c.JSON(fiber.Map{"status": "cancelled"})
	}
	return c.JSON(j.Snapshot())
}

// AdminJobsStream streams all job snapshots via SSE (admin-only).
// GET /api/admin/jobs/stream
func (s *Server) AdminJobsStream(c fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	ch := s.jobs.Subscribe()
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer s.jobs.Unsubscribe(ch)

		// Send initial snapshot immediately.
		writeSSE(w, fiber.Map{"jobs": s.jobs.ListAll()})

		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case snaps, ok := <-ch:
				if !ok {
					return
				}
				writeSSE(w, fiber.Map{"jobs": snaps})
			case <-heartbeat.C:
				fmt.Fprintf(w, ": heartbeat\n\n")
				w.Flush()
			}
		}
	})
	return nil
}

// ── Authenticated user job handlers ──────────────────────────────────────────

// ListMyJobs returns jobs visible to the current user.
// GET /api/jobs
func (s *Server) ListMyJobs(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}
	return c.JSON(fiber.Map{"jobs": s.jobs.ListVisible(u.ID, u.Level)})
}

// UserJobsStream streams job snapshots visible to the current user via SSE.
// GET /api/jobs/stream
func (s *Server) UserJobsStream(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	viewerID := u.ID
	viewerLevel := u.Level

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	ch := s.jobs.Subscribe()
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer s.jobs.Unsubscribe(ch)

		// Send initial snapshot.
		writeSSE(w, fiber.Map{"jobs": s.jobs.ListVisible(viewerID, viewerLevel)})

		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case _, ok := <-ch:
				if !ok {
					return
				}
				// Re-filter for this user (the broadcast sends all snapshots).
				writeSSE(w, fiber.Map{"jobs": s.jobs.ListVisible(viewerID, viewerLevel)})
			case <-heartbeat.C:
				fmt.Fprintf(w, ": heartbeat\n\n")
				w.Flush()
			}
		}
	})
	return nil
}
