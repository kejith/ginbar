package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"time"

	"wallium/db"
	"wallium/utils"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/session"
	"github.com/gofiber/fiber/v3/middleware/static"
	redisstore "github.com/gofiber/storage/redis/v2"
	"github.com/redis/go-redis/v9"
)

// SessionUser is the shape stored in every session — keep stable across
// requests; handlers cast via sess.Get("user").
type SessionUser struct {
	ID    int32  `json:"id"`
	Name  string `json:"name"`
	Level int32  `json:"level"`
}

// Server holds the Fiber app, DB store, session store, and Redis client.
type Server struct {
	App      *fiber.App
	store    *db.Store
	rdb      *redis.Client
	sessions *session.Store
	log      *slog.Logger
	dirs     utils.Directories
}

// NewServer wires up the Fiber v3 application.
func NewServer(store *db.Store, rdb *redis.Client, sessionSecret string, log *slog.Logger) *Server {
	app := fiber.New(fiber.Config{
		// Structured JSON error responses.
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fiberErr *fiber.Error
			if errors.As(err, &fiberErr) {
				code = fiberErr.Code
			}

			reqID, _ := c.Locals("request_id").(string)
			attrs := []any{
				"request_id", reqID,
				"method", c.Method(),
				"path", c.Path(),
				"status", code,
				"err", err.Error(),
				"body", maskBody(c.Body()),
			}
			if code >= 500 {
				log.Error("request error", attrs...)
			} else {
				log.Warn("request error", attrs...)
			}

			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	// ── Session store (redis-backed) ─────────────────────────────────────────
	// Reuse the same Redis connection parameters as the vote-buffer client.
	opts := rdb.Options()
	host, port, _ := net.SplitHostPort(opts.Addr)
	redisPort, _ := strconv.Atoi(port)
	sessionSt := redisstore.New(redisstore.Config{
		Host:      host,
		Port:      redisPort,
		Password:  opts.Password,
		Database:  opts.DB,
		Reset:     false,
	})
	sessions := session.New(session.Config{
		Storage:    sessionSt,
		Expiration: 7 * 24 * time.Hour,
		KeyLookup:  "cookie:session_id",
	})
	// Register the session user type for gob encoding.
	sessions.RegisterType(SessionUser{})

	cwd, _ := os.Getwd()
	dirs := utils.SetupDirectories(cwd)

	// Ensure required subdirs exist.
	for _, d := range []string{dirs.Image, dirs.Thumbnail, dirs.Video, dirs.Tmp,
		filepath.Join(dirs.Tmp, "thumbnails"), dirs.Upload} {
		_ = os.MkdirAll(d, 0o755)
	}

	srv := &Server{
		App:      app,
		store:    store,
		rdb:      rdb,
		sessions: sessions,
		log:      log,
		dirs:     dirs,
	}

	// ── Global middleware ─────────────────────────────────────────────────────
	// requestIDMiddleware first — all subsequent middleware/handlers can read it.
	app.Use(requestIDMiddleware())
	// Panic recovery wired through slog so panics appear in the structured log.
	app.Use(panicRecoveryMiddleware(log))
	app.Use(srv.slogMiddleware())
	app.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5173", "http://localhost:3000"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept"},
		AllowCredentials: true,
	}))

	// ── Routes ────────────────────────────────────────────────────────────────
	api := app.Group("/api")

	// Auth check
	api.Get("/check/me", srv.Me)

	// Users
	user := api.Group("/user")
	user.Get("/:id", srv.GetUser)
	user.Get("/*", srv.GetUsers)
	user.Post("/login", srv.Login)
	user.Post("/logout", srv.requireAuth, srv.Logout)
	user.Post("/create", srv.CreateUser)

	// Invitations
	invite := api.Group("/invite")
	invite.Post("/", srv.requireAuth, srv.CreateInvite)
	invite.Get("/", srv.requireAuth, srv.ListMyInvites)
	invite.Get("/:token", srv.ValidateInvite)

	// Posts
	post := api.Group("/post")
	post.Get("/search/", srv.GetPosts)
	post.Get("/search/:query", srv.Search)
	post.Get("/:post_id", srv.GetPost)
	post.Get("/*", srv.GetPosts)
	post.Post("/vote", srv.requireAuth, srv.VotePost)
	post.Post("/create", srv.requireAuth, srv.CreatePost)
	post.Post("/upload", srv.requireAuth, srv.UploadPost)
	// Import is restricted to admins.
	post.Post("/import/pr0gramm", srv.requireAdmin, srv.ImportFromPr0gramm)

	// Comments
	comment := api.Group("/comment")
	comment.Post("/create", srv.requireAuth, srv.CreateComment)
	comment.Post("/vote", srv.requireAuth, srv.VoteComment)

	// Messages
	msg := api.Group("/message", srv.requireAuth)
	msg.Get("/unread", srv.GetUnreadCount)
	msg.Get("/inbox", srv.GetInbox)
	msg.Get("/notifications", srv.GetNotificationsPage)
	msg.Get("/thread/:partner", srv.GetThread)
	msg.Post("/send", srv.SendMessage)
	msg.Post("/mark-read", srv.MarkMessageRead)
	msg.Post("/mark-all-read", srv.MarkAllRead)

	// Tags
	tag := api.Group("/tag")
	tag.Get("/", srv.GetTags)
	tag.Post("/create", srv.requireAuth, srv.CreatePostTag)
	tag.Post("/vote", srv.requireAuth, srv.VotePostTag)

	// ── Admin routes (all require level >= LevelAdmin) ────────────────────────
	admin := api.Group("/admin", srv.requireAdmin)
	admin.Get("/stats", srv.GetAdminStats)
	admin.Get("/users", srv.ListUsers)
	admin.Get("/comments", srv.AdminListComments)
	admin.Patch("/users/:id/level", srv.AdminUpdateUserLevel)
	admin.Delete("/users/:id", srv.AdminDeleteUser)
	admin.Delete("/posts/:id", srv.AdminDeletePost)
	admin.Delete("/comments/:id", srv.AdminDeleteComment)
	admin.Delete("/tags/:id", srv.AdminDeleteTag)
	admin.Post("/posts/backfill-dimensions", srv.BackfillPostDimensions)
	admin.Post("/posts/regenerate-images", srv.RegenerateImages)
	admin.Post("/message/broadcast", srv.BroadcastMessage)

	// Static — SPA fallback (frontend served separately in dev via Vite proxy)
	app.Use("/", static.New("./public", static.Config{Browse: false}))

	return srv
}

