package api

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"ginbar/db"
	"ginbar/utils"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/session"
	"github.com/gofiber/fiber/v3/middleware/static"
	pgstore "github.com/gofiber/storage/postgres"
)

// SessionUser is the shape stored in every session — keep stable across
// requests; handlers cast via sess.Get("user").
type SessionUser struct {
	ID    int32  `json:"id"`
	Name  string `json:"name"`
	Level int32  `json:"level"`
}

// Server holds the Fiber app, DB store, and session store.
type Server struct {
	App      *fiber.App
	store    *db.Store
	sessions *session.Store
	log      *slog.Logger
	dirs     utils.Directories
}

// NewServer wires up the Fiber v3 application.
func NewServer(store *db.Store, sessionSecret string, log *slog.Logger) *Server {
	app := fiber.New(fiber.Config{
		// Structured JSON error responses.
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			var fiberErr *fiber.Error
			if errors.As(err, &fiberErr) {
				code = fiberErr.Code
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	// ── Session store (postgres-backed) ──────────────────────────────────────
	pgSt := pgstore.New(pgstore.Config{
		// Re-use the same PG connection string from env.  The storage package
		// uses database/sql internally so it manages its own small pool.
		ConnectionURI: getDBURL(),
		Table:         "sessions",
		Reset:         false,
		GCInterval:    10 * time.Minute,
	})
	sessions := session.New(session.Config{
		Storage:    pgSt,
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
		sessions: sessions,
		log:      log,
		dirs:     dirs,
	}

	// ── Global middleware ─────────────────────────────────────────────────────
	app.Use(recover.New())
	app.Use(slogMiddleware(log))
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
	user.Post("/logout", srv.Logout)
	user.Post("/create", srv.CreateUser)

	// Posts
	post := api.Group("/post")
	post.Get("/search/", srv.GetPosts)
	post.Get("/search/:query", srv.Search)
	post.Get("/:post_id", srv.GetPost)
	post.Get("/*", srv.GetPosts)
	post.Post("/vote", srv.VotePost)
	post.Post("/create", srv.CreatePost)
	post.Post("/upload", srv.UploadPost)

	// Comments
	comment := api.Group("/comment")
	comment.Post("/create", srv.CreateComment)
	comment.Post("/vote", srv.VoteComment)

	// Tags
	tag := api.Group("/tag")
	tag.Post("/create", srv.CreatePostTag)
	tag.Post("/vote", srv.VotePostTag)

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

// ── slog request logger middleware ───────────────────────────────────────────

func slogMiddleware(log *slog.Logger) fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		log.InfoContext(
			context.Background(),
			"request",
			"method", c.Method(),
			"path", c.Path(),
			"status", c.Response().StatusCode(),
			"latency", time.Since(start).String(),
			"ip", c.IP(),
		)
		return err
	}
}

// ── Env helper (mirrors main.go) ──────────────────────────────────────────────

func getDBURL() string {
	if v := os.Getenv("DB_URL"); v != "" {
		return v
	}
	return "postgres://ginbar:devpassword@localhost:5432/ginbar?sslmode=disable"
}
