# Ginbar Rewrite — Session Context
> Full rewrite plan: `_plan/PLAN.md` · Legacy reference: `_ref/README.md`
> Read this first when starting a new conversation inside the devcontainer.

---

## Status: Chunk 0 complete — devcontainer builds (last fix applied, not yet verified)

### What exists
```
.devcontainer/
  Dockerfile          # Go 1.23-bookworm base, system apt tools, sqlc, migrate, air, golangci-lint, goose
  docker-compose.yml  # services: app + postgres:17-alpine + pgadmin4
  devcontainer.json   # features: node:22 + pnpm; all extensions; port forwards 3000/5173/5432/5050
  postCreate.sh       # installs gopls, goimports, pgcli; runs go mod download + pnpm install if present
  postStart.sh        # prints connection info
_plan/PLAN.md         # full rewrite plan (tech decisions, all chunks, API routes, DB schema)
_ref/README.md        # map to legacy submodule files worth consulting
src/backend/.keep     # empty scaffold — rewrite target
src/frontend/.keep    # empty scaffold — rewrite target
Makefile              # make dev-backend/frontend, migrate-up/down, sqlc, lint-backend, psql
.editorconfig
```

### Devcontainer build errors encountered & fixes applied

**Error 1** — `npm: not found` (exit 127)
- Cause: base image ships Debian Node 18 via apt; nodesource setup_22 script replaced it but npm wasn't on PATH during `RUN`
- Fix: removed Node install from Dockerfile entirely; added devcontainer `features` block in `devcontainer.json`:
  ```json
  "features": {
    "ghcr.io/devcontainers/features/node:1": { "version": "22" },
    "ghcr.io/devcontainers-contrib/features/pnpm:2": {}
  }
  ```

**Error 2** — `apt-get update` exits 100, `NO_PUBKEY 62D54FD4003F6525`
- Cause: base image `mcr.microsoft.com/devcontainers/go:1.23-bookworm` has `/etc/apt/sources.list.d/yarn.list` (dl.yarnpkg.com) baked in with an expired GPG key
- Fix: prepended `rm -f /etc/apt/sources.list.d/yarn.list` before `apt-get update` in Dockerfile

### Next steps inside the container

Once the container is running, work through the chunks in order:

**Chunk 1 — PostgreSQL schema** (`src/backend/db/migrations/`)
- Write 8 goose migration files converting MySQL DDL → PostgreSQL
- Types: `INT UNSIGNED AUTO_INCREMENT` → `SERIAL`, `DATETIME` → `TIMESTAMPTZ`, strip `ENGINE=InnoDB`
- Reference: `backend/mysql/schema/001_user.sql` … `008_post_tag_votes.sql`
- Run: `make migrate-up`

**Chunk 2 — DB layer** (`src/backend/`)
- `go mod init ginbar` with Go 1.23
- Add `github.com/jackc/pgx/v5`, `github.com/gofiber/fiber/v3`, `golang.org/x/crypto`
- Write `sqlc.yaml` targeting pgx/v5 engine
- Write SQL queries (reference: `backend/mysql/query/*.sql`)
- Run: `make sqlc` → generates `src/backend/db/gen/`
- Write `db/store.go` with `pgx.Pool`-backed store

**Chunk 3 — Fiber v3 server + main.go**
- `src/backend/main.go`: env vars, pgx pool, graceful shutdown via `os.Signal`
- `src/backend/api/server.go`: Fiber v3, slog logger middleware, error handler, session via `gofiber/storage/postgres`, CORS, static serve

**Chunk 4 — Route handlers** (`src/backend/api/`)
- `post.go`, `user.go`, `comment.go`, `tag.go`
- Port from `backend/fiberapi/` — same logic, Fiber v3 API (`fiber.Ctx` unchanged mostly)
- Return sqlc structs directly with json tags; remove `models/` abstraction layer

**Chunk 5 — Utils** (`src/backend/utils/`)
- Copy `backend/utils/image.go`, `video.go`, `download.go`, `directories.go`
- Add timeout + max-redirects to `download.go`
- Drop `utils/cache/` (was commented out anyway)

**Chunk 6 — Frontend scaffold** (`src/frontend/`)
- `pnpm create vite@latest . -- --template react`
- Update `package.json`: React 19, React Router v7, Zustand 5, Tailwind CSS v4, axios
- `vite.config.js`: proxy `/api` → `http://localhost:3000`

**Chunk 7 — State: Redux → Zustand** (`src/frontend/src/stores/`)
- `authStore.js`, `postsStore.js`, `commentsStore.js`, `voteStore.js`
- `utils/api.js`: axios instance with baseURL + credentials
- Delete `src/frontend/src/redux/`

**Chunk 8 — UI layout** (full-width imageboard)
- Full-width CSS Grid board, no side margins ever
- Tailwind v4 throughout, delete legacy `.css` files
- Sticky nav, responsive PostView, touch-friendly votes

**Chunk 9 — Production Docker Compose + Nginx**
- `Dockerfile` (root): Go 1.23 builder + node:22-alpine + debian slim final
- `nginx/nginx.conf`: proxy `/api/` → backend, serve `dist/` as static
- `docker-compose.yml`: postgres:17-alpine, backend, nginx (no exposed 3000)
- `.env.sample` with PG vars

---

## Key env vars (dev)
```
DB_URL=postgres://ginbar:devpassword@localhost:5432/ginbar?sslmode=disable
SESSION_SECRET=change-me-in-prod
PORT=3000
```

## Useful commands inside container
```bash
make migrate-up          # run all pending goose migrations
make sqlc                # regenerate db/gen/ from queries
make dev-backend         # air hot-reload on :3000
make dev-frontend        # vite dev on :5173
make psql                # connect to postgres
PGPASSWORD=devpassword psql -h localhost -U ginbar ginbar
```
