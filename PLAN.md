# Rollarr Backend Rewrite — Go Media Buffer Orchestrator

## Context

The current TypeScript/Express backend has fundamental design issues (per user). This plan rewrites the backend in Go following the user's design document: a reverse-proxy orchestration layer sitting between Seerr and Sonarr, managing episode buffers dynamically based on Plex watch history.

**Key architectural shift**: Instead of Seerr sending webhooks and Rollarr calling Sonarr directly, Seerr is configured to point to Rollarr as its Sonarr instance. Rollarr proxies all Sonarr API calls, intercepting download requests to limit them to the computed buffer window.

---

## Tech Stack

- **Backend**: Go (single binary)
- **Frontend**: React TypeScript (embedded via `go:embed`)
- **Database**: SQLite via `modernc.org/sqlite` (pure Go, CGO-free)
- **HTTP routing**: `github.com/go-chi/chi/v5`
- **Scheduler**: `github.com/robfig/cron/v3`
- **Proxy**: `net/http/httputil.ReverseProxy`

---

## Database Schema (single `rollarr.db`)

```sql
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT
);
-- defaults: global_buffer_size=3, global_inactivity_days=30,
--           sonarr_url, sonarr_api_key, plex_url, plex_token,
--           plex_db_path, seerr_webhook_secret,
--           reconcile_interval_minutes=15, inactivity_interval_minutes=60

CREATE TABLE shows (
    tvdb_id                INTEGER PRIMARY KEY,
    sonarr_id              INTEGER UNIQUE NOT NULL,
    title                  TEXT NOT NULL,
    poster_url             TEXT,
    status                 TEXT NOT NULL DEFAULT 'active', -- active|inactive|removed
    custom_buffer_size     INTEGER,      -- NULL = use global
    custom_inactivity_days INTEGER,      -- NULL = use global
    last_activity_at       DATETIME,
    created_at             DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE user_requests (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    plex_user_id      TEXT NOT NULL,
    tvdb_id           INTEGER NOT NULL REFERENCES shows(tvdb_id) ON DELETE CASCADE,
    request_timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    is_rewatching     INTEGER NOT NULL DEFAULT 0,
    UNIQUE(plex_user_id, tvdb_id)
);

CREATE TABLE discrepancy_flags (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    tvdb_id           INTEGER NOT NULL,
    sonarr_episode_id INTEGER,
    issue_description TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'open', -- open|resolved|ignored
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

---

## Go Project Structure

```
cmd/rollarr/
    main.go                   # wire everything, start server

internal/
    config/
        config.go             # load env vars; seed settings DB on first boot

    db/
        db.go                 # open SQLite (WAL mode, FK enforcement)
        migrations.go         # sequential numbered migrations array

    db/repository/
        settings.go           # Get/Set/SetMany/GetInt
        shows.go              # CRUD + FindByStatus + EffectiveBuffer (coalesce)
        user_requests.go      # Upsert + FindByShow + SetRewatching
        discrepancy_flags.go  # Insert + FindOpen + UpdateStatus

    plex/
        client.go             # HTTP API: GetShowHistory, FindShowByTVDB, BuildUserMap
        plexdb.go             # Read-only SQLite connector: GetMarkedWatched (WAL)

    sonarr/
        client.go             # GetSeries, GetEpisodes, MonitorEpisodes,
                              # SearchEpisodes, DeleteEpisodeFile, GetQueue

    state/
        engine.go             # ComputeExpectedState(tvdbId) -> map[season][]int
                              # interval union + exceptions (S01E01, season premieres)

    proxy/
        proxy.go              # httputil.ReverseProxy wrapper
        interceptor.go        # intercept PUT /episode/monitor + POST /command

    reconcile/
        reconciler.go         # ReconcileShow(tvdbId): expected vs actual diff
                              # triggers Sonarr download/delete + writes flags

    webhook/
        seerr.go              # POST /webhooks/seerr + /webhooks/jellyseerr

    api/
        router.go             # chi router + Bearer auth middleware
        shows.go              # GET /api/shows, GET /api/shows/:id, POST reconcile
        requests.go           # DELETE /api/shows/:id/requests/:userId
        flags.go              # GET /api/flags, PUT /api/flags/:id
        settings.go           # GET/PUT /api/settings

    scheduler/
        scheduler.go          # cron: ReconcileAll + InactivityPrune jobs

