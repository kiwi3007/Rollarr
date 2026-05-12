package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kiwi3007/rollarr/internal/db/repository"
)

type requestsHandler struct {
	requests *repository.UserRequestRepository
}

// patchRequest handles PATCH /api/shows/{tvdbId}/requests/{plexUserId}.
// Accepts {"is_rewatching": bool, "since": "RFC3339"} — since defaults to now.
func (h *requestsHandler) patchRequest(w http.ResponseWriter, r *http.Request) {
	tvdbId, ok := parseTVDBId(w, r)
	if !ok {
		return
	}
	plexUserId := chi.URLParam(r, "plexUserId")
	if plexUserId == "" {
		http.Error(w, "missing plexUserId", http.StatusBadRequest)
		return
	}

	var body struct {
		IsRewatching bool   `json:"is_rewatching"`
		Since        string `json:"since"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	since := time.Now()
	if body.Since != "" {
		t, err := time.Parse(time.RFC3339, body.Since)
		if err != nil {
			http.Error(w, "since must be RFC3339: "+err.Error(), http.StatusBadRequest)
			return
		}
		since = t
	}

	if err := h.requests.SetRewatching(tvdbId, plexUserId, body.IsRewatching, since); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "is_rewatching": body.IsRewatching, "since": since})
}

func (h *requestsHandler) deleteRequest(w http.ResponseWriter, r *http.Request) {
	tvdbId, ok := parseTVDBId(w, r)
	if !ok {
		return
	}

	plexUserId := chi.URLParam(r, "plexUserId")
	if plexUserId == "" {
		http.Error(w, "missing plexUserId", http.StatusBadRequest)
		return
	}

	if err := h.requests.Delete(tvdbId, plexUserId); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]bool{"ok": true})
}
