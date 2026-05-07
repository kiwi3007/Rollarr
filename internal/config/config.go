package config

import "os"

// Config holds all application configuration loaded from environment variables.
type Config struct {
	SonarrURL          string
	SonarrAPIKey       string
	PlexURL            string
	PlexToken          string
	PlexDBPath         string
	SeerrWebhookSecret string
	APIToken           string
	Port               string
	DBPath             string
}

// Load reads configuration from environment variables, applying defaults where appropriate.
func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3001"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "rollarr.db"
	}

	return &Config{
		SonarrURL:          os.Getenv("SONARR_URL"),
		SonarrAPIKey:       os.Getenv("SONARR_API_KEY"),
		PlexURL:            os.Getenv("PLEX_URL"),
		PlexToken:          os.Getenv("PLEX_TOKEN"),
		PlexDBPath:         os.Getenv("PLEX_DB_PATH"),
		SeerrWebhookSecret: os.Getenv("SEERR_WEBHOOK_SECRET"),
		APIToken:           os.Getenv("ROLLARR_API_TOKEN"),
		Port:               port,
		DBPath:             dbPath,
	}
}
