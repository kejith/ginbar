package api

import (
	"strconv"
	"strings"

	dbgen "ginbar/db/gen"

	"github.com/gofiber/fiber/v3"
)

// ── Forms ─────────────────────────────────────────────────────────────────────

type postURLForm struct {
	URL string `form:"URL" json:"URL"`
}

type postVoteForm struct {
	PostID    int32 `form:"post_id"    json:"post_id"`
	VoteState int16 `form:"vote_state" json:"vote_state"`
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (s *Server) GetPosts(c fiber.Ctx) error {
	page := queryInt(c, "page", 1)
	limit := queryInt(c, "limit", 50)
	offset := int32((page - 1) * limit)

	u, _ := s.sessionUser(c)

	if u != nil && u.ID > 0 {
		rows, err := s.store.GetVotedPosts(c.Context(), dbgen.GetVotedPostsParams{
			UserID:    u.ID,
			UserLevel: u.Level,
			Limit:     int32(limit),
			Offset:    offset,
		})
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"posts": rows})
	}

	rows, err := s.store.GetPosts(c.Context(), dbgen.GetPostsParams{
		UserLevel: 0,
		Limit:     int32(limit),
		Offset:    offset,
	})
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"posts": rows})
}

func (s *Server) Search(c fiber.Ctx) error {
	query := c.Params("query")
	tags := strings.Split(query, "%20")
	posts, err := s.store.Search(c.Context(), tags)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"posts": posts})
}

func (s *Server) GetPost(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("post_id", "0"), 10, 32)
	if err != nil || id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid post id")
	}

	u, _ := s.sessionUser(c)

	if u != nil && u.ID > 0 {
		post, err := s.store.GetVotedPost(c.Context(), dbgen.GetVotedPostParams{
			UserID:    u.ID,
			ID:        int32(id),
			UserLevel: u.Level,
		})
		if err != nil {
			return err
		}
		comments, _ := s.store.GetVotedComments(c.Context(), dbgen.GetVotedCommentsParams{
			UserID: u.ID,
			PostID: post.ID,
		})
		tags, _ := s.store.GetTagsByPost(c.Context(), dbgen.GetTagsByPostParams{
			UserID: u.ID,
			PostID: post.ID,
		})
		return c.JSON(fiber.Map{"data": post, "comments": comments, "tags": tags})
	}

	post, err := s.store.GetPost(c.Context(), dbgen.GetPostParams{
		ID:        int32(id),
		UserLevel: 0,
	})
	if err != nil {
		return err
	}
	tags, _ := s.store.GetTagsByPost(c.Context(), dbgen.GetTagsByPostParams{
		UserID: 0,
		PostID: post.ID,
	})
	return c.JSON(fiber.Map{"data": post, "comments": nil, "tags": tags})
}

func (s *Server) VotePost(c fiber.Ctx) error {
	form := new(postVoteForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	if form.VoteState != 0 {
		err = s.store.UpsertPostVote(c.Context(), dbgen.UpsertPostVoteParams{
			PostID: form.PostID,
			UserID: u.ID,
			Vote:   form.VoteState,
		})
	} else {
		err = s.store.DeletePostVote(c.Context(), dbgen.DeletePostVoteParams{
			PostID: form.PostID,
			UserID: u.ID,
		})
	}
	if err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusOK)
}

// CreatePost and UploadPost require the utils image/video pipeline (Chunk 5).

func (s *Server) CreatePost(c fiber.Ctx) error {
	// TODO(chunk5): download URL → process image/video → insert post
	return fiber.NewError(fiber.StatusNotImplemented, "not yet implemented")
}

func (s *Server) UploadPost(c fiber.Ctx) error {
	// TODO(chunk5): save multipart file → process image/video → insert post
	return fiber.NewError(fiber.StatusNotImplemented, "not yet implemented")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func queryInt(c fiber.Ctx, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}
