// Types
export interface UserBufferInfo {
  display_name: string;
  season: number;
  buffer_start: number;
  buffer_end: number;
}

export interface ShowSummary {
  tvdb_id: number;
  sonarr_id: number;
  title: string;
  status: 'active' | 'inactive' | 'removed';
  effective_buffer_size: number;
  active_request_count: number;
  last_activity_at: string | null;
  poster_url: string;
  fanart_url: string;
  user_buffers: UserBufferInfo[];
}

export interface UserRequest {
  plex_user_id: string;
  display_name: string;
  tvdb_id: number;
  request_timestamp: string;
  is_rewatching: boolean;
  last_watched_season: number | null;
  last_watched_episode: number | null;
}

export interface Flag {
  id: number;
  tvdb_id: number;
  sonarr_episode_id: number | null;
  issue_description: string;
  status: 'open' | 'resolved' | 'ignored';
  created_at: string;
  updated_at: string;
}

export interface ShowEvent {
  id: number;
  tvdb_id: number;
  action: string;
  detail: string;
  created_at: string;
}

export interface Stats {
  bytes_deleted: number;
  files_deleted: number;
}

export interface ShowDetail extends ShowSummary {
  requests: UserRequest[];
  expected_state: Record<number, number[]>;
  all_episodes: Record<number, number[]>;
  open_flags: Flag[];
  events: ShowEvent[];
}

export type SettingsMap = Record<string, string>;

export interface PlexLibraryShow {
  tvdb_id: number;
  title: string;
  year: number;
  plex_key: string;
  poster_url: string;
  in_sonarr: boolean;
  sonarr_id: number;
  is_tracked: boolean;
}

export interface WindowSegment {
  season: number;
  start: number;
  end: number;
}

export interface PreviewWatcher {
  plex_user_id: string;
  display_name: string;
  detected: boolean;
  is_rewatching: boolean;
  highest_season: number;
  highest_episode: number;
  segments: WindowSegment[];
}

export interface ShowPreview {
  tvdb_id: number;
  title: string;
  buffer_size: number;
  expected_state: Record<number, number[]>;
  all_episodes: Record<number, number[]>;
  watchers: PreviewWatcher[];
  files_deleted: number;
  bytes_freed: number;
}

export interface PlexUserOption {
  id: number;
  name: string;
}

export interface ManualUser {
  plex_user_id: string;
  display_name: string;
  requested_season: number;
}

// API client
const TOKEN = (window as any).__ROLLARR_TOKEN__ ?? '';

async function apiFetch<T>(path: string, opts?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...opts,
    headers: {
      'Content-Type': 'application/json',
      ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
      ...opts?.headers,
    },
  });
  if (!res.ok) {
    const body = (await res.text().catch(() => '')).trim();
    throw new Error(body || `${res.status} ${res.statusText}`);
  }
  return res.json();
}

export const api = {
  getShows: () => apiFetch<ShowSummary[]>('/api/shows'),
  getShow: (tvdbId: number) => apiFetch<ShowDetail>(`/api/shows/${tvdbId}`),
  reconcileShow: (tvdbId: number) => apiFetch<{queued: boolean}>(`/api/shows/${tvdbId}/reconcile`, { method: 'POST' }),
  deleteRequest: (tvdbId: number, plexUserId: string) =>
    apiFetch<{ok: boolean}>(`/api/shows/${tvdbId}/requests/${encodeURIComponent(plexUserId)}`, { method: 'DELETE' }),
  getFlags: () => apiFetch<Flag[]>('/api/flags'),
  updateFlag: (id: number, status: 'resolved' | 'ignored') =>
    apiFetch<Flag>(`/api/flags/${id}`, { method: 'PUT', body: JSON.stringify({ status }) }),
  getStats: () => apiFetch<Stats>('/api/stats'),
  getLibrary: () => apiFetch<PlexLibraryShow[]>('/api/plex/library'),
  getShowPreview: (tvdbId: number, override?: { user: string; name: string; season: number }) => {
    const params = override
      ? `?user=${encodeURIComponent(override.user)}&name=${encodeURIComponent(override.name)}&season=${override.season}`
      : '';
    return apiFetch<ShowPreview>(`/api/plex/library/${tvdbId}/preview${params}`);
  },
  getPlexUsers: () => apiFetch<PlexUserOption[]>('/api/plex/users'),
  addShow: (tvdbId: number, manualUser?: ManualUser) =>
    apiFetch<{queued: boolean}>('/api/shows', {
      method: 'POST',
      body: JSON.stringify({ tvdb_id: tvdbId, ...(manualUser ? { manual_user: manualUser } : {}) }),
    }),
  getSettings: () => apiFetch<SettingsMap>('/api/settings'),
  saveSettings: (s: SettingsMap) => apiFetch<SettingsMap>('/api/settings', { method: 'PUT', body: JSON.stringify(s) }),
};
