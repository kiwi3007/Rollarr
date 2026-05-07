package api

import (
	"encoding/json"
	"net/http"

	"github.com/kiwi3007/rollarr/internal/db/repository"
)

// secretKeys are settings keys whose non-empty values are redacted in GET
// responses and whose sentinel value ("***SET***") is preserved on PUT.
var secretKeys = map[string]struct{}{
	"sonarr_api_key":       {},
	"plex_token":           {},
	"seerr_webhook_secret": {},
	"rollarr_api_token":    {},
}

const secretSentinel = "***SET***"

type settingsHandler struct {
	settings *repository.SettingsRepository
}

func (h *settingsHandler) get(w http.ResponseWriter, r *http.Request) {
	all, err := h.settings.All()
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Redact secrets.
	for k := range secretKeys {
		if v, ok := all[k]; ok && v != "" {
			all[k] = secretSentinel
		}
	}

	writeJSON(w, all)
}

func (h *settingsHandler) put(w http.ResponseWriter, r *http.Request) {
	var incoming map[string]string
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// For secret keys, if the caller sent the sentinel, preserve the existing value.
	for k := range secretKeys {
		v, ok := incoming[k]
		if !ok {
			continue
		}
		if v == secretSentinel {
			// Don't overwrite — remove from the map so we don't touch the stored value.
			delete(incoming, k)
		}
	}

	if err := h.settings.SetMany(incoming); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return updated settings (redacted).
	all, err := h.settings.All()
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for k := range secretKeys {
		if v, ok := all[k]; ok && v != "" {
			all[k] = secretSentinel
		}
	}

	writeJSON(w, all)
}
