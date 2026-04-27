// ── Inline types ────────────────────────────────────────────────────────────

export interface ShowRow {
  id: number; sonarr_id: number; title: string; tvdb_id: number;
  status: 'Active' | 'Stale' | 'Completed';
  buffer_size: number; current_window_start: number; current_season: number;
  poster_url: string | null; backdrop_url: string | null;
  created_at: string; updated_at: string;
}

export interface SeasonData { season: number; total_episodes: number; }

export interface ShowSummary extends ShowRow {
  trackerCount: number;
  trackers: TrackerWithUser[];
  season_data: SeasonData[];
}

export interface TrackerRow {
  id: number; show_id: number; user_id: number;
  last_watched_episode: number; last_watched_season: number;
  is_active: 1 | 0; watchlist_active: 1 | 0; created_at: string;
}
export type TrackerWithUser = TrackerRow & { plex_username: string; last_activity: string; };

export interface EpisodeRow {
  id: number; show_id: number; sonarr_episode_id: number;
  sonarr_file_id: number | null; season: number; episode_number: number;
  status: 'Monitored' | 'Unmonitored' | 'Watched' | 'Deleted';
}

export interface ShowWithTrackers extends ShowRow {
  trackers: TrackerWithUser[];
  episodes: EpisodeRow[];
  season_data: SeasonData[];
}

export interface UserRow {
  id: number; plex_account_id: string; plex_username: string; last_active_at: string;
}

export type SettingsMap = Record<string, string>;

// ── Fetch helper ────────────────────────────────────────────────────────────

async function apiFetch<T>(
  path: string,
  init?: RequestInit
): Promise<{ data: T } | { error: string }> {
  try {
    const res = await fetch(`/api${path}`, {
      headers: { 'Content-Type': 'application/json' },
      ...init,
    });
    const json = await res.json();
    if (!res.ok) return { error: json.error ?? `HTTP ${res.status}` };
    return json as { data: T };
  } catch (err) {
    return { error: err instanceof Error ? err.message : 'Network error' };
  }
}

// ── API client ──────────────────────────────────────────────────────────────

export const api = {
  getShows:         () => apiFetch<ShowSummary[]>('/shows'),
  getShow:          (id: number) => apiFetch<ShowWithTrackers>(`/shows/${id}`),
  refreshShow:      (id: number) => apiFetch<{ queued: boolean }>(`/shows/${id}/refresh`, { method: 'POST' }),
  getUsers:         () => apiFetch<UserRow[]>('/users'),
  getTrackers:      () => apiFetch<(TrackerWithUser & { show_title: string })[]>('/trackers'),
  dropTracker:      (showId: number, userId: number) =>
    apiFetch<{ ok: boolean }>(`/shows/${showId}/trackers/${userId}`, { method: 'DELETE' }),
  getSettings:      () => apiFetch<SettingsMap>('/settings'),
  saveSettings:     (settings: SettingsMap) =>
    apiFetch<SettingsMap>('/settings', { method: 'PUT', body: JSON.stringify(settings) }),
};
