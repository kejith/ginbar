package api

import (
	dbgen "ginbar/db/gen"

	"github.com/gofiber/fiber/v3"
)

// ── Forms ─────────────────────────────────────────────────────────────────────

type commentWriteForm struct {
	Content string `form:"content" json:"content"`
	PostID  int32  `form:"post_id" json:"post_id"`
}

type commentVoteForm struct {
	CommentID int32 `form:"comment_id" json:"comment_id"`
	VoteState int16 `form:"vote_state" json:"vote_state"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (s *Server) CreateComment(c fiber.Ctx) error {
	form := new(commentWriteForm)
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

	comment, err := s.store.CreateComment(c.Context(), dbgen.CreateCommentParams{
		Content:  form.Content,
		UserName: u.Name,
		PostID:   form.PostID,
	})
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(comment)
}

func (s *Server) VoteComment(c fiber.Ctx) error {
	form := new(commentVoteForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if form.CommentID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid comment_id")
	}

	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	if form.VoteState != 0 {
		err = s.store.UpsertCommentVote(c.Context(), dbgen.UpsertCommentVoteParams{
			CommentID: form.CommentID,
			UserID:    u.ID,
			Vote:      form.VoteState,
		})
	} else {
		err = s.store.DeleteCommentVote(c.Context(), dbgen.DeleteCommentVoteParams{
			CommentID: form.CommentID,
			UserID:    u.ID,
		})
	}
	if err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusOK)
}
