package api

import (
	"context"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"wallium/cache"
	walliumdb "wallium/db"
	dbgen "wallium/db/gen"
	"wallium/utils"

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
	username := c.Query("user")

	if username != "" {
		posts, err := s.store.SearchByUser(c.Context(), dbgen.SearchByUserParams{
			Tags:     tags,
			UserName: username,
		})
		if err != nil {
			return err
		}
		return c.JSON(fiber.Map{"posts": posts})
	}

	posts, err := s.store.Search(c.Context(), tags)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"posts": posts})
}

// GetPostPosition returns the 0-based offset and 1-based page number of a
// post in the default list (ORDER BY id DESC, limit=50). Used by the frontend
// to jump directly to the correct page when opening a deep /post/:id URL
// without chase-fetching every preceding page.
func (s *Server) GetPostPosition(c fiber.Ctx) error {
	const limit = 50

	id, err := strconv.ParseInt(c.Params("post_id", "0"), 10, 32)
	if err != nil || id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid post id")
	}

	u, _ := s.sessionUser(c)
	userLevel := int32(0)
	if u != nil {
		userLevel = u.Level
	}

	offset, err := s.store.GetPostOffset(c.Context(), int32(id), userLevel)
	if err != nil {
		return err
	}
	page := int(offset)/limit + 1
	return c.JSON(fiber.Map{"offset": offset, "page": page})
}

// GetPostsAround returns up to `limit` posts newer than + the target post +
// up to `limit` posts older than the given post id, all in DESC id order.
// Response: { posts, has_newer, has_older }
func (s *Server) GetPostsAround(c fiber.Ctx) error {
	const defaultLimit = int32(50)

	id, err := strconv.ParseInt(c.Params("post_id", "0"), 10, 32)
	if err != nil || id == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid post id")
	}

	u, _ := s.sessionUser(c)
	userID := int32(0)
	userLevel := int32(0)
	if u != nil {
		userID = u.ID
		userLevel = u.Level
	}

	newer, target, older, hasNewer, hasOlder, err := s.store.GetPostsAround(
		c.Context(), int32(id), userID, userLevel, defaultLimit,
	)
	if err != nil {
		return err
	}

	// Combine: newer (DESC) + target + older (DESC) → single DESC list.
	var posts []walliumdb.PostWithVote
	posts = append(posts, newer...)
	if target.ID != 0 {
		posts = append(posts, target)
	}
	posts = append(posts, older...)

	return c.JSON(fiber.Map{"posts": posts, "has_newer": hasNewer, "has_older": hasOlder})
}

// GetPostsCursor returns posts using cursor-based pagination.
// Query params (mutually exclusive):
//
//	before_id=X  →  posts with id < X, ORDER BY id DESC, LIMIT limit
//	after_id=X   →  posts with id > X, ORDER BY id DESC, LIMIT limit
func (s *Server) GetPostsCursor(c fiber.Ctx) error {
	const defaultLimit = int32(50)

	beforeStr := c.Query("before_id")
	afterStr := c.Query("after_id")

	u, _ := s.sessionUser(c)
	userID := int32(0)
	userLevel := int32(0)
	if u != nil {
		userID = u.ID
		userLevel = u.Level
	}

	var posts []walliumdb.PostWithVote
	var hasMore bool
	var err error

	switch {
	case beforeStr != "":
		beforeID, pErr := strconv.ParseInt(beforeStr, 10, 32)
		if pErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid before_id")
		}
		posts, err = s.store.GetPostsOlderThan(c.Context(), int32(beforeID), userID, userLevel, defaultLimit+1)
		if err != nil {
			return err
		}
		if len(posts) > int(defaultLimit) {
			hasMore = true
			posts = posts[:defaultLimit]
		}
	case afterStr != "":
		afterID, pErr := strconv.ParseInt(afterStr, 10, 32)
		if pErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid after_id")
		}
		posts, err = s.store.GetPostsNewerThan(c.Context(), int32(afterID), userID, userLevel, defaultLimit+1)
		if err != nil {
			return err
		}
		if len(posts) > int(defaultLimit) {
			hasMore = true
			posts = posts[:defaultLimit]
		}
	default:
		return fiber.NewError(fiber.StatusBadRequest, "before_id or after_id required")
	}

	return c.JSON(fiber.Map{"posts": posts, "has_more": hasMore})
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

	if _, castErr := cache.CastVote(c.Context(), s.rdb, cache.EntityPost, form.PostID, u.ID, form.VoteState); castErr != nil {
		return castErr
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
	return s.processAndInsertPostCtx(c.Context(), srcURL, inputFile, userName)
}

// processAndInsertPostCtx is the context-aware core of processAndInsertPost.
// It can be called from any goroutine without a live fiber.Ctx, making it
// suitable for batch-import workflows.
func (s *Server) processAndInsertPostCtx(ctx context.Context, srcURL, inputFile, userName string) (*dbgen.Post, error) {
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
		dups, _ := s.store.GetPossibleDuplicatePosts(ctx, dbgen.GetPossibleDuplicatePostsParams{
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
		params.Width = int32(res.Width)
		params.Height = int32(res.Height)

	case "video":
		vres, err := utils.ProcessVideo(inputFile, mimeType, s.dirs)
		if err != nil {
			return nil, fiber.NewError(fiber.StatusUnprocessableEntity, "video processing failed: "+err.Error())
		}
		params.ContentType = mimeType
		params.Filename = filepath.Base(vres.Filename)
		params.ThumbnailFilename = filepath.Base(vres.ThumbnailFilename)
		params.UploadedFilename = filepath.Base(inputFile)
		params.Width = int32(vres.Width)
		params.Height = int32(vres.Height)

	default:
		return nil, fiber.NewError(fiber.StatusUnsupportedMediaType, "unsupported file type: "+mimeType)
	}

	post, err := s.store.CreatePost(ctx, params)
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