// ── Session helpers ───────────────────────────────────────────────────────────

func (s *Server) sessionGet(c fiber.Ctx) (*session.Session, error) {
	return s.sessions.Get(c)
}

func (s *Server) sessionUser(c fiber.Ctx) (*SessionUser, error) {
	sess, err := s.sessions.Get(c)
	if err != nil {
		return nil, err
	}
	u, ok := sess.Get("user").(SessionUser)
	if !ok {
		return nil, nil
	}
	return &u, nil
}

// ── Middleware ────────────────────────────────────────────────────────────────

// requestIDMiddleware generates a unique correlation ID for every request,
// stores it in c.Locals("request_id"), and echoes it in the response header.
func requestIDMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		id := newRequestID()
		c.Locals("request_id", id)
		c.Set("X-Request-ID", id)
		return c.Next()
	}
}

// panicRecoveryMiddleware catches any panic from downstream handlers, logs it
// with a full stack trace via slog, and returns a 500 response.
func panicRecoveryMiddleware(log *slog.Logger) fiber.Handler {
	return func(c fiber.Ctx) (err error) {
		defer func() {
			if r := recover(); r != nil {
				reqID, _ := c.Locals("request_id").(string)
				log.Error("panic recovered",
					"request_id", reqID,
					"method", c.Method(),
					"path", c.Path(),
					"panic", r,
					"stack", string(debug.Stack()),
				)
				err = fiber.ErrInternalServerError
			}
		}()
		return c.Next()
	}
}

// slogMiddleware logs every completed request at INFO level with the
// correlation ID, method, path, status, latency, IP, and (if authenticated)
// user identity.
func (s *Server) slogMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		err := c.Next()

		reqID, _ := c.Locals("request_id").(string)

		attrs := []any{
			"request_id", reqID,
			"method", c.Method(),
			"path", c.Path(),
			"status", c.Response().StatusCode(),
			"latency", time.Since(start).String(),
			"ip", c.IP(),
		}

		// Attach user identity when a session is present — best-effort, no error.
		if u, _ := s.sessionUser(c); u != nil {
			attrs = append(attrs, "user_id", u.ID, "username", u.Name)
		}

		s.log.InfoContext(context.Background(), "request", attrs...)
		return err
	}
}
