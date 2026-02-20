package api

import (
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	dbgen "ginbar/db/gen"
	"ginbar/utils"

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
	username := c.Query("username")

	u, _ := s.sessionUser(c)
	userLevel := int32(0)
	if u != nil {
		userLevel = u.Level
	}

	// filter by username if provided
	if username != "" {
		rows, err := s.store.GetPostsByUser(c.Context(), dbgen.GetPostsByUserParams{
			UserName:  username,
			UserLevel: userLevel,
		})
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"posts": rows})
	}

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

// CreatePost downloads a file from a URL, processes it, and inserts a post.
func (s *Server) CreatePost(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	form := new(postURLForm)
	if err := c.Bind().Body(form); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if form.URL == "" {
		return fiber.NewError(fiber.StatusBadRequest, "URL is required")
	}

	tmpPath, err := utils.DownloadFile(form.URL, s.dirs.Tmp)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "could not download file: "+err.Error())
	}
	defer os.Remove(tmpPath)

	post, err := s.processAndInsertPost(c, form.URL, tmpPath, u.Name)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "postCreated", "posts": []dbgen.Post{*post}})
}

// UploadPost saves a multipart file, processes it, and inserts a post.
func (s *Server) UploadPost(c fiber.Ctx) error {
	u, err := s.sessionUser(c)
	if err != nil || u == nil || u.ID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "not logged in")
	}

	fh, err := c.FormFile("file")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "file field missing: "+err.Error())
	}

	tmpPath := filepath.Join(s.dirs.Tmp, filepath.Base(fh.Filename))
	if err := c.SaveFile(fh, tmpPath); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "could not save upload: "+err.Error())
	}
	defer os.Remove(tmpPath)

	post, err := s.processAndInsertPost(c, "", tmpPath, u.Name)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "postCreated", "posts": []dbgen.Post{*post}})
}

// processAndInsertPost is shared by CreatePost and UploadPost.
// It detects the content type, runs the image/video pipeline, deduplicates,
// and inserts a post row.
func (s *Server) processAndInsertPost(c fiber.Ctx, srcURL, inputFile, userName string) (*dbgen.Post, error) {
	mimeType := mime.TypeByExtension(filepath.Ext(inputFile))
	fileType := strings.SplitN(mimeType, "/", 2)[0]

	params := dbgen.CreatePostParams{
		Url:      srcURL,
		UserName: userName,
	}

	switch fileType {
	case "image":
		res, err := utils.ProcessImage(inputFile, s.dirs)
		if err != nil {
			return nil, fiber.NewError(fiber.StatusUnprocessableEntity, "image processing failed: "+err.Error())
		}

		// perceptual-hash duplicate check
		h := res.PerceptionHash.GetHash()
		dups, _ := s.store.GetPossibleDuplicatePosts(c.Context(), dbgen.GetPossibleDuplicatePostsParams{
			Column1: int64(h[0]),
			Column2: int64(h[1]),
			Column3: int64(h[2]),
			Column4: int64(h[3]),
		})
		if len(dups) > 0 {
			return nil, fiber.NewError(fiber.StatusConflict, "possible duplicate post")
		}

		params.PHash0 = int64(h[0])
		params.PHash1 = int64(h[1])
		params.PHash2 = int64(h[2])
		params.PHash3 = int64(h[3])
		params.ContentType = "image"
		params.Filename = res.Filename
		params.ThumbnailFilename = res.ThumbnailFilename
		params.UploadedFilename = res.UploadedFilename

	case "video":
		filename, thumb, err := utils.ProcessVideo(inputFile, mimeType, s.dirs)
		if err != nil {
			return nil, fiber.NewError(fiber.StatusUnprocessableEntity, "video processing failed: "+err.Error())
		}
		params.ContentType = mimeType
		params.Filename = filepath.Base(filename)
		params.ThumbnailFilename = filepath.Base(thumb)
		params.UploadedFilename = filepath.Base(inputFile)

	default:
		return nil, fiber.NewError(fiber.StatusUnsupportedMediaType, "unsupported file type: "+mimeType)
	}

	post, err := s.store.CreatePost(c.Context(), params)
	if err != nil {
		return nil, err
	}
	return &post, nil
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