frontend/                     # React TypeScript SPA (redesigned)
    dist/                     # built output (go:embed target)
```

---

## State Engine: `internal/state/engine.go`

**Function**: `ComputeExpectedState(tvdbId int) (map[int][]int, error)`

**Algorithm**:
1. Load all `user_requests` for show → `[]UserRequest{PlexUserID, RequestTimestamp, IsRewatching}`
2. Load effective buffer size from show (coalesce: custom → global)
3. For each user:
   - Fetch Plex watch history for show (HTTP API + optional SQLite)
   - If `is_rewatching`: filter to entries where `viewed_at > request_timestamp`
   - Build `map[season]int` of highest watched episode per season
   - For each season: compute interval `[watched+1, watched+bufferSize]`
4. **Interval Union** per season: merge all user intervals into minimal covering set
5. **Exceptions** (always append):
   - S01E01 always included
   - SXXE01 for every season that has any active request
6. Return `map[season][]episodeNumbers` (deduplicated, sorted)

**Types**:
```go
type ExpectedState map[int][]int  // season -> []episodeNumber

type Interval struct { Start, End int }

func unionIntervals(intervals []Interval) []Interval
```

---

## Proxy Layer: `internal/proxy/`

**Seerr is configured to use `http://rollarr:PORT` as its Sonarr URL.**

All requests to the proxy are forwarded to the real Sonarr, except:

### Intercepted Endpoints

| Endpoint | Intercept Reason |
|----------|-----------------|
| `PUT /api/v3/episode/monitor` | Rewrite: filter monitored episode IDs to buffer-only |
| `POST /api/v3/command` (EpisodeSearch) | Rewrite: filter episodeIds to buffer-only |

### Interception Flow (`PUT /api/v3/episode/monitor`)
1. Read + buffer request body
2. Parse `{ episodeIds: []int, monitored: bool }`
3. If `monitored = true`: call Sonarr to map episode IDs → `(seriesId, season, episodeNumber)`
4. Look up `tvdb_id` from `sonarr_id` in shows table
5. Call `state.ComputeExpectedState(tvdbId)` → expected episodes
6. Filter `episodeIds` to only those in expected state
7. Rewrite body and forward to real Sonarr

### Passthrough
All other endpoints proxied unmodified via `httputil.ReverseProxy`.

**Note**: Sonarr episode IDs are numeric and internal. The proxy maintains a short-lived in-memory cache mapping `sonarr_episode_id → {season, episode}` populated on demand from Sonarr's `GET /api/v3/episode?seriesId=X`.

---

## Reconciliation Engine: `internal/reconcile/reconciler.go`

**Function**: `ReconcileShow(tvdbId int) error`

1. `expected` ← `state.ComputeExpectedState(tvdbId)` → `map[season][]int`
2. `actual` ← Sonarr `GET /episode?seriesId={sonarrId}` filtered to episodes with files
3. Compute diff:
   - **Missing** (expected but not on disk): `monitorEpisodes(ids)` + `searchEpisodes(ids)`
   - **Orphaned** (on disk but not expected): `DELETE /episodefile/{fileId}` with `?unmonitor=true`
4. If Sonarr search produces no results after N attempts → insert `discrepancy_flags` row
5. Update `shows.last_activity_at` from latest Plex history timestamp

**Function**: `ReconcileAll() error` — iterates all active shows, calls ReconcileShow per show.

---

## Webhook Handler: `internal/webhook/seerr.go`

**Endpoints**: `POST /webhooks/seerr`, `POST /webhooks/jellyseerr`

