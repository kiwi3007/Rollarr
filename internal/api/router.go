package api

import (
	"bytes"
	"crypto/subtle"
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/reconcile"
	"github.com/kiwi3007/rollarr/internal/scheduler"
)

// NewRouter assembles the full chi router for the admin API and SPA fallback.
func NewRouter(
	shows *repository.ShowRepository,
	requests *repository.UserRequestRepository,
	flags *repository.DiscrepancyFlagRepository,
	settings *repository.SettingsRepository,
	reconciler *reconcile.Reconciler,
	queue *scheduler.JobQueue,
	token string,
	frontendFS embed.FS,
) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	// ── API routes (Bearer auth) ─────────────────────────────────────────────
	r.Group(func(r chi.Router) {
		r.Use(bearerAuth(token))

		showsH := &showsHandler{shows: shows, requests: requests, flags: flags, engine: nil, reconciler: reconciler, queue: queue}
		r.Get("/api/shows", showsH.list)
		r.Get("/api/shows/{tvdbId}", showsH.detail)
		r.Post("/api/shows/{tvdbId}/reconcile", showsH.reconcile)

		reqsH := &requestsHandler{requests: requests}
		r.Delete("/api/shows/{tvdbId}/requests/{plexUserId}", reqsH.deleteRequest)

		flagsH := &flagsHandler{flags: flags}
		r.Get("/api/flags", flagsH.listOpen)
		r.Put("/api/flags/{id}", flagsH.updateStatus)

		settingsH := &settingsHandler{settings: settings}
		r.Get("/api/settings", settingsH.get)
		r.Put("/api/settings", settingsH.put)
	})

	// ── SPA fallback ─────────────────────────────────────────────────────────
	// Build a sub-FS rooted at "frontend/dist" from the embed.
	distFS, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		// Fallback: serve a minimal placeholder if the embed is not available.
		r.Get("/*", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("frontend not built")) //nolint:errcheck
		})
		return r
	}

	fileServer := http.FileServer(http.FS(distFS))
	r.Get("/*", spaHandler(distFS, fileServer, token))

	return r
}

// bearerAuth returns middleware that verifies the Authorization: Bearer <token>
// header. If the configured token is empty the middleware is skipped (dev mode).
func bearerAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if token == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(auth, prefix) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			provided := auth[len(prefix):]
			if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// spaHandler serves static files from distFS and falls back to index.html for
// unmatched paths (React Router support). It also injects the API token into
// index.html via the window.__ROLLARR_TOKEN__ sentinel.
func spaHandler(distFS fs.FS, fileServer http.Handler, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Check if the file exists in the dist FS.
		cleanPath := strings.TrimPrefix(r.URL.Path, "/")
		if cleanPath == "" {
			cleanPath = "index.html"
		}

		f, err := distFS.Open(cleanPath)
		if err != nil {
			// File not found → serve index.html (SPA fallback).
			serveIndexHTML(w, r, distFS, token)
			return
		}
		f.Close()

		// For index.html specifically, inject the token.
		if cleanPath == "index.html" || r.URL.Path == "/" {
			serveIndexHTML(w, r, distFS, token)
			return
		}

		fileServer.ServeHTTP(w, r)
	}
}

// serveIndexHTML reads index.html, injects the API token, and writes the response.
func serveIndexHTML(w http.ResponseWriter, _ *http.Request, distFS fs.FS, token string) {
	content, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		http.Error(w, "frontend not found", http.StatusNotFound)
		return
	}

	// Inject token as a script tag before </head>.
	injection := `<script>window.__ROLLARR_TOKEN__ = "` + token + `";</script>`
	content = bytes.Replace(content, []byte("</head>"), []byte(injection+"</head>"), 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(content) //nolint:errcheck
}
