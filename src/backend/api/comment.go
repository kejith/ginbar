package api

import (
	"wallium/cache"
	dbgen "wallium/db/gen"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgtype"
)

// ── Forms ─────────────────────────────────────────────────────────────────────

type commentWriteForm struct {
	Content  string `form:"content"   json:"content"`
	PostID   int32  `form:"post_id"   json:"post_id"`
	ParentID int32  `form:"parent_id" json:"parent_id"`
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
		ParentID: pgtype.Int4{Int32: form.ParentID, Valid: form.ParentID > 0},
	})
	if err != nil {
		return err
	}

	// If this is a reply, notify the parent comment's author.
	if form.ParentID > 0 {
		if parent, lookupErr := s.store.GetComment(c.Context(), form.ParentID); lookupErr == nil {
			go s.sendReplyNotification(parent.UserName, u.Name, form.Content, form.PostID, comment.ID)
		}
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

	if _, castErr := cache.CastVote(c.Context(), s.rdb, cache.EntityComment, form.CommentID, u.ID, form.VoteState); castErr != nil {
		return castErr
	}
	return c.SendStatus(fiber.StatusOK)
}
