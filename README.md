# Rollarr


**JIT episode buffer manager for Plex + Sonarr.**

Rollarr tracks what each user watches via Plex history, then tells Sonarr to keep only a sliding window of episodes downloaded ahead of their progress — deleting behind and downloading ahead as they watch. Instead of hoarding entire series on disk, you keep a few episodes per active viewer and let Rollarr reclaim the rest.

**AI DISCLOSURE**
This application was developed with heavy AI input. 
It has seen intensive testing by myself, but is very much in Alpha


## Why

A 10-season show is hundreds of GB. Most of it sits unwatched. Rollarr keeps only a <buffer_size> window of episodes ahead of where each user actually is, so disk usage tracks *active viewing* rather than *full catalog*. New episodes download just in time; watched episodes get pruned automatically.

## Features

- **Multi-user tracking** — every Plex viewer of a show is tracked independently. Each user gets their own linear position from their own watch history, and the expected state is the *union* of every user's window, so a show stays buffered correctly even when several people are at different points. Anchors (S01E01 while any watcher exists, plus each request's requested-season E01) are always kept.
- **Manual adding** — a built-in Plex library browser (`GET /api/plex/library`) cross-references every Plex show against Sonarr and Rollarr's tracking state. Add a show via `POST /api/shows` without waiting for a Seerr request. You can either let Rollarr auto-discover watchers from Plex history, or override with an explicit user + starting season.
- **Window & savings preview** — before adding, `GET /api/plex/library/{tvdbId}/preview` shows the exact window that would be kept, who the watchers are, and how many files / how much disk the first reconcile would reclaim — rendered inline per library show.
- **Rewatch detection** — when a user requests (or is manually added at) a season at or below their highest previously-watched season, Rollarr treats it as a rewatch: the stored forward-only progress floor is cleared and old history before the rewatch timestamp is ignored, so the window resets to the rewatch start instead of pinning at the old finished position. Same semantics whether triggered by the Seerr webhook or a manual add.
- **Auto-discovered watchers** — reconcile can pick up viewers found in Plex history with no Seerr request.
- **"Mark as Watched" detection** — with an optional read-only mount of the Plex SQLite DB (`PLEX_DB_PATH`), Rollarr also catches episodes marked watched without a play event, merged with HTTP history and the stored floor. Handles Plex cloud↔local account ID translation.
- **Instant reaction** — a Plex `media.scrobble` webhook reconciles immediately when an episode finishes, instead of waiting for the next scheduled pass.
- **Savings counter & audit trail** — Track how much data you've removed
- **Inactivity pruning** — shows with no recent watch activity or requests get fully cleaned up and unmonitored.
- **Fail-safe reconcile** — Sonarr/Plex errors abort before any delete; a show with zero requests is never reconciled.

## How it works

```
Seerr request ──► Rollarr webhook ──► (proxy) ──► Sonarr
                       │
Plex history ──────────┤  computes per-user position → window [pos-safetyBehind+1, pos+bufferSize]
                       │
                       └──► Reconciler ──► Sonarr: delete behind / monitor + search ahead
```

1. **Onboarding** — Seerr (Overseerr/Jellyseerr) sends a webhook when a show is requested. Rollarr sits as a reverse proxy between Seerr and Sonarr, stripping full-season searches down to the window so Sonarr only grabs what's needed.
2. **State engine** — builds a *linear* episode layout (windows cross season boundaries; episodes are one continuous sequence), fetches per-user Plex history, reduces each user to one position, and computes the window.
3. **Reconciler** — compares expected vs. actual: deletes orphaned files, unmonitors out-of-window episodes, monitors + searches what's missing. Fails safe — Sonarr/Plex errors abort before any delete.
4. **Pruning** — shows with no recent activity get fully cleaned up and unmonitored.
5. **Instant reaction** — a Plex `media.scrobble` webhook triggers an immediate reconcile when someone finishes an episode.

Every delete advances a savings counter and writes an audit row explaining why, exposed at `GET /api/stats`.

## Stack

- **Backend** — Go (`cmd/rollarr`, `internal/`), SQLite, serial job queue so all Sonarr/Plex work is race-free.
- **Frontend** — React + Vite, embedded into the Go binary.
- **Shared types** — TypeScript types in `shared/` (frontend only).

## Quick start

### Docker (recommended)

Image is published to `ghcr.io/kiwi3007/rollarr` by CI.

```bash
cp .env.example .env   # fill in Sonarr/Plex/Seerr values
docker compose up -d
```

See `docker-compose.yml` for volume mounts (Plex DB, SQLite data dir).

### Local build

```bash
./build.sh                      # frontend + linux/amd64 binary → ./rollarr
# or, backend only:
go build ./cmd/rollarr/
go test ./...
npm run dev                     # frontend dev server (Vite)
```

## Configuration

Env vars seed the `settings` table on **first boot only** — after that the UI is the source of truth. (Scheduler intervals are read at startup; changing them needs a restart.)

| Var | Purpose |
|-----|---------|
| `ROLLARR_API_TOKEN` | Bearer token for `/api/*` (empty = no auth, dev only) |
| `SONARR_URL` / `SONARR_API_KEY` | Sonarr v3 connection |
| `PLEX_URL` / `PLEX_TOKEN` | Plex connection |
| `PLEX_DB_PATH` | Optional path to Plex SQLite DB — enables "Mark as Watched" detection |
| `SEERR_WEBHOOK_SECRET` | Webhook secret from Seerr/Jellyseerr (empty = skip verify) |
| `PORT` | Server port (default `3001`) |
| `DB_PATH` | SQLite path (default `rollarr.db`) |

## Endpoints

**Admin API** (`/api/*`, Bearer auth):

| Method | Path | |
|--------|------|---|
| GET | `/api/shows`, `/api/shows/{tvdbId}` | tracked shows |
| POST | `/api/shows/{tvdbId}/reconcile` | force reconcile |
| GET/POST | `/api/shows` | list / add manually from Plex library |
| GET | `/api/plex/library`, `/api/plex/library/{tvdbId}/preview`, `/api/plex/users` | Plex browse + window preview |
| GET | `/api/stats` | savings counters |
| GET | `/api/flags` | open discrepancy flags |
| GET | `/api/logs?tail=N` | live logs |
| GET | `/api/events` | SSE stream |
| GET | `/api/settings` | runtime config |

**Webhooks** (`X-Webhook-Secret` header, or `?secret=` for Plex):

- `POST /webhooks/seerr`, `POST /webhooks/jellyseerr` — show onboarding
- `POST /webhooks/plex` — `media.scrobble` → instant reconcile

**Proxy** — `/api/v3/*` and `/sonarr-proxy/*` forward to Sonarr (Sonarr's own API key applies). Point Seerr's Sonarr server at Rollarr to scope requests to the window.

## Deletions

Rollarr only works on shows requested after it's installed, or that you manually add. So you don't have to worry about it affecting all your shows on first run.

```
