// ── Enums ──────────────────────────────────────────────────────────────────

export enum ShowStatus {
  Active    = 'Active',
  Stale     = 'Stale',
  Completed = 'Completed',
  Removed   = 'Removed',
}

export enum EpisodeStatus {
  Monitored   = 'Monitored',
  Unmonitored = 'Unmonitored',
  Watched     = 'Watched',
  Deleted     = 'Deleted',
}

// ── DB row shapes ──────────────────────────────────────────────────────────

export interface ShowRow {
  id:                   number;
  sonarr_id:            number;
  title:                string;
  tvdb_id:              number;
  status:               ShowStatus;
  buffer_size:          number;
  current_window_start: number;
  current_season:       number;
  created_at:           string;
  updated_at:           string;
}

export interface UserRow {
  id:              number;
  plex_account_id: string;
  plex_username:   string;
  last_active_at:  string;
}

export interface TrackerRow {
  id:                   number;
  show_id:              number;
  user_id:              number;
  last_watched_episode: number;
  last_watched_season:  number;
  is_active:            1 | 0;
  watchlist_active:     1 | 0;
  rewatch_since:        string | null;
  created_at:           string;
}

export interface EpisodeRow {
  id:                number;
  show_id:           number;
  sonarr_episode_id: number;
  sonarr_file_id:    number | null;
  season:            number;
  episode_number:    number;
  status:            EpisodeStatus;
}

export interface SettingRow {
  key:   string;
  value: string;
}

// ── Sonarr v3 API shapes ───────────────────────────────────────────────────

export interface SonarrSeries {
  id:       number;
  title:    string;
  tvdbId:   number;
  seasons:  SonarrSeason[];
  status:   string;
}

export interface SonarrSeason {
  seasonNumber: number;
  monitored:    boolean;
  statistics?:  { totalEpisodeCount: number; episodeCount: number };
}

export interface SonarrEpisode {
  id:            number;
  seriesId:      number;
  seasonNumber:  number;
  episodeNumber: number;
  monitored:     boolean;
  hasFile:       boolean;
  episodeFileId: number;
}

export interface SonarrEpisodeFile {
  id:       number;
  seriesId: number;
  path:     string;
}

// ── Plex API shapes ────────────────────────────────────────────────────────

export interface PlexHistoryEntry {
  accountID:        number;
  grandparentTitle: string | undefined;  // absent for movies
  grandparentKey:   string | undefined;  // absent for movies
  parentIndex:      number;   // season
  index:            number;   // episode
  viewedAt:         number;
  ratingKey:        string;
}

export interface PlexEpisodeMetadata {
  ratingKey:            string;
  title:                string;
  parentIndex:          number;   // season
  index:                number;   // episode
  viewCount:            number;
  grandparentRatingKey: string;
}

export interface PlexUser {
  id:        number;
  uuid:      string;
  username:  string;
  title?:    string;   // display name, used by Seerr as plexUsername
  email?:    string;
}

// ── Seerr webhook payload ──────────────────────────────────────────────────

export interface SeerrWebhookPayload {
  notification_type: string;
  media: {
    media_type: 'tv' | 'movie';
    tmdbId:     number;
    tvdbId:     number;
    status:     string;
  };
  request?: {
    requestedBy: {
      plexUsername: string;
    };
  };
  subject: string;
}

// ── API response shapes ────────────────────────────────────────────────────

export interface ShowWithTrackers extends ShowRow {
  trackers: (TrackerRow & { plex_username: string })[];
  episodes: EpisodeRow[];
}

export interface ApiResponse<T> {
  data:   T;
  error?: string;
}

export type SettingsMap = Record<string, string>;
