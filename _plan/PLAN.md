# Ginbar — Rewrite Plan (AI Reference)
## Decisions
| Layer | Tech |
|---|---|
| Backend | Go 1.23, Fiber v3, pgx/v5, sqlc 2 |
| Database | PostgreSQL 17 |
| Sessions | Fiber v3 session + gofiber/storage/postgres |
| Image proc | libwebp (CGO), goimagehash, ffmpeg subprocess |
| Frontend | React 19, Vite 6, Zustand 5, Tailwind CSS v4, React Router v7 |
| Deployment | Docker Compose + Nginx reverse proxy |
| Code layout | `src/backend/`, `src/frontend/` — old submodules in `backend/`+`frontend/` for ref |

---
## Chunks
| # | Scope | Key actions |
|---|---|---|
| 0 | Dev env | DONE — devcontainer, plan docs |
| 1 | PG Schema | Convert 8 MySQL schema files → PG DDL; write goose migrations in `src/backend/db/migrations/` |
| 2 | DB layer | sqlc.yaml for pgx/v5; regenerate queries; replace `mysql/db/` with `db/`; new store.go with pgx.Pool |
| 3 | go.mod + Fiber v3 | New go.mod Go 1.23; Fiber v3 imports; slog; graceful shutdown; PG session storage |
| 4 | Handlers | Port post/user/comment/tag handlers to Fiber v3; typed error responses; remove models/ abstraction |
| 5 | Utils | timeout on download.go; keep image/video pipeline; remove dead cache/ pkg |
| 6 | Vite scaffold | Delete CRA, new vite.config.js, package.json (React 19, Zustand, Tailwind v4, RR v7) |
| 7 | State (Zustand) | Replace redux/ with stores/; thin api.js (axios); auth, posts, comments, vote stores |
| 8 | UI layout | Full-width CSS Grid board; no side margins; sticky nav; Tailwind throughout |
| 9 | Deployment | Dockerfile (Go 1.23 + node:22-alpine); nginx.conf; docker-compose.yml (PG + nginx) |

---
## Repo Layout (target)
```
src/
  backend/
    main.go
    api/           # Fiber v3 handlers
    db/
      migrations/  # goose .sql files
      queries/     # .sql for sqlc
      gen/         # sqlc output (pgx/v5)
    utils/
    go.mod
    Makefile       # make dev|migrate|sqlc|lint
  frontend/
    src/
      components/
      stores/      # zustand
      utils/
    vite.config.js
    package.json
nginx/
  nginx.conf
docker-compose.yml
Dockerfile
.env.sample
backend/           # REF: old submodule (read-only reference)
frontend/          # REF: old submodule (read-only reference)
```

---
## API Routes (unchanged logic, new implementation)
```
GET  /api/check/me
GET  /api/user/:id          GetUser
GET  /api/user/*            GetUsers
POST /api/user/login        Login
POST /api/user/logout       Logout
POST /api/user/create       CreateUser

GET  /api/post/*            GetPosts  (query: page, limit, tag)
GET  /api/post/search/:q    Search    (space-separated tags via %20)
GET  /api/post/:id          GetPost
POST /api/post/vote         VotePost  {post_id, vote_state}
POST /api/post/create       CreatePost (URL-based)
POST /api/post/upload       UploadPost (multipart)

POST /api/comment/create    CreateComment {content, post_id}
POST /api/comment/vote      VoteComment  {comment_id, vote_state}

POST /api/tag/create        CreatePostTag {name, post_id}
POST /api/tag/vote          VotePostTag   {post_tag_id, vote_state}
```

---
## Data Models (DB — PG types)
```sql
users(id SERIAL PK, name VARCHAR UNIQUE, email VARCHAR UNIQUE,
      password VARCHAR, level INT DEFAULT 1, created_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ)

posts(id SERIAL PK, url TEXT, uploaded_filename TEXT, filename VARCHAR,
      thumbnail_filename VARCHAR, content_type VARCHAR, score INT DEFAULT 0,
      user_level INT DEFAULT 0, p_hash_0..3 BIGINT, user_name VARCHAR FK→users,
      created_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ)

comments(id SERIAL PK, content TEXT, score INT DEFAULT 0,
         user_name VARCHAR FK→users, post_id INT FK→posts,
         created_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ)

tags(id SERIAL PK, name VARCHAR(32) UNIQUE, user_level INT DEFAULT 0)

post_tags(id SERIAL PK, post_id INT FK→posts, tag_id INT FK→tags,
          user_id INT FK→users, score INT DEFAULT 0)

post_votes(post_id INT FK→posts, user_id INT FK→users, vote INT, PRIMARY KEY(post_id,user_id))
comment_votes(comment_id INT FK→comments, user_id INT FK→users, vote INT, PK(...))
post_tag_votes(post_tag_id INT FK→post_tags, user_id INT FK→users, vote INT, PK(...))
```

---
## Notes
- `GetVotedPost` / `GetVotedComments` use LEFT JOIN on vote tables — keep this pattern in PG queries
- Session stores `{ID int32, Name string, Level int32}` — keep same shape
- Images stored at `./public/images/`, thumbnails in `./public/images/thumbnails/`
- p_hash_0..3 = 4×uint64 perceptual hash (goimagehash) — BIGINT in PG (signed, cast safely)
- Search: currently splits on `%20` space → tag name array → SQL `WHERE tag.name IN (...)` — can upgrade to PG `pg_trgm` later but keep simple first
- Vote state: -1 / 0 / 1
- User level 0=anonymous 1=user (higher=mod/admin, implied)
