# Ginbar

A custom-made imageboard. Go/Fiber backend, React frontend.

## Stack

| Layer    | Tech                                        |
| -------- | ------------------------------------------- |
| Backend  | Go · Fiber v3 · sqlc · goose                |
| Frontend | React 19 · Vite · Zustand · Tailwind CSS v4 |
| Data     | PostgreSQL 17 · Redis 7                     |
| Prod     | Docker Compose · host nginx                 |

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

### Architecture

On a real server, Docker runs only the three backend services.
The **host nginx** handles everything user-facing directly from the filesystem — no Docker networking overhead for static content.

```
HTTPS → host nginx ──┬── /images/, /videos/  →  MEDIA_DIR   (host filesystem)
                     ├── /assets/, /          →  FRONTEND_DIR (host filesystem)
                     └── /api/                →  127.0.0.1:3001 (backend container)

Docker: redis · postgres · backend (API only)
```

### First-time install

Run the interactive installer as root on the server:

```sh
sudo bash scripts/install.sh
```

The script will:

1. Check/install prerequisites (git, nginx, docker)
2. Set the install directory (default `/opt/ginbar`)
3. Configure the domain and Cloudflare Origin Certificate
4. Write `.env` with generated secrets and host paths
5. Install and enable the nginx vhost
6. Build Docker images and extract the Vite frontend to `FRONTEND_DIR`
7. Create media directories (`MEDIA_DIR/{images,videos,upload}`)
8. Run database migrations and start the stack
9. Install `ginbar.service` so the stack auto-starts on reboot

### Updating

```sh
sudo bash scripts/update.sh
```

Pulls latest code, rebuilds images, re-extracts the frontend, overwrites the nginx vhost and systemd service file, runs any new migrations, then restarts the service via systemctl.

### Resetting (wipe all data)

```sh
sudo bash scripts/clean.sh
```

**Destructive.** Drops the PostgreSQL volume, deletes all uploaded images and videos, and restarts the stack with a fresh migrated database. Requires typing `yes` to confirm.

### Environment variables

| Variable            | Required | Default                  | Description                                                                     |
| ------------------- | -------- | ------------------------ | ------------------------------------------------------------------------------- |
| `POSTGRES_PASSWORD` | yes      | —                        | PostgreSQL password                                                             |
| `SESSION_SECRET`    | yes      | —                        | Session signing key                                                             |
| `MEDIA_DIR`         | no       | `<install_dir>/media`    | Host path where backend writes uploaded files; host nginx reads from here       |
| `FRONTEND_DIR`      | no       | `<install_dir>/frontend` | Host path where the compiled Vite SPA is extracted; host nginx serves from here |
| `POSTGRES_DB`       | no       | `ginbar`                 | Database name                                                                   |
| `POSTGRES_USER`     | no       | `ginbar`                 | Database user                                                                   |

### Manual Docker commands

```sh
docker compose logs -f          # tail all logs
docker compose ps               # container status
docker compose run --rm migrate # run migrations manually
docker compose down             # stop stack
```