1. Verify `X-Webhook-Secret` header (timing-safe compare)
2. Filter: only `MEDIA_APPROVED` / `MEDIA_AUTO_APPROVED` with `media_type=tv`
3. Extract: `tvdbId`, `plexUsername` (Overseerr flat or Jellyseerr nested), `requestedSeasons`
4. Resolve `plexUsername` → `plexUserId` via Plex API `GET /api/v2/users`
5. Upsert `shows` row (sonarr_id from `GET /api/v3/series?tvdbId=X`)
6. Check Plex history: if user previously finished show → set `is_rewatching=1`
7. Upsert `user_requests{plex_user_id, tvdb_id, request_timestamp, is_rewatching}`
8. Enqueue `ReconcileShow(tvdbId)` (serialized queue, returns 202 immediately)

---

## Admin REST API: `internal/api/`

**Auth**: Bearer token from `ROLLARR_API_TOKEN` env var (timing-safe, optional in dev)
**Token injection**: `window.__ROLLARR_TOKEN__` injected into `index.html` at boot

```
GET  /api/shows
     → []ShowSummary{tvdb_id, sonarr_id, title, status, effective_buffer_size,
                     active_request_count, last_activity_at}

GET  /api/shows/:tvdbId
     → ShowDetail{...ShowSummary, requests: []UserRequest,
                  expected_state: map[season][]int,
                  open_flags: []DiscrepancyFlag}

POST /api/shows/:tvdbId/reconcile
     → 202 {queued: true}

DELETE /api/shows/:tvdbId/requests/:plexUserId
     → 200 {ok: true}

GET  /api/flags
     → []DiscrepancyFlag (status=open)

PUT  /api/flags/:id
     body: {status: "resolved"|"ignored"}
     → updated DiscrepancyFlag

GET  /api/settings
     → SettingsMap (secrets redacted as "***SET***")

PUT  /api/settings
     body: SettingsMap (***SET*** sentinel preserves existing secret)
     → updated SettingsMap
```

---

## Scheduler: `internal/scheduler/scheduler.go`

Two cron jobs (intervals from settings, require restart to change):
- **Reconciliation**: every `reconcile_interval_minutes` (default 15) → `ReconcileAll()`
- **Inactivity pruning**: every `inactivity_interval_minutes` (default 60):
  - For each active show: check `last_activity_at`
  - If `now - last_activity_at > effective_inactivity_days` → mark inactive, delete all episode files, unmonitor all

Serialized via a channel-based job queue (one goroutine) to prevent concurrent Sonarr/Plex races.

---

## Inactivity & Pruning

**Coalesce pattern** (both buffer size and inactivity):
```go
func (r *ShowRepository) EffectiveBufferSize(tvdbId int) int {
    show := r.FindByTVDB(tvdbId)
    if show.CustomBufferSize != nil { return *show.CustomBufferSize }
    return r.settings.GetInt("global_buffer_size", 3)
}
```

---

## Frontend Redesign (React TypeScript)

**New pages/components**:
- `Dashboard.tsx` — ShowCard grid; shows `active_request_count`, `status`, `last_activity_at`
- `ShowDetail.tsx` — UserRequests list (plex_user_id, request_timestamp, is_rewatching badge), ExpectedState visualizer (interval bars per season), open DiscrepancyFlags panel
- `FlagsPage.tsx` — table of open flags with resolve/ignore actions
- `Settings.tsx` — same layout, updated field names (global_buffer_size, global_inactivity_days, sonarr_url, plex_url, proxy_port, etc.)

**Removed**: EpisodeRow table, TrackerRow, SeasonData, WindowProgress (replaced by interval union visualizer)

**API client types**:
```typescript
ShowSummary = { tvdb_id, sonarr_id, title, status, effective_buffer_size, active_request_count, last_activity_at }
ShowDetail   = ShowSummary & { requests: UserRequest[], expected_state: Record<number, number[]>, open_flags: Flag[] }
UserRequest  = { plex_user_id, request_timestamp, is_rewatching }
Flag         = { id, tvdb_id, sonarr_episode_id, issue_description, status, created_at }
SettingsMap  = Record<string, string>
```

---

## Single Binary Build Pipeline

