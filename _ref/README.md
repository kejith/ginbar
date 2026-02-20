# Reference — Legacy Code

The legacy submodules (`backend/`, `frontend/`) have been removed from the repo.
They served as read-only reference during the rewrite and are no longer needed.

The full rewrite lives in `src/`:

- `src/backend/` — Go backend (Fiber v3, pgx/v5, sqlc, goose, PostgreSQL)
- `src/frontend/` — Vite 6 + React 19 frontend (Zustand, Tailwind v4, nginx)

The `_plan/PLAN.md` file documents the full 9-chunk rewrite plan.

- `../backend/mysql/query/` — all sqlc query definitions
- `../backend/models/` — JSON response structs
- `../backend/utils/` — image/video/download pipeline
- `../frontend/src/components/` — all UI components
- `../frontend/src/redux/slices/` — state shape
