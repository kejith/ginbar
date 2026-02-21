# Ginbar

A custom-made imageboard. Go/Fiber backend, React frontend.

## Stack

| Layer | Tech |
|-------|------|
| Backend | Go · Fiber v3 · sqlc · goose |
| Frontend | React 19 · Vite · Zustand · Tailwind CSS v4 |
| Data | PostgreSQL 17 · Redis 7 |
| Prod | Docker Compose · nginx |

## Development

Requires: Go, Node, pnpm, air, goose, PostgreSQL & Redis running locally.

```sh
make dev          # migrate + backend (air) + frontend (vite)
make dev-backend  # backend only
make dev-frontend # frontend only
```

Default DB URL: `postgres://ginbar:devpassword@localhost:5432/ginbar`

### Database

```sh
make migrate-up     # apply migrations
make migrate-down   # roll back one
make migrate-status # show migration state
make sqlc           # regenerate query code
make psql           # open psql shell
```

## Production

Copy `.env.example` to `.env` and set the required secrets, then:

```sh
make build        # build Docker images
make up           # docker compose up -d
make migrate-prod # run goose inside container (first run)
make logs         # tail logs
make down         # stop stack
```

Required env vars: `POSTGRES_PASSWORD`, `SESSION_SECRET`.
