import Database from 'better-sqlite3';
import { settingsRepository } from '../db/repositories/settingsRepository';
import { createLogger } from '../utils/logger';
import type { PlexEpisodeMetadata, PlexHistoryEntry, PlexUser } from '@rollarr/shared';

const logger = createLogger('plex');

export class PlexError extends Error {
  constructor(message: string, public status?: number) {
    super(message);
    this.name = 'PlexError';
  }
}

function getPlexUrl(): string {
  const url = settingsRepository.get('plex_url') || '';
  if (!url) throw new PlexError('Plex URL not configured');
  return url.replace(/\/$/, '');
}

function getPlexToken(): string {
  const token = settingsRepository.get('plex_token') || '';
  if (!token) throw new PlexError('Plex token not configured');
  return token;
}

async function plexFetch<T>(url: string, token?: string): Promise<T> {
  const t = token ?? getPlexToken();
  const res = await fetch(url, {
    headers: {
      'X-Plex-Token': t,
      'X-Plex-Client-Identifier': 'rollarr',
      'Accept': 'application/json',
    },
  });
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    logger.error(`Plex GET ${url} → ${res.status}`, body);
    throw new PlexError(`Plex API error ${res.status}`, res.status);
  }
  return res.json() as Promise<T>;
}

// accountId → Plex account username (stable, used for lookups)
let userMap: Map<string, string> = new Map();
// Plex display name (title) → accountId (secondary lookup for Seerr which sends display names)
let displayNameMap: Map<string, string> = new Map();
let adminAccountId: number | undefined;

