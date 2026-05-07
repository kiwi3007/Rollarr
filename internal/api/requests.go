package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kiwi3007/rollarr/internal/db/repository"
)

type requestsHandler struct {
	requests *repository.UserRequestRepository
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
