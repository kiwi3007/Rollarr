package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	rollarr "github.com/kiwi3007/rollarr"
	"github.com/kiwi3007/rollarr/internal/api"
	"github.com/kiwi3007/rollarr/internal/config"
	"github.com/kiwi3007/rollarr/internal/logbuf"
	"github.com/kiwi3007/rollarr/internal/db"
	"github.com/kiwi3007/rollarr/internal/db/repository"
	"github.com/kiwi3007/rollarr/internal/plex"
	"github.com/kiwi3007/rollarr/internal/proxy"
	"github.com/kiwi3007/rollarr/internal/reconcile"
	"github.com/kiwi3007/rollarr/internal/scheduler"
	"github.com/kiwi3007/rollarr/internal/sonarr"
	"github.com/kiwi3007/rollarr/internal/state"
	"github.com/kiwi3007/rollarr/internal/webhook"
)

func main() {
	logbuf.Install(nil) // capture log output into ring buffer; nil = also write to stderr
	cfg := config.Load()

	// ── Database ─────────────────────────────────────────────────────────────
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer database.Close()

	if err := db.RunMigrations(database); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	// ── Repositories ─────────────────────────────────────────────────────────
	settings := repository.NewSettingsRepository(database)
	if err := settings.SeedFromEnv(cfg); err != nil {
		log.Fatalf("seed settings from env: %v", err)
	}

	shows := repository.NewShowRepository(database, settings)
	requests := repository.NewUserRequestRepository(database)
	flags := repository.NewDiscrepancyFlagRepository(database)

	// ── External clients (from settings; fall back gracefully if unconfigured) ─
	sonarrURL := settings.GetWithDefault("sonarr_url", cfg.SonarrURL)
	sonarrKey := settings.GetWithDefault("sonarr_api_key", cfg.SonarrAPIKey)
	plexURL := settings.GetWithDefault("plex_url", cfg.PlexURL)
	plexToken := settings.GetWithDefault("plex_token", cfg.PlexToken)
	plexDBPath := settings.GetWithDefault("plex_db_path", cfg.PlexDBPath)

	sonarrClient := sonarr.NewClient(sonarrURL, sonarrKey)
	plexClient := plex.NewClient(plexURL, plexToken)

	var plexDB *plex.PlexDB
	if plexDBPath != "" {
		resolvedPath, err := plex.ResolvePlexDBPath(plexDBPath)
		if err != nil {
			log.Printf("warning: could not resolve Plex DB path %q: %v", plexDBPath, err)
		} else {
			pd, err := plex.OpenPlexDB(resolvedPath)
			if err != nil {
				log.Printf("warning: could not open Plex DB at %q: %v", resolvedPath, err)
			} else {
				log.Printf("Plex DB opened at %q", resolvedPath)
				plexDB = pd
				defer plexDB.Close()
			}
		}
	}

	// ── State engine ─────────────────────────────────────────────────────────
	engine := state.NewEngine(shows, requests, plexClient, plexDB)

	// ── SSE broker ───────────────────────────────────────────────────────────
	broker := api.NewBroker()

	// ── Job queue ────────────────────────────────────────────────────────────
	queue := scheduler.NewJobQueue()
	queue.SetNotify(broker.Notify)
	queue.Start()

	// ── Reconciler ───────────────────────────────────────────────────────────
	reconciler := reconcile.NewReconciler(shows, requests, flags, sonarrClient, plexClient, plexDB, engine)

	// ── Scheduler ────────────────────────────────────────────────────────────
	sched := scheduler.NewScheduler(reconciler, settings, queue)
	if err := sched.Start(); err != nil {
		log.Fatalf("start scheduler: %v", err)
	}
	defer sched.Stop()

	// Reconcile immediately on startup so dev restarts don't wait for the cron.
	queue.Enqueue(func() {
		if err := reconciler.ReconcileAll(); err != nil {
			log.Printf("[startup] ReconcileAll error: %v", err)
		}
	})

	// ── Proxy ────────────────────────────────────────────────────────────────
	interceptor := proxy.NewInterceptor(sonarrClient, shows, engine)
	interceptor.OnSeriesAdd = func(tvdbId int, requestedSeasons []int) {
		queue.Enqueue(func() {
			log.Printf("[proxy] onboard tvdb=%d: waiting for Sonarr to index series, requestedSeasons=%v", tvdbId, requestedSeasons)

			// Retry fetching from Sonarr — the series add is confirmed (2xx) but
			// Sonarr's internal indexing may not be complete immediately.
			var series *sonarr.Series
			for attempt := 1; attempt <= 10; attempt++ {
				time.Sleep(time.Duration(attempt) * time.Second)
				s, err := sonarrClient.GetSeriesByTVDB(tvdbId)
				if err == nil {
					series = s
					break
				}
				log.Printf("[proxy] onboard tvdb=%d: attempt %d/10: %v", tvdbId, attempt, err)
			}
			if series == nil {
				log.Printf("[proxy] onboard tvdb=%d: series never appeared in Sonarr, giving up", tvdbId)
				return
			}

			if err := shows.Upsert(repository.Show{
				TVDBId:    tvdbId,
				SonarrId:  series.ID,
				Title:     series.Title,
				PosterURL: series.PosterURL(),
				FanartURL: series.FanartURL(),
				Status:    "active",
			}); err != nil {
				log.Printf("[proxy] onboard tvdb=%d: upsert show: %v", tvdbId, err)
				return
			}

			// Persist the requested season on the most-recent user_request.
			// Pick the lowest monitored season = where they want to start.
			if len(requestedSeasons) > 0 {
				minSeason := requestedSeasons[0]
				for _, s := range requestedSeasons[1:] {
					if s < minSeason {
						minSeason = s
					}
				}
				if err := requests.SetRequestedSeason(tvdbId, minSeason); err != nil {
					log.Printf("[proxy] onboard tvdb=%d: set requested season: %v", tvdbId, err)
				} else {
					log.Printf("[proxy] onboard tvdb=%d: requested season set to %d", tvdbId, minSeason)
				}
			}

			log.Printf("[proxy] onboard tvdb=%d (%s) upserted, reconciling", tvdbId, series.Title)

			if err := reconciler.ReconcileShow(tvdbId); err != nil {
				log.Printf("[proxy] onboard tvdb=%d reconcile error: %v", tvdbId, err)
			}
		})
	}
	sonarrProxy := proxy.NewProxy(func() string {
		return settings.GetWithDefault("sonarr_url", cfg.SonarrURL)
	}, interceptor)

	// ── Webhook handler ──────────────────────────────────────────────────────
	apiToken := settings.GetWithDefault("rollarr_api_token", cfg.APIToken)
	webhookHandler := webhook.NewHandler(
		sonarrClient,
		plexClient,
		shows,
		requests,
		settings,
		queue.Enqueue,
		reconciler.ReconcileShow,
	)

	// ── API router ───────────────────────────────────────────────────────────
	apiRouter := api.NewRouter(shows, requests, flags, settings, reconciler, queue, apiToken, rollarr.FrontendFS, engine, sonarrClient, plexClient, broker)

	// ── Root router ──────────────────────────────────────────────────────────
	r := chi.NewRouter()

	// Webhooks (no Bearer auth — they use X-Webhook-Secret).
	r.Post("/webhooks/seerr", webhookHandler.ServeHTTP)
	r.Post("/webhooks/jellyseerr", webhookHandler.ServeHTTP)

	// Sonarr proxy — always registered; returns 503 with explanation if sonarr_url
	// is not yet configured in Rollarr settings.
	r.Handle("/sonarr-proxy/*", http.StripPrefix("/sonarr-proxy", sonarrProxy))
	r.Handle("/api/v3/*", sonarrProxy)

	// Admin API + SPA.
	r.Mount("/", apiRouter)

	addr := ":" + cfg.Port
	fmt.Fprintf(log.Writer(), "Rollarr starting on %s\n", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

