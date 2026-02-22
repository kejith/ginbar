package api

import "github.com/gofiber/fiber/v3"

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
// optionally restricted to a single requested filter.
//
//   - requestedFilter = ""     → return everything the user level allows
//   - requestedFilter = "nsfw" → members see nsfp+nsfw; secret/admin see nsfw only
//   - any other value          → exact match, subject to level check
func allowedFilters(requestedFilter string, userLevel int32) []string {
	// Build the full set this user can see.
	allowed := []string{"sfw"}
	if userLevel >= LevelMember {
		allowed = append(allowed, "nsfp", "nsfw")
	}
	if userLevel >= LevelSecret {
		allowed = append(allowed, "secret")
	}

	if requestedFilter == "" {
		return allowed
	}

	// For members using the coarse "nsfw" toggle, also include nsfp.
	if requestedFilter == "nsfw" && userLevel >= LevelMember && userLevel < LevelSecret {
		return []string{"nsfp", "nsfw"}
	}

	// Honour the exact request only when the user is allowed to see it.
	for _, f := range allowed {
		if f == requestedFilter {
			return []string{requestedFilter}
		}
	}
	// Requested filter is above the user's level — return nothing visible.
	return []string{}
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
