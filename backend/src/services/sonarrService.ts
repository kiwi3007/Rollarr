import { settingsRepository } from '../db/repositories/settingsRepository';
import { createLogger } from '../utils/logger';
import type { SonarrEpisode, SonarrSeries } from '@rollarr/shared';

const logger = createLogger('sonarr');

export class SonarrError extends Error {
  constructor(message: string, public status?: number) {
    super(message);
    this.name = 'SonarrError';
  }
}

function getBaseUrl(): string {
  const url = settingsRepository.get('sonarr_url') || '';
  if (!url) throw new SonarrError('Sonarr URL not configured');
  return `${url.replace(/\/$/, '')}/api/v3`;
}

function getApiKey(): string {
  const key = settingsRepository.get('sonarr_api_key') || '';
  if (!key) throw new SonarrError('Sonarr API key not configured');
  return key;
}

async function sonarrFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const url = `${getBaseUrl()}${path}`;
  const apiKey = getApiKey();

  const res = await fetch(url, {
    ...init,
    headers: {
      'X-Api-Key': apiKey,
      'Content-Type': 'application/json',
      ...(init.headers ?? {}),
    },
  });

  if (!res.ok) {
    const body = await res.text().catch(() => '');
    logger.error(`Sonarr ${init.method ?? 'GET'} ${path} → ${res.status}`, body);
    throw new SonarrError(`Sonarr API error ${res.status}: ${body}`, res.status);
  }

  const text = await res.text();
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

export const sonarrService = {
  async getSeries(sonarrId: number): Promise<SonarrSeries> {
    return sonarrFetch<SonarrSeries>(`/series/${sonarrId}`);
  },

  // Find a series already in Sonarr's library by TVDB ID (returns real database ID).
  // Retries because Seerr adds the series to Sonarr and fires the webhook simultaneously —
  // Sonarr may not have finished inserting the series yet on the first attempt.
  async getSeriesByTvdbId(tvdbId: number, { retries = 5, delayMs = 2000 } = {}): Promise<SonarrSeries | undefined> {
    for (let i = 0; i <= retries; i++) {
      const results = await sonarrFetch<SonarrSeries[]>(`/series?tvdbId=${tvdbId}`);
      const found = results.find((s) => s.id > 0);
      if (found) return found;
      if (i < retries) {
        logger.info(`Series tvdbId=${tvdbId} not in Sonarr library yet, retrying in ${delayMs}ms (${i + 1}/${retries})`);
        await new Promise((r) => setTimeout(r, delayMs));
      }
    }
    return undefined;
  },

  async getEpisodes(sonarrId: number, season: number): Promise<SonarrEpisode[]> {
    return sonarrFetch<SonarrEpisode[]>(
      `/episode?seriesId=${sonarrId}&seasonNumber=${season}`
    );
  },

  async getAllEpisodes(sonarrId: number): Promise<SonarrEpisode[]> {
    return sonarrFetch<SonarrEpisode[]>(`/episode?seriesId=${sonarrId}`);
  },

  async setEpisodesMonitored(episodeIds: number[], monitored: boolean): Promise<void> {
    if (episodeIds.length === 0) return;
    await sonarrFetch<void>('/episode/monitor', {
      method: 'PUT',
      body: JSON.stringify({ episodeIds, monitored }),
    });
    logger.debug(`Set ${episodeIds.length} episodes monitored=${monitored}`);
  },

  async deleteEpisodeFile(fileId: number): Promise<void> {
    await sonarrFetch<void>(`/episodefile/${fileId}`, { method: 'DELETE' });
    logger.debug(`Deleted episode file ${fileId}`);
  },

  async episodeSearch(episodeIds: number[]): Promise<void> {
    if (episodeIds.length === 0) return;
    await sonarrFetch<void>('/command', {
      method: 'POST',
      body: JSON.stringify({ name: 'EpisodeSearch', episodeIds }),
    });
    logger.debug(`Triggered search for episode IDs: ${episodeIds.join(', ')}`);
  },

  async getQueueForSeries(sonarrId: number): Promise<Array<{ id: number; seriesId?: number; episodeId?: number; title?: string }>> {
    const data = await sonarrFetch<{ records: Array<{ id: number; seriesId?: number; episodeId?: number; title?: string }> }>(
      `/queue?seriesId=${sonarrId}&pageSize=50&includeUnknownSeriesItems=false`
    );
    return data.records ?? [];
  },

  async removeFromQueue(queueId: number): Promise<void> {
    await sonarrFetch<void>(`/queue/${queueId}?removeFromClient=true&blocklist=false`, {
      method: 'DELETE',
    });
    logger.info(`Removed queue item ${queueId} from downloader`);
  },

  async getAllSeries(): Promise<SonarrSeries[]> {
    return sonarrFetch<SonarrSeries[]>('/series');
  },

  async unmonitorAllEpisodes(sonarrId: number): Promise<void> {
    const episodes = await this.getAllEpisodes(sonarrId);
    const ids = episodes.map((e) => e.id);
    if (ids.length > 0) {
      await this.setEpisodesMonitored(ids, false);
    }
  },
};
