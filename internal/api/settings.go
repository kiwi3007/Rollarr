package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kiwi3007/rollarr/internal/db/repository"
)

// secretKeys are settings keys whose non-empty values are redacted in GET
// responses and whose sentinel value ("***SET***") is preserved on PUT.
var secretKeys = map[string]struct{}{
	"sonarr_api_key":       {},
	"plex_token":           {},
	"seerr_webhook_secret": {},
	"plex_webhook_secret":  {},
	"rollarr_api_token":    {},
}

const secretSentinel = "***SET***"

// intRange bounds a numeric setting. A zero inactivity threshold would prune
// every show on the next pass, and a zero buffer would delete every unwatched
// episode, so both have a floor of 1.
type intRange struct{ min, max int }

// writableSettings is the allowlist for PUT /api/settings. Anything else in
// the table (schema_version, stat_* counters) is internal: the UI posts back
// the whole map it loaded, and writing those stale values would rewind them.
// A nil range means a free-form string. rollarr_api_token is deliberately not
// writable: it is read once at startup (a change would silently not apply),
// and blanking it would turn API auth off. Set ROLLARR_API_TOKEN instead.
var writableSettings = map[string]*intRange{
	"sonarr_url":                  nil,
	"sonarr_api_key":              nil,
	"plex_url":                    nil,
	"plex_token":                  nil,
	"plex_db_path":                nil,
	"seerr_webhook_secret":        nil,
	"plex_webhook_secret":         nil,
	"excluded_user_ids":           nil,
	"global_buffer_size":          {1, 100},
	"global_inactivity_days":      {1, 3650},
	"reconcile_interval_minutes":  {1, 1440},
	"inactivity_interval_minutes": {1, 1440},
}

type settingsHandler struct {
	settings *repository.SettingsRepository
}

func redact(all map[string]string) map[string]string {
	for k := range secretKeys {
		if v, ok := all[k]; ok && v != "" {
			all[k] = secretSentinel
		}
	}
	return all
}

func (h *settingsHandler) get(w http.ResponseWriter, r *http.Request) {
	all, err := h.settings.All()
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, redact(all))
}

// validateSettings drops keys that are not writable or that carry the secret
// sentinel (meaning "unchanged"), and rejects out-of-range numbers.
func validateSettings(incoming map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(incoming))
	for k, v := range incoming {
		rng, writable := writableSettings[k]
		if !writable {
			continue
		}
		if _, secret := secretKeys[k]; secret && v == secretSentinel {
			continue
		}
		if rng != nil {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < rng.min || n > rng.max {
				return nil, fmt.Errorf("%s must be a whole number from %d to %d", k, rng.min, rng.max)
			}
			v = strconv.Itoa(n)
		}
		out[k] = v
	}
	return out, nil
}

func (h *settingsHandler) put(w http.ResponseWriter, r *http.Request) {
	var incoming map[string]string
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	updates, err := validateSettings(incoming)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.settings.SetMany(updates); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	all, err := h.settings.All()
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, redact(all))
}
