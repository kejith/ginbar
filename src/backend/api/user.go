package api

import "github.com/gofiber/fiber/v3"

func (s *Server) Me(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "not logged in"})
	}
	return c.JSON(u)
}

func (s *Server) GetUser(c fiber.Ctx) error {
	return fiber.ErrNotImplemented
}

func (s *Server) GetUsers(c fiber.Ctx) error {
	return fiber.ErrNotImplemented
}

func (s *Server) Login(c fiber.Ctx) error {
	return fiber.ErrNotImplemented
}

func (s *Server) Logout(c fiber.Ctx) error {
	return fiber.ErrNotImplemented
}

func (s *Server) CreateUser(c fiber.Ctx) error {
	return fiber.ErrNotImplemented
}
