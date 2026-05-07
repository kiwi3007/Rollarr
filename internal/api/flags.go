package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/kiwi3007/rollarr/internal/db/repository"
)

type flagsHandler struct {
	flags *repository.DiscrepancyFlagRepository
}

func (h *flagsHandler) listOpen(w http.ResponseWriter, r *http.Request) {
	flags, err := h.flags.FindOpen()
	if err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if flags == nil {
		flags = []repository.DiscrepancyFlag{}
	}
	writeJSON(w, flags)
}

func (h *flagsHandler) updateStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		http.Error(w, "invalid flag id", http.StatusBadRequest)
		return
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch body.Status {
	case "resolved", "ignored":
		// valid
	default:
		http.Error(w, "status must be 'resolved' or 'ignored'", http.StatusBadRequest)
		return
	}

	if err := h.flags.UpdateStatus(id, body.Status); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]interface{}{"id": id, "status": body.Status})
}