export const plexService = {
  async getGlobalHistory(limit = 500): Promise<PlexHistoryEntry[]> {
    const base = getPlexUrl();
    const data = await plexFetch<{ MediaContainer: { Metadata?: PlexHistoryEntry[] } }>(
      `${base}/status/sessions/history/all?limit=${limit}&sort=viewedAt:desc`
    );
    return data.MediaContainer.Metadata ?? [];
  },

  // Returns all season episodes with viewCount. Requires the ratingKey of the season container.
  async getSeasonEpisodes(seasonRatingKey: string): Promise<PlexEpisodeMetadata[]> {
    const base = getPlexUrl();
    const data = await plexFetch<{ MediaContainer: { Metadata?: PlexEpisodeMetadata[] } }>(
      `${base}/library/metadata/${seasonRatingKey}/children`
    );
    return data.MediaContainer.Metadata ?? [];
  },

  // Find a show's ratingKey by scanning the TV section using tvdbId from Plex's guid
  async findShowRatingKey(tvdbId: number): Promise<string | undefined> {
    const base = getPlexUrl();
    // First get library sections to find TV library key
    const sectionsData = await plexFetch<{
      MediaContainer: { Directory: Array<{ key: string; type: string }> };
    }>(`${base}/library/sections`);

    const tvSections = sectionsData.MediaContainer.Directory.filter((d) => d.type === 'show');

    for (const section of tvSections) {
      const showsData = await plexFetch<{
        MediaContainer: { Metadata?: Array<{ ratingKey: string; guid: string; Guid?: Array<{ id: string }> }> };
      }>(`${base}/library/sections/${section.key}/all?type=2&includeGuids=1`);

      const shows = showsData.MediaContainer.Metadata ?? [];
      for (const show of shows) {
        // Modern Plex: Guid array contains "tvdb://XXXXX"
        const modernMatch = (show.Guid ?? []).some((g) => g.id === `tvdb://${tvdbId}`);
        // Legacy Plex agent: guid field contains "com.plexapp.agents.thetvdb://XXXXX/..."
        const legacyMatch = /thetvdb:\/\/(\d+)/.exec(show.guid)?.[1] === String(tvdbId);
        if (modernMatch || legacyMatch) {
          return show.ratingKey;
        }
      }
    }
    return undefined;
  },

  // Returns seasons for a show ratingKey
  async getShowSeasons(showRatingKey: string): Promise<Array<{ ratingKey: string; index: number; leafCount: number }>> {
    const base = getPlexUrl();
    const data = await plexFetch<{
      MediaContainer: { Metadata?: Array<{ ratingKey: string; index: number; leafCount: number }> };
    }>(`${base}/library/metadata/${showRatingKey}/children`);
    return data.MediaContainer.Metadata ?? [];
  },

  // Get episodes for a specific season number given a show's ratingKey
  async getEpisodesForSeason(
    showRatingKey: string,
    seasonNumber: number
  ): Promise<PlexEpisodeMetadata[]> {
    const seasons = await this.getShowSeasons(showRatingKey);
    const season = seasons.find((s) => s.index === seasonNumber);
    if (!season) return [];
    return this.getSeasonEpisodes(season.ratingKey);
  },

  // Returns ALL episodes across ALL seasons for a show (including unavailable/deleted ones
  // whose metadata Plex still retains). Uses /allLeaves — more comprehensive than per-season
  // for detecting pre-existing watch progress on shows being re-added.
  async getAllEpisodes(showRatingKey: string): Promise<PlexEpisodeMetadata[]> {
    const base = getPlexUrl();
    const data = await plexFetch<{ MediaContainer: { Metadata?: PlexEpisodeMetadata[] } }>(
      `${base}/library/metadata/${showRatingKey}/allLeaves`
    );
    return data.MediaContainer.Metadata ?? [];
  },

  // Returns play history for a single show — avoids the global 5000-entry limit by targeting
  // just this show's grandparentRatingKey. Covers plays for deleted/unavailable episodes too.
  async getShowHistory(showRatingKey: string, limit = 200): Promise<PlexHistoryEntry[]> {
    const base = getPlexUrl();
    const data = await plexFetch<{ MediaContainer: { Metadata?: PlexHistoryEntry[] } }>(
      `${base}/status/sessions/history/all?grandparentRatingKey=${showRatingKey}&limit=${limit}&sort=viewedAt:desc`
    );
    return data.MediaContainer.Metadata ?? [];
  },

  // Build accountId → username map from all user types
  async buildUserMap(): Promise<Map<string, string>> {
    const token = getPlexToken();
    const newMap     = new Map<string, string>();
    const newTitleMap = new Map<string, string>();

    // Admin's own account
    try {
      const adminData = await plexFetch<{ id: number; username: string; title?: string }>(
        'https://plex.tv/api/v2/user',
        token
      );
      newMap.set(String(adminData.id), adminData.username);
      if (adminData.title) newTitleMap.set(adminData.title.toLowerCase(), String(adminData.id));
      adminAccountId = adminData.id;
      settingsRepository.set('_admin_plex_account_id', String(adminData.id));
    } catch (err) {
      logger.warn('Failed to fetch admin Plex user', err);
      // Fall back to last known value persisted in DB
      const cached = settingsRepository.get('_admin_plex_account_id');
      if (cached) adminAccountId = Number(cached);
    }

    // Home users (managed children + own-account home members)
    try {
      const homeData = await plexFetch<{
        MediaContainer?: { User?: PlexUser[] };
        // v2 format
        users?: PlexUser[];
      }>(`https://plex.tv/api/v2/home/users?X-Plex-Token=${token}`, token);

      const homeUsers: PlexUser[] =
        homeData.users ??
        homeData.MediaContainer?.User ??
        [];

      for (const u of homeUsers) {
        newMap.set(String(u.id), u.username || u.email || String(u.id));
        if (u.title) newTitleMap.set(u.title.toLowerCase(), String(u.id));
      }
    } catch (err) {
      logger.warn('Failed to fetch Plex home users', err);
    }

    // Friends / external shared users
    try {
      const friendsData = await plexFetch<PlexUser[]>(
        `https://plex.tv/api/v2/friends?X-Plex-Token=${token}`,
        token
      );
      for (const u of Array.isArray(friendsData) ? friendsData : []) {
        newMap.set(String(u.id), u.username || u.email || String(u.id));
        if (u.title) newTitleMap.set(u.title.toLowerCase(), String(u.id));
      }
    } catch (err) {
      logger.warn('Failed to fetch Plex friends', err);
    }

    userMap      = newMap;
    displayNameMap = newTitleMap;
    logger.info(`User map refreshed: ${userMap.size} users`);
    return userMap;
  },

  getUsernameForAccountId(accountId: string): string {
    return userMap.get(accountId) ?? `user_${accountId}`;
  },

  getCachedUserMap(): Map<string, string> {
    return userMap;
  },

  getAdminAccountId(): number | undefined {
    if (adminAccountId !== undefined) return adminAccountId;
    const cached = settingsRepository.get('_admin_plex_account_id');
    return cached ? Number(cached) : undefined;
  },

  // Query Plex's local SQLite DB for episodes any user has marked as watched.
  // Returns Map<accountId, Map<season, highestEpisodeNumber>> across all seasons.
  // Covers "Mark as Watched" for all user types including remote friends — play history alone misses this.
  getMarkedWatchedFromDb(plexRatingKey: string): Map<number, Map<number, number>> {
    const dbPath = settingsRepository.get('plex_db_path') || '';
    if (!dbPath) return new Map();

    let plexDb: InstanceType<typeof Database> | undefined;
    try {
      plexDb = new Database(dbPath, { readonly: true, fileMustExist: true });

      // Plex's local DB uses server-local account IDs. The admin is always local id=1;
      // all other accounts (home users / friends) have local id = their cloud account id.
      // Build a local→cloud translation so the returned map uses cloud IDs throughout.
      const accountRows = plexDb
        .prepare(`SELECT id, name FROM accounts WHERE name IS NOT NULL AND name != ''`)
        .all() as Array<{ id: number; name: string }>;

      const localToCloud = new Map<number, number>();
      const adminCloudId = adminAccountId ?? (
        settingsRepository.get('_admin_plex_account_id')
          ? Number(settingsRepository.get('_admin_plex_account_id'))
          : undefined
      );
      for (const acc of accountRows) {
        if (acc.id === 1 && adminCloudId) {
          // Admin's local id is always 1; map to their known cloud id
          localToCloud.set(1, adminCloudId);
        } else if (acc.id > 1) {
          // For all other accounts, local id IS the cloud id
          localToCloud.set(acc.id, acc.id);
        }
      }

      const rows = plexDb
        .prepare(
          `SELECT s."index" AS season_num, mi."index" AS ep_num, mis.account_id
           FROM metadata_item_settings mis
           JOIN metadata_items mi ON mis.guid = mi.guid
           JOIN metadata_items s ON mi.parent_id = s.id
           WHERE s.parent_id = ? AND s.metadata_type = 3 AND mi.metadata_type = 4 AND mis.view_count > 0`
        )
        .all(Number(plexRatingKey)) as Array<{ season_num: number; ep_num: number; account_id: number }>;

      const result = new Map<number, Map<number, number>>();
      for (const row of rows) {
        // Skip any local account that has no cloud mapping — avoids persisting the raw
        // local ID (e.g. admin's local id=1) as if it were a real cloud account ID.
        const cloudId = localToCloud.get(row.account_id);
        if (cloudId === undefined) continue;
        let seasons = result.get(cloudId);
        if (!seasons) { seasons = new Map(); result.set(cloudId, seasons); }
        const prev = seasons.get(row.season_num) ?? 0;
        if (row.ep_num > prev) seasons.set(row.season_num, row.ep_num);
      }
      logger.debug(`Plex DB: ${result.size} user(s) have marked episodes watched`);
      return result;
    } catch (err) {
      logger.warn('Could not read Plex SQLite DB', err instanceof Error ? err.message : String(err));
      return new Map();
    } finally {
      plexDb?.close();
    }
  },

  // Resolve a Seerr-provided name to a Plex accountId.
  // Tries stable account username first, falls back to display name (title).
  resolveAccountId(name: string): string | undefined {
    const lower = name.toLowerCase();
    for (const [accountId, username] of userMap) {
      if (username.toLowerCase() === lower) return accountId;
    }
    return displayNameMap.get(lower);
  },
};
