package api

import (
	"errors"

	"ginbar/db"

	"github.com/gofiber/fiber/v3"
)

// POST /api/invite — authenticated user generates a new one-time invite token.
func (s *Server) CreateInvite(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	inv, err := s.store.CreateInvitation(c.Context(), u.ID)
	if err != nil {
		return err
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"token": inv.Token,
	})
}

// GET /api/invite — authenticated user lists all their invitations.
func (s *Server) ListMyInvites(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	invs, err := s.store.ListInvitationsByUser(c.Context(), u.ID)
	if err != nil {
		return err
	}

	// Return an empty array instead of null.
	if invs == nil {
		invs = []db.Invitation{}
	}
	return c.JSON(fiber.Map{"data": invs})
}

// GET /api/invite/:token — public; validates whether a token is usable.
func (s *Server) ValidateInvite(c fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return fiber.NewError(fiber.StatusBadRequest, "missing token")
	}

	inv, err := s.store.GetInvitation(c.Context(), token)
	if err != nil {
		if errors.Is(err, db.ErrInvitationNotFound) {
			return c.JSON(fiber.Map{"valid": false, "reason": "not found"})
		}
		return err
	}

	if inv.UsedBy.Valid {
		return c.JSON(fiber.Map{"valid": false, "reason": "already used"})
	}

	return c.JSON(fiber.Map{"valid": true})
}
