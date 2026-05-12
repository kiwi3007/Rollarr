# Deferred review items

Lower-priority cleanups identified in the 2026-04-28 code review.
Critical and high-severity issues fixed in the same session.
Items marked **✓ FIXED** were resolved in the 2026-05-06 refactor.

## Robustness

- **Better-sqlite3 connection contention.** `scripts/backfillImages.ts` opens its own DB connection and calls `runMigrations()` from a separate process. WAL allows concurrent reads but only one writer; the script can briefly block server writes. Either skip migrations in the script or document that the server should be stopped before running it.
- **`userRepository.upsert` two-step is racy.** SELECT-then-UPDATE-then-INSERT. Two webhooks for the same `plex_username` arriving in parallel could both see "no real exists" and step on each other. Fold to a single statement, or rely on the webhook now flowing through `jobQueue` (already serialised after this session's fix).
- ✓ **`pruneNonBufferDownloads` comment vs. code drift.** Comment updated to match actual cumulative delay values.
- **`app.get('*')` SPA fallback intercepts `/api/*` 404s.** In production, an unmatched `/api` path falls through to the SPA HTML serve. Add a `/api/*` 404 handler before the catch-all.
- **Migration idempotency.** Migration 2 (`ALTER TABLE shows ADD COLUMN ...`) is not idempotent on its own — version tracking is the only guard. If the version row is ever lost, replay errors. Wrap each `ALTER` in a try/catch for "duplicate column name" and treat as no-op.

## Style / hygiene

- ✓ **Plex token in URL for `plex.tv` calls.** Removed `?X-Plex-Token=` from home/users and friends URLs — token already sent via header.
- ✓ **`isDryMode()` duplicated.** Hoisted to `utils/dryMode.ts`; both service and job import from there.
- ✓ **Dead `trackersRouter.ts`.** Deleted.
- **`X-Plex-Client-Identifier: 'rollarr'` not unique.** Plex docs request a stable per-install UUID. Plex may collapse session counts for shared identifiers. Generate once at startup, persist to settings as `_plex_client_id`.
- ✓ **`frontend/src/api/client.ts` redefines shared types.** Now imports base types from `@rollarr/shared`; frontend-only composite types (`ShowSummary`, `ShowWithTrackers`, `TrackerWithUser`) remain local.
- **`SeerrWebhookPayload` type ignores Overseerr flat shape.** `webhooks/seerrWebhook.ts:42-44` casts through `Record<string, unknown>` to access `requestedBy_username`. Either widen the shared type to a union of Overseerr (flat) and Jellyseerr (nested), or use a discriminated parser.
- **Settings UI hint says "Restart required" for poll/maintenance cron.** This is a code limitation, not a design choice — `node-cron` tasks can be cancelled and rescheduled. Consider live-reloading the schedule when those settings change.

## Performance

- **n+1 in `apiRouter.get('/trackers')`.** Per-show prepared statement. Fine until shows scale; replace with a single JOIN.
- ✓ **Prepared statements recompiled on every call.** All repository methods now use lazy-compiled statement caches via a `prepare()` helper (compiles on first use, reuses thereafter).

## Architecture

- ✓ **`advanceWindow` god method (340 lines).** Extracted into 5 named phase functions: `fetchPlexHistory`, `updateTrackerProgress`, `buildWindowDiff`, `executeSonarrOps`, `commitWindowState`. Public `advanceWindow` is now ~35 lines.
- ✓ **`triggerForTvdbId` god method (220 lines).** Extracted: `resolveEarlyAccountId`, `computeWatchHistory`, `createTrackerRecord`. Public function reduced to ~45 lines.
- ✓ **`bootstrapNewShow` manual rollback.** Replaced raw `db.prepare('DELETE')` with `showRepository.delete()`. Non-null assertion removed by using the insert return value directly.
- ✓ **`SonarrEpisode.episodeFileId: number` misleading type.** Changed to `number | null` in `shared/src/types.ts`. All call sites updated with null-safe guards.
- ✓ **IIFE transaction pattern.** Transactions now assigned to named `const` and called explicitly (e.g. `const persistWindow = db.transaction(...); persistWindow()`).
- ✓ **Silent `catch {}` blocks.** Intent comments added throughout.
