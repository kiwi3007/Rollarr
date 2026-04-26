function required(key: string): string {
  const val = process.env[key];
  if (!val) throw new Error(`Missing required env var: ${key}`);
  return val;
}

function optional(key: string, fallback: string): string {
  return process.env[key] ?? fallback;
}

export const config = {
  port:               parseInt(optional('PORT', '3001'), 10),
  nodeEnv:            optional('NODE_ENV', 'development'),
  dbPath:             optional('DB_PATH', './rollarr.db'),

  // These start as env var seeds; runtime values come from settings table
  sonarrUrl:          optional('SONARR_URL', ''),
  sonarrApiKey:       optional('SONARR_API_KEY', ''),
  plexUrl:            optional('PLEX_URL', ''),
  plexToken:          optional('PLEX_TOKEN', ''),
  seerrWebhookSecret: optional('SEERR_WEBHOOK_SECRET', ''),
};
