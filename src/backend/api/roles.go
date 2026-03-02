package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"
)

// Role level constants.  The users.level column (default 1) carries these
// values; higher values represent elevated permissions.
const (
	LevelGuest  = int32(0)  // unauthenticated visitor
	LevelMember = int32(1)  // regular registered user (DB default)
	LevelSecret = int32(5)  // secret role — can see secret-filtered content
	LevelAdmin  = int32(10) // administrator
)

// AllFilters lists every valid post-filter value.
var AllFilters = []string{"sfw", "nsfp", "nsfw", "secret"}

// allowedFilters returns the slice of filter values the given user may see,
// restricted to those present in the (comma-separated) requestedFilters param.
//
//   - requestedFilters = ""            → return everything the user level allows
//   - requestedFilters = "sfw,nsfp"   → intersection with what the user may see
func allowedFilters(requestedFilters string, userLevel int32) []string {
	// Build the full set this user can see.
	allowed := []string{"sfw"}
	if userLevel >= LevelMember {
		allowed = append(allowed, "nsfp", "nsfw")
	}
	if userLevel >= LevelSecret {
		allowed = append(allowed, "secret")
	}

	if requestedFilters == "" {
		return allowed
	}

	// Index the allowed set for fast lookup.
	allowedSet := make(map[string]bool, len(allowed))
	for _, f := range allowed {
		allowedSet[f] = true
	}

	// Return the intersection of requested and allowed, preserving order.
	var result []string
	seen := make(map[string]bool)
	for _, f := range strings.Split(requestedFilters, ",") {
		f = strings.TrimSpace(f)
		if allowedSet[f] && !seen[f] {
			result = append(result, f)
			seen[f] = true
		}
	}
	return result
}

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

// requireSecret is a Fiber middleware that rejects requests from users whose
// level is below LevelSecret.  It implicitly requires authentication.
//
//nolint:unused
func (s *Server) requireSecret(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}
	if u.Level < LevelSecret {
		return fiber.NewError(fiber.StatusForbidden, "secret access required")
	}
	return c.Next()
}
