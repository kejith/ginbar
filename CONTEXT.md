# Ginbar Rewrite — Session Context
> Full rewrite plan: `_plan/PLAN.md` · Legacy reference: `_ref/README.md`
> Read this first when starting a new conversation inside the devcontainer.

---

## Status: Chunks 0–5 complete — backend done, frontend not started

### Chunk progress
| # | Status | Notes |
|---|---|---|
| 0 | ✅ | Devcontainer, plan docs, repo scaffold |
| 1 | ✅ | 8 goose PG migrations — `make migrate-up` verified (version 8) |
| 2 | ✅ | `go.mod` (Go 1.23, pgx/v5.6.0, fiber/v3-beta.3), sqlc.yaml, 8 query files, 11 generated files, `db/store.go` |
| 3 | ✅ | `main.go` (pgxpool, graceful shutdown), `api/server.go` (Fiber v3, sessions, CORS, slog middleware, all routes) |
| 4 | ✅ | `api/user.go`, `api/post.go`, `api/comment.go`, `api/tag.go` — all handlers implemented |
| 5 | ✅ | `utils/` — download (30s timeout, max 5 redirects), image (cwebp+goimagehash+smartcrop), video (ffmpeg), validation (bcrypt); CreatePost/UploadPost fully wired |
| 6 | ⏳ | **NEXT** — Vite scaffold in `src/frontend/` |
| 7 | ⏳ | Zustand stores, axios api.js |
| 8 | ⏳ | UI layout (full-width grid, Tailwind v4) |
| 9 | ⏳ | Production Docker Compose + nginx |

### Git state
- Branch: `rewrite` (on top of `master`)
- 6 commits: chunk 0 → chunk 5
- `go build ./...` → clean binary ✅

---

## Backend layout (complete)
```
src/backend/
  main.go                   # pgxpool connect, signal handler, graceful shutdown
  go.mod / go.sum           # Go 1.23; pgx/v5.6.0, fiber/v3-beta.3, x/crypto, goimagehash, smartcrop, libwebp
  sqlc.yaml                 # pgx/v5 engine, emit_json_tags, emit_interface
  api/
    server.go               # Fiber app, sessions (gofiber/storage/postgres), CORS, slog middleware, route groups
    user.go                 # GetUsers, GetUser, Login, Logout, CreateUser, Me
    post.go                 # GetPosts, GetPost, Search, VotePost, CreatePost, UploadPost
    comment.go              # CreateComment, VoteComment
    tag.go                  # CreatePostTag, VotePostTag
  db/
    migrations/             # 001_users … 008_post_tag_votes (goose)
    queries/                # user.sql post.sql comment.sql tag.sql post_vote.sql comment_vote.sql post_tag.sql post_tag_vote.sql
    gen/                    # sqlc output — DO NOT EDIT
    store.go                # Store{*gen.Queries, *pgxpool.Pool} + ExecTx()
  utils/
    directories.go          # Directories struct + SetupDirectories()
    download.go             # DownloadFile with timeout/redirect limits
    image.go                # ProcessImage, ConvertImageToWebp, CropImage, SaveImage (CGO libwebp)
    video.go                # ProcessVideo, CreateVideoThumbnail (ffmpeg)
    validation.go           # IsEmailValid, CreatePasswordHash (bcrypt)
```

### Key implementation notes (pitfalls already hit)
- Sessions store `SessionUser{ID int32, Name string, Level int32}` via gob
- sqlc import alias: `dbgen "ginbar/db/gen"` (package name is `db`, not `gen`)
- Vote fields are `int16` (column name `vote`, not `upvoted`)
- `GetPossibleDuplicatePostsParams` fields are `Column1`–`Column4`
- `fiber.As` doesn't exist in v3 beta.3 — use `errors.As`
- `app.Static()` removed in v3 — use `static.New()` middleware from `middleware/static`
- `cors.Config.AllowOrigins` is `[]string` in v3 (not a string)
- `go get` for latest versions requires Go 1.24+; pinned: pgx@v5.6.0, fiber/v3@v3.0.0-beta.3, x/crypto@v0.25.0, x/image@v0.21.0
- Module cache permissions may need: `sudo chown -R vscode:vscode /home/vscode/go`
- Use `GOFLAGS=-mod=mod go build` when adding new deps without explicit `go get`

---

## Next: Chunk 6 — Frontend scaffold

```bash
cd /workspace/src/frontend
pnpm create vite@latest . -- --template react
# Accept overwrite prompts
pnpm add react-router-dom@7 zustand@5 axios
pnpm add -D @tailwindcss/vite tailwindcss
```

Then update `vite.config.js` with API proxy:
```js
server: { proxy: { '/api': 'http://localhost:3000' } }
```

After that: Chunk 7 (Zustand stores + api.js), Chunk 8 (UI components), Chunk 9 (Docker+nginx).

---

## Key env vars (dev)
```
DB_URL=postgres://ginbar:devpassword@localhost:5432/ginbar?sslmode=disable
SESSION_SECRET=change-me-in-prod
PORT=3000
```

## Useful make targets
```bash
make migrate-up          # run all pending goose migrations
make sqlc                # regenerate db/gen/ from queries
make dev-backend         # air hot-reload on :3000
make dev-frontend        # vite dev on :5173
make psql                # psql into postgres
PGPASSWORD=devpassword psql -h localhost -U ginbar ginbar
```
