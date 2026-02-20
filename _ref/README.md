# Reference — Legacy Code
The old implementation lives in the git submodules at the repo root:
- `../backend/` — Go backend (Gin + Fiber v2, MySQL, Go 1.15)
- `../frontend/` — React frontend (CRA, Redux, plain CSS)

These are **read-only reference**. The rewrite lives in `../src/`.

Key reference files to consult:
- `../backend/fiberapi/` — all route handlers
- `../backend/mysql/schema/` — original DB schema (8 files)
- `../backend/mysql/query/` — all sqlc query definitions
- `../backend/models/` — JSON response structs
- `../backend/utils/` — image/video/download pipeline
- `../frontend/src/components/` — all UI components
- `../frontend/src/redux/slices/` — state shape
