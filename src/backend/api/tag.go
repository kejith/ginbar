package api

import (
	"wallium/cache"
	dbgen "wallium/db/gen"

	"github.com/gofiber/fiber/v3"
)

// ── Forms ─────────────────────────────────────────────────────────────────────

type createPostTagForm struct {
	Name   string `form:"name"   json:"name"`
	PostID int32  `form:"post_id" json:"post_id"`
}

type postTagVoteForm struct {
	PostTagID int32 `form:"post_tag_id" json:"post_tag_id"`
	VoteState int16 `form:"vote_state"  json:"vote_state"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// GetTags returns all tags (used by the admin panel tag browser).
func (s *Server) GetTags(c fiber.Ctx) error {
	tags, err := s.store.GetTags(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"tags": tags})
}

func (s *Server) CreatePostTag(c fiber.Ctx) error {
	form := new(createPostTagForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if form.PostID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid post_id")
	}

	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	// Upsert the tag (creates new or returns existing by name).
	tag, err := s.store.CreateTag(c.Context(), form.Name)
	if err != nil {
		return err
	}

	postTag, err := s.store.AddTagToPost(c.Context(), dbgen.AddTagToPostParams{
		TagID:  tag.ID,
		PostID: form.PostID,
		UserID: u.ID,
	})
	if err != nil {
		return err
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":      postTag.ID,
		"score":   postTag.Score,
		"name":    tag.Name,
		"post_id": postTag.PostID,
		"user_id": postTag.UserID,
	})
}

func (s *Server) VotePostTag(c fiber.Ctx) error {
	form := new(postTagVoteForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if form.PostTagID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid post_tag_id")
	}

	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	if _, castErr := cache.CastVote(c.Context(), s.rdb, cache.EntityPostTag, form.PostTagID, u.ID, form.VoteState); castErr != nil {
		return castErr
	}
	return c.SendStatus(fiber.StatusOK)
}
