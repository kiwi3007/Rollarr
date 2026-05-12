package api

import (
	"net/http"
	"strconv"

	"github.com/kiwi3007/rollarr/internal/logbuf"
)

type logsHandler struct{}

func (h *logsHandler) tail(w http.ResponseWriter, r *http.Request) {
	n := 200
	if s := r.URL.Query().Get("tail"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 && v <= 2000 {
			n = v
		}
	}
	lines := logbuf.Last(n)
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, map[string]interface{}{
		"lines": lines,
		"count": len(lines),
	})
}
