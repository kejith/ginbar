package api

import (
	"errors"
	"regexp"
	"strconv"

	"wallium/db"
	dbgen "wallium/db/gen"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

// ── Forms ─────────────────────────────────────────────────────────────────────

type loginForm struct {
	Name     string `form:"name"     json:"name"`
	Password string `form:"password" json:"password"`
}

type registerForm struct {
	Name        string `form:"name"         json:"name"`
	Email       string `form:"email"        json:"email"`
	Password    string `form:"password"     json:"password"`
	InviteToken string `form:"invite_token" json:"invite_token"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (s *Server) Me(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "not logged in"})
	}
	return c.JSON(u)
}

func (s *Server) GetUsers(c fiber.Ctx) error {
	users, err := s.store.GetUsers(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"data": users})
}

func (s *Server) GetUser(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 32)
	if err != nil || id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid user id")
	}
	user, err := s.store.GetUser(c.Context(), int32(id))
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"data": user})
}

func (s *Server) Login(c fiber.Ctx) error {
	form := new(loginForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	user, err := s.store.GetUserByName(c.Context(), form.Name)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid credentials")
	}

	if err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(form.Password)); err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "invalid credentials")
	}

	sess, err := s.sessionGet(c)
	if err != nil {
		return err
	}
	sess.Set("user", SessionUser{ID: user.ID, Name: user.Name, Level: user.Level})
	if err = sess.Save(); err != nil {
		return err
	}

	return c.JSON(fiber.Map{"id": user.ID, "name": user.Name, "level": user.Level})
}

func (s *Server) Logout(c fiber.Ctx) error {
	sess, err := s.sessionGet(c)
	if err != nil {
		return err
	}
	return sess.Destroy()
}

func (s *Server) CreateUser(c fiber.Ctx) error {
	form := new(registerForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// Must not already be logged in.
	if u, _ := s.sessionUser(c); u != nil && u.ID > 0 {
		return fiber.NewError(fiber.StatusConflict, "already logged in")
	}

	// Validate invite token — required for all registrations.
	if form.InviteToken == "" {
		return fiber.NewError(fiber.StatusForbidden, "an invitation is required to register")
	}
	inv, err := s.store.GetInvitation(c.Context(), form.InviteToken)
	if err != nil {
		if errors.Is(err, db.ErrInvitationNotFound) {
			return fiber.NewError(fiber.StatusForbidden, "invalid invitation token")
		}
		return err
	}
	if inv.UsedBy.Valid {
		return fiber.NewError(fiber.StatusForbidden, "invitation already used")
	}

	if len(form.Name) < 4 {
		return fiber.NewError(fiber.StatusBadRequest, "name too short (min 4 chars)")
	}
	if !isEmailValid(form.Email) {
		return fiber.NewError(fiber.StatusBadRequest, "invalid email address")
	}

	hash, hashErr := bcrypt.GenerateFromPassword([]byte(form.Password), bcrypt.DefaultCost)
	if hashErr != nil {
		return hashErr
	}

	newUser, createErr := s.store.CreateUser(c.Context(), dbgen.CreateUserParams{
		Name:     form.Name,
		Email:    form.Email,
		Password: string(hash),
	})
	if createErr != nil {
		return createErr
	}

	// Mark the invitation as consumed by the newly-created user.
	if useErr := s.store.UseInvitation(c.Context(), form.InviteToken, newUser.ID); useErr != nil {
		// Non-fatal: user was already created; log but don't fail the request.
		s.log.Error("failed to mark invitation used", "token", form.InviteToken, "err", useErr)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"name": form.Name})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

var emailRx = regexp.MustCompile(`^[a-zA-Z0-9.!#$%&'*+\/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

func isEmailValid(e string) bool {
	if len(e) < 3 || len(e) > 254 {
		return false
	}
	if !emailRx.MatchString(e) {
		return false
	}
	return true
}
