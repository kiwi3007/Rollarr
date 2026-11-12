# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project does

Rollarr is a JIT episode buffer manager for Plex + Sonarr. It tracks what each user has watched via Plex history, then tells Sonarr to keep only a sliding window of `buffer_size` episodes downloaded ahead of their progress — deleting behind and downloading ahead as they watch. Windows cross season boundaries (episodes are modelled as one linear sequence). Seerr (Overseerr/Jellyseerr) webhooks trigger new show onboarding, and Rollarr sits as a reverse proxy between Seerr and Sonarr to strip full-season searches down to the window.

## Commands

```bash
# Full production build (frontend + linux/amd64 binary)
./build.sh

# Go backend only
go build ./cmd/rollarr/
go test ./...
go vet ./...

# Frontend dev server (Vite)
npm run dev

# Frontend production build
npm run build
```

Deploy: push to GitHub → CI builds `ghcr.io/kiwi3007/rollarr` → pulled by the Arr-stack compose (separate `docker_compose` repo). Live logs: `GET http://<host>:3001/api/logs?tail=200`.

`_legacy/` holds the retired TypeScript backend — reference only, never edit.

## Architecture

Go backend (`cmd/rollarr`, `internal/`), React+Vite frontend (`frontend/`, embedded into the binary via `assets.go`). Frontend API types live in `frontend/src/api/client.ts`.

```
cmd/rollarr/main.go    — wiring: DB, repos, clients, engine, reconciler, scheduler, proxy, router
internal/
  config/              — env var config (seeds settings table on first boot only)
  db/                  — sql.DB open + sequential migrations (schema_version in settings table)
  db/repository/       — shows, user_requests, discrepancy_flags, settings
  plex/                — Plex HTTP API client + optional read-only Plex SQLite DB (plexdb.go,
                         "Mark as Watched" detection; cloud↔local account ID translation)
  sonarr/              — Sonarr v3 REST client
  state/               — Engine: computes ExpectedState (season → episodes that should exist)
  reconcile/           — Reconciler: expected vs actual, drives Sonarr delete/monitor/search
  scheduler/           — cron + serial JobQueue (ALL jobs run through it; prevents races)
  proxy/               — reverse proxy to Sonarr; intercepts series-add, episode/monitor,
                         command (search) to scope Seerr's requests to the window
  webhook/             — POST /webhooks/seerr, /webhooks/jellyseerr (X-Webhook-Secret) and
                         /webhooks/plex (media.scrobble → instant reconcile; ?secret= query param)
  api/                 — admin REST API (Bearer auth) + SSE + embedded SPA
```

### Core flow

1. **Seerr request** → webhook upserts show + user_request (`requested_season`, rewatch detection) → Seerr POSTs the series to Sonarr *through the proxy*, which disables search-on-add and fires `OnSeriesAdd` (retries until Sonarr indexes, then reconciles).
2. **state.Engine.Compute(tvdbId)** — builds a *linear* episode layout from Sonarr, fetches per-user Plex history (HTTP + plexdb, merged with the stored `last_watched_*` floor), reduces each user to one linear position, window = `[pos-safetyBehind+1, pos+bufferSize]` in linear order. No history → seed at requested season E01. No episodes ahead (finished) → no window ahead (revives when new episodes appear); the finale itself is kept for `finaleGrace` (7 days) after its Plex watch time, or indefinitely if the time is unknown, until the inactivity prune. Anchors: S01E01 while any watcher exists + each request's requested-season E01.
3. **reconcile.Reconciler.ReconcileShow** — expected vs actual: deletes orphaned files, *then* unmonitors out-of-window (Sonarr re-monitors on file delete, so order matters), monitors expected, searches missing (skipping queued + exponential search backoff). Refuses to act when a show has zero user_requests (empty expected would wipe everything).
4. **PruneInactive** — inactivity = max(last watch event incl. plexdb, newest request_timestamp, air date of the next aired episode after each watcher's progress) older than threshold → delete all files + unmonitor, mark inactive *last* (so failures retry). If a watcher had caught up to the last aired episode, a revive marker is stored; `ReviveInactive` (run at the start of each prune pass) reactivates the show once an episode after the marker airs and restarts the inactivity clock.

### Key invariants

- `requested_season = 0` marks an **auto-discovered watcher** (found via Plex history, no Seerr request); the engine skips the request-timestamp history filter for them. `>= 1` is Seerr-initiated and history before `request_timestamp` is ignored.
- `shows.managed = 0` (Go: `Show.Unmanaged`) marks a show requested only by users in `excluded_user_ids`: reconcile, prune and the proxy leave it untouched. Decided at onboarding by `repository.ShouldManage`; only a non-excluded Seerr request promotes it, nothing demotes it automatically. `shows.Upsert` never writes `managed` on conflict.
- Stored watch progress (`last_watched_season/episode`) is a forward-only floor; it is cleared when a rewatch starts (webhook or PATCH request API).
- Unknown state must fail safe: Sonarr/Plex errors abort the reconcile before any delete; they never produce an empty expected state. `PruneInactive` skips the whole pass when Plex doesn't answer (unknown activity ≠ no activity).
- Rollarr's scope is regular episodes: specials (season 0) are never deleted, monitored or unmonitored. Sonarr deletes per *file*; a multi-episode file is only deleted when none of its episodes is expected (`reconcile/plan.go`).
- `seerr:<username>` placeholder requests are converted to numeric rows only when that username resolves (`resolveSeerrRows`); never delete them wholesale.
- The Plex token goes in the `X-Plex-Token` header, never the URL — Go transport errors include the URL and logs are served at `/api/logs`.
- Everything that touches Sonarr/Plex runs through the serial `JobQueue`.
- Every file delete goes through `Reconciler.deleteFileTracked`: it advances the savings counters (`stat_bytes_deleted`/`stat_files_deleted` in settings, exposed at `GET /api/stats`) and writes an `events` audit row explaining why. Don't call `sonarr.DeleteEpisodeFile` directly from reconcile paths.
- The state engine depends on `PlexClient`/`PlexDBReader`/`SonarrClient` interfaces — scenario tests in `internal/state/engine_scenarios_test.go` use fakes + in-memory SQLite. New window-logic changes need a scenario test.

### Auth

- `/api/*` — Bearer token, timing-safe compare. The SPA is public and never carries the token: on a 401 it shows a sign-in prompt and keeps the token in localStorage (`frontend/src/auth.ts`). SSE passes it as `?token=`.
- `/webhooks/*` — `X-Webhook-Secret` header.
- `/api/v3/*` and `/sonarr-proxy/*` — forwarded to Sonarr (Sonarr's own API key auth applies).

### Settings

`settings` SQLite table; env vars seed empty values on first boot only, the UI is source of truth thereafter. Scheduler intervals are read at startup — changing them requires a restart.
