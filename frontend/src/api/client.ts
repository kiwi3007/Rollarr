import { getToken, signalUnauthorized } from '../auth';

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
  managed: boolean;
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

export interface PlexUser {
  id: string;
  username: string;
}

export type SettingsMap = Record<string, string>;

// API client
async function apiFetch<T>(path: string, opts?: RequestInit): Promise<T> {
  const token = getToken();
  const res = await fetch(path, {
    ...opts,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...opts?.headers,
    },
  });
  if (res.status === 401) {
    signalUnauthorized();
    throw new Error('Not signed in');
  }
  if (!res.ok) {
    // Surface the server's message (e.g. settings validation errors).
    const text = (await res.text().catch(() => '')).trim();
    throw new Error(text || `${res.status} ${res.statusText}`);
  }
  return res.json();
}

/** Check a candidate token against the API without storing it. */
export async function checkToken(token: string): Promise<boolean> {
  const res = await fetch('/api/stats', { headers: { Authorization: `Bearer ${token}` } });
  return res.ok;
}

export const api = {
  getShows: () => apiFetch<ShowSummary[]>('/api/shows'),
  getShow: (tvdbId: number) => apiFetch<ShowDetail>(`/api/shows/${tvdbId}`),
  reconcileShow: (tvdbId: number) => apiFetch<{queued: boolean}>(`/api/shows/${tvdbId}/reconcile`, { method: 'POST' }),
  deleteRequest: (tvdbId: number, plexUserId: string) =>
    apiFetch<{ok: boolean}>(`/api/shows/${tvdbId}/requests/${encodeURIComponent(plexUserId)}`, { method: 'DELETE' }),
  setManaged: (tvdbId: number, managed: boolean) =>
    apiFetch<{ok: boolean; managed: boolean}>(`/api/shows/${tvdbId}/managed`, { method: 'PUT', body: JSON.stringify({ managed }) }),
  getPlexUsers: () => apiFetch<PlexUser[]>('/api/plex/users'),
  getFlags: () => apiFetch<Flag[]>('/api/flags'),
  updateFlag: (id: number, status: 'resolved' | 'ignored') =>
    apiFetch<Flag>(`/api/flags/${id}`, { method: 'PUT', body: JSON.stringify({ status }) }),
  getStats: () => apiFetch<Stats>('/api/stats'),
  getSettings: () => apiFetch<SettingsMap>('/api/settings'),
  saveSettings: (s: SettingsMap) => apiFetch<SettingsMap>('/api/settings', { method: 'PUT', body: JSON.stringify(s) }),
};