```
# 1. Build frontend
cd frontend && npm run build   # outputs to frontend/dist/

# 2. Build Go binary (embeds frontend/dist)
go build -o rollarr ./cmd/rollarr/

# 3. Docker multi-stage
FROM golang:1.23-alpine AS builder
COPY . .
RUN cd frontend && npm run build
RUN go build -o /rollarr ./cmd/rollarr/

FROM gcr.io/distroless/static
COPY --from=builder /rollarr /rollarr
ENTRYPOINT ["/rollarr"]
```

`cmd/rollarr/main.go` embedding:
```go
//go:embed frontend/dist
var frontendFS embed.FS
```

SPA fallback: serve `index.html` for any unmatched non-`/api/` non-`/webhooks/` path.

---

## Implementation Phases

### Phase 1 — Go scaffold + DB layer
- `go.mod`, `go.sum` with deps
- `internal/db/db.go` (SQLite open, WAL, FK)
- `internal/db/migrations.go` (4-table schema)
- `internal/db/repository/` (all 4 repos)
- `internal/config/config.go`

### Phase 2 — External API clients
- `internal/sonarr/client.go` (GetSeries, GetEpisodes, Monitor, Search, DeleteFile, GetQueue)
- `internal/plex/client.go` (GetHistory, FindShow, BuildUserMap, GetSeasons)
- `internal/plex/plexdb.go` (read-only WAL SQLite, GetMarkedWatched)

### Phase 3 — State engine
- `internal/state/engine.go` (interval union + exceptions)
- Unit-testable pure functions

### Phase 4 — Proxy layer
- `internal/proxy/proxy.go` (httputil.ReverseProxy base)
- `internal/proxy/interceptor.go` (monitor + search intercept)
- Episode ID cache (in-memory, TTL 5min)

### Phase 5 — Webhook + reconciliation
- `internal/webhook/seerr.go`
- `internal/reconcile/reconciler.go`
- `internal/scheduler/scheduler.go` (job queue + cron)

### Phase 6 — Admin API
- `internal/api/router.go` + all handlers
- Token injection into index.html

### Phase 7 — Frontend redesign
- Update API client types
- Redesign ShowDetail, Dashboard, add FlagsPage
- Remove TrackerRow / EpisodeTable / WindowProgress

### Phase 8 — Build pipeline
- Dockerfile multi-stage
- docker-compose.yml update
- `go:embed` wiring in main.go

---

## Critical Files to Create/Modify

| File | Action | Notes |
|------|--------|-------|
| `go.mod` | Create | Module + deps |
| `cmd/rollarr/main.go` | Create | Wire all components |
| `internal/db/db.go` | Create | SQLite singleton |
| `internal/db/migrations.go` | Create | 4-table schema |
| `internal/db/repository/*.go` | Create | 4 repos |
| `internal/state/engine.go` | Create | Core algorithm |
| `internal/proxy/interceptor.go` | Create | Key intercept logic |
| `internal/reconcile/reconciler.go` | Create | Expected vs actual diff |
| `internal/webhook/seerr.go` | Create | Port from TS webhook |
| `internal/api/router.go` | Create | New API surface |
| `frontend/src/api/client.ts` | Modify | New types + endpoints |
| `frontend/src/pages/ShowDetail.tsx` | Modify | New data model |
| `frontend/src/pages/Dashboard.tsx` | Modify | Remove TrackerRow |
| `frontend/src/pages/FlagsPage.tsx` | Create | New flags UI |
| `docker-compose.yml` | Modify | Single container, new env vars |
| `.env.example` | Modify | New env var list |

**Delete**: entire `backend/` directory (TypeScript), `shared/` directory

---

## Verification

1. `docker compose up -d` → single container starts, serves frontend at port 3001
2. Configure Seerr to use `http://rollarr:3001` as Sonarr URL
3. Approve a show request in Seerr → webhook fires → user_request row created
4. Proxy intercepts Seerr's episode monitor call → only buffer episodes monitored in Sonarr
5. Reconciliation runs → verifies expected == actual, no discrepancy_flags created
6. Watch episodes in Plex → next reconciliation cycle advances buffer
7. `GET /api/flags` → empty (no unresolvable issues)
8. Settings page → save Sonarr/Plex config → settings persisted, secrets redacted on re-fetch
9. Wait past inactivity threshold → show transitions to inactive, files pruned
