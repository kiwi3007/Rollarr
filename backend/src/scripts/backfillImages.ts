import { runMigrations } from '../db/migrations';
import { showRepository } from '../db/repositories/showRepository';
import { settingsRepository } from '../db/repositories/settingsRepository';

async function main() {
  runMigrations();

  const sonarrUrl = settingsRepository.get('sonarr_url')?.replace(/\/$/, '');
  const apiKey    = settingsRepository.get('sonarr_api_key');

  if (!sonarrUrl || !apiKey) {
    console.error('Sonarr URL or API key not configured in settings');
    process.exit(1);
  }

  const shows = showRepository.findAll();
  console.log(`Backfilling images for ${shows.length} show(s)…`);

  for (const show of shows) {
    try {
      const res = await fetch(`${sonarrUrl}/api/v3/series/${show.sonarr_id}`, {
        headers: { 'X-Api-Key': apiKey },
      });
      if (!res.ok) {
        console.warn(`  ✗ ${show.title} — Sonarr returned ${res.status}`);
        continue;
      }
      const data = await res.json() as { images?: Array<{ coverType: string; remoteUrl?: string }> };
      const poster_url   = data.images?.find((i) => i.coverType === 'poster')?.remoteUrl  ?? null;
      const backdrop_url = data.images?.find((i) => i.coverType === 'fanart')?.remoteUrl  ?? null;
      showRepository.update(show.id, { poster_url, backdrop_url });
      console.log(`  ✓ ${show.title}`);
      console.log(`      poster:   ${poster_url ?? '(none)'}`);
      console.log(`      backdrop: ${backdrop_url ?? '(none)'}`);
    } catch (err) {
      console.warn(`  ✗ ${show.title} — ${err}`);
    }
  }

  console.log('Done.');
}

main();
