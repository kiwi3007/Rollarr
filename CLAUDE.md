# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project does

Rollarr is a JIT episode buffer manager for Plex + Sonarr. It tracks what each user has watched via Plex history, then tells Sonarr to keep only a sliding window of `buffer_size` episodes downloaded ahead of their progress — deleting behind and downloading ahead as they watch. Seerr (Overseerr/Jellyseerr) webhooks trigger new show onboarding.

## Commands

```bash
# Dev (both backend + frontend with hot reload)
npm run dev

# Backend only
npm run dev -w backend

# Frontend only (Vite, proxies /api and /webhooks to :3001)
npm run dev -w frontend

# Production build
npm run build

# Type-check without building
cd backend && npx tsc --noEmit
cd frontend && npx tsc --noEmit
```

**No test suite exists yet.**

### Backend .env setup

Copy `.env.example` to `backend/.env`. `ROLLARR_API_TOKEN` gates all `/api/*` endpoints — omit in dev, required in prod. The Sonarr/Plex/Seerr env vars seed the settings table on first boot only; the UI is source of truth thereafter.

### Docker

```bash
docker compose up -d
```

Plex DB mount (optional, enables "Mark as Watched" detection) is commented out in `docker-compose.yml`.

## Architecture

### Monorepo structure

- `shared/` — TypeScript types shared between backend and frontend (`ShowRow`, `TrackerRow`, `EpisodeStatus`, Sonarr/Plex API shapes, etc.)
- `backend/` — Express API + SQLite + cron jobs
- `frontend/` — React + Vite + Tailwind SPA

### Backend layers

```
index.ts           — Express setup, runs migrations, starts scheduler
config.ts          — Single config object from env vars
db/
  database.ts      — better-sqlite3 singleton (WAL mode, FK enforcement)
  migrations.ts    — sequential numbered migrations, schema_version in settings table
  repositories/    — one file per table, synchronous SQLite prepared statements
services/
  sonarrService.ts — Sonarr v3 REST client
  plexService.ts   — Plex HTTP API + optional SQLite DB reads (for "Mark as Watched")
  rollingWindowService.ts — core business logic (see below)
jobs/
  scheduler.ts     — node-cron, snaps poll intervals to valid cron strides
  discoveryJob.ts  — syncs Sonarr shows/episodes into DB
  progressJob.ts   — calls advanceWindow for every active show
  maintenanceJob.ts — marks shows Stale/Removed, handles inactivity
webhooks/
  seerrWebhook.ts  — POST /webhooks/seerr and /webhooks/jellyseerr
api/
  router.ts        — Bearer auth middleware, mounts sub-routers
  showsRouter.ts / settingsRouter.ts / usersRouter.ts / trackersRouter.ts
utils/
  asyncQueue.ts    — serial job queue (prevents concurrent advanceWindow races)
  logger.ts        — structured logger with module labels
  retry.ts         — exponential backoff helper
```

### Core business logic: `rollingWindowService.advanceWindow`

Four phases run per-show on each poll:

1. **Plex history fetch** — merges show-scoped history, global history (key/title match), and optional SQLite DB reads into `historyBySeason: Map<accountId, Map<season, highestEp>>`
2. **Tracker progress update** — resolves synthetic `seerr:username` account IDs, updates each tracker's `last_watched_season/episode`
3. **Sonarr ops** — deletes files behind the window, monitors + searches episodes ahead; guarded by a DB pre-check to skip Sonarr calls when nothing changed
4. **DB commit** — single `better-sqlite3` transaction updates episode statuses and `current_window_start`

All jobs run through `asyncQueue` to serialise against webhook-triggered runs.

### Auth flow

- `/api/*` — Bearer token (`ROLLARR_API_TOKEN`), checked in `api/router.ts` with `timingSafeEqual`
- `/webhooks/*` — separate router, uses `X-Webhook-Secret` header with `timingSafeEqual`
- Frontend receives the token injected into `index.html` at boot via `window.__ROLLARR_TOKEN__`

### Settings

Stored in the `settings` SQLite table. Env vars seed empty values on first boot only (`seedFromEnv` in migrations.ts). `settingsRepository` provides typed helpers including `getNumber`. Scheduler reads poll/maintenance intervals at startup — changing them requires a restart.

### DB migrations

`migrations.ts` maintains an array of sequential migration functions. Version tracked as `schema_version` in the settings table. New migrations: append to the array. Migration 0 seeds default settings.

## Known deferred issues

See `LATER_FIXES.md` for a post-review list. Highlights:
- `trackersRouter.ts` is dead code (never imported)
- `isDryMode()` is duplicated in `rollingWindowService.ts` and `discoveryJob.ts`
- `frontend/src/api/client.ts` redefines shared types instead of importing from `@rollarr/shared`
- Migration `ALTER TABLE` statements are not idempotent on their own (version row is the only guard)
