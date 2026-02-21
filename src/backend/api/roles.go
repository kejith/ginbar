package api

import "github.com/gofiber/fiber/v3"

// Role level constants.  The users.level column (default 1) carries these
// values; higher values represent elevated permissions.
const (
	LevelGuest  = int32(0)  // unauthenticated visitor
	LevelMember = int32(1)  // regular registered user (DB default)
	LevelAdmin  = int32(10) // administrator
)

// requireAuth is a Fiber middleware that rejects non-logged-in requests.
func (s *Server) requireAuth(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}
	return c.Next()
}

// requireAdmin is a Fiber middleware that rejects requests from users whose
// level is below LevelAdmin.  It implicitly requires authentication.
func (s *Server) requireAdmin(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}
	if u.Level < LevelAdmin {
		return fiber.NewError(fiber.StatusForbidden, "admin access required")
	}
	return c.Next()
}
