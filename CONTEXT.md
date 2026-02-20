# Ginbar Rewrite — Session Context

> Full rewrite plan: `_plan/PLAN.md` · Legacy reference: `_ref/README.md`
> Read this first when starting a new conversation inside the devcontainer.

---

## Status: Chunks 0–7 complete — backend done, state layer done

### Chunk progress

| #   | Status | Notes                                                                                                                                                           |
| --- | ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0   | ✅     | Devcontainer, plan docs, repo scaffold                                                                                                                          |
| 1   | ✅     | 8 goose PG migrations — `make migrate-up` verified (version 8)                                                                                                  |
| 2   | ✅     | `go.mod` (Go 1.23, pgx/v5.6.0, fiber/v3-beta.3), sqlc.yaml, 8 query files, 11 generated files, `db/store.go`                                                    |
| 3   | ✅     | `main.go` (pgxpool, graceful shutdown), `api/server.go` (Fiber v3, sessions, CORS, slog middleware, all routes)                                                 |
| 4   | ✅     | `api/user.go`, `api/post.go`, `api/comment.go`, `api/tag.go` — all handlers implemented                                                                         |
| 5   | ✅     | `utils/` — download (30s timeout, max 5 redirects), image (cwebp+goimagehash+smartcrop), video (ffmpeg), validation (bcrypt); CreatePost/UploadPost fully wired |
| 6   | ✅     | Vite 6, React 19, SWC, Tailwind v4, RR v7, lazy routes, `/api` proxy                                                                                            |
| 7   | ✅     | `utils/api.js` (axios), `stores/authStore`, `postStore`, `commentStore`, `tagStore`                                                                             |
| 8   | ⏳     | **NEXT** — UI layout (full-width grid, sticky nav, Tailwind v4)                                                                                                |
| 9   | ⏳     | Production Docker Compose + nginx                                                                                                                               |

### Git state

- Branch: `rewrite` (on top of `master`)
- 8 commits: chunk 0 → chunk 7
- `go build ./...` → clean binary ✅
- `pnpm build` (frontend) → clean, 73 kB gzip main bundle ✅

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

## Frontend layout (scaffold done)

```
src/frontend/
  package.json          # react@19, react-dom@19, react-router-dom@7, zustand@5, axios
  vite.config.js        # SWC plugin, @tailwindcss/vite, /api proxy, es2022, manualChunks
  index.html            # minimal shell, preconnect hint
  pnpm-lock.yaml
  pnpm-workspace.yaml   # ignoredBuiltDependencies: @swc/core, esbuild
  public/
    favicon.svg         # brand orange SVG
  src/
    main.jsx            # createRoot + BrowserRouter
    App.jsx             # lazy Routes + hydrate() on mount
    index.css           # @import "tailwindcss"; dark tokens; full-bleed grid
    pages/
      Home.jsx          # stub → Chunk 8
      Post.jsx          # stub → Chunk 8
      Login.jsx         # stub → Chunk 8
      Profile.jsx       # stub → Chunk 8
      NotFound.jsx      # stub
    stores/
      authStore.js      # user {id,name,level}/null/false; hydrate,login,logout,register
      postStore.js      # fetchPosts(paginated), search, fetchPost, votePost (optimistic),
                        # createPost, uploadPost
      commentStore.js   # seed(postId,comments), createComment, voteComment (optimistic)
      tagStore.js       # seed(postId,tags), createTag, voteTag (optimistic)
    utils/
      api.js            # axios {baseURL:/api, withCredentials:true} + error interceptor
```

Build stats: 860 ms, main bundle 73 kB gzip (stores + axios included).

### Key notes (pitfalls already hit)

- `pnpm install` requires `sudo chown -R $(whoami) node_modules` first (root-owned from earlier)
- `@swc/core` and `esbuild` build scripts are ignored by pnpm — native bins (`@swc/core-linux-x64-gnu`, `esbuild-linux-x64`) are downloaded directly as separate packages; build works fine
- React 19 ESM means `react-vendor` manualChunk comes out empty — omit it
- Tailwind v4: CSS-first, no `tailwind.config.js`; just `@import "tailwindcss"` in the CSS entry

## Next: Chunk 8 — UI layout

Implement real components in `src/frontend/src/`:

- `components/Nav.jsx` — sticky top bar: logo, search input (→ `/search?q=`), login link or username
- `components/PostCard.jsx` — image/video thumb, score, upvote/downvote buttons, tag chips, link to post page
- `components/VoteButtons.jsx` — ±1 / 0 vote UI, uses store action passed as prop
- `components/TagChip.jsx` — clickable tag (navigates to `/?tag=name`), vote badge
- `components/CommentItem.jsx` — user, timestamp, content, vote buttons
- `components/CommentForm.jsx` — textarea + submit; uses `useAuthStore` to gate
- Pages wired:
  - `Home.jsx` → `usePostStore.fetchPosts`, infinite scroll or load-more button, grid of `PostCard`
  - `Post.jsx` → `usePostStore.fetchPost`, seed comment/tag stores, full-res media, `CommentSection`
  - `Login.jsx` → form wired to `useAuthStore.login`; redirect on success

Layout: full-width CSS grid (no max-width cap on the board), sticky nav 48 px tall, dark theme tokens already in index.css.

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
