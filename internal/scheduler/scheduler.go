package scheduler

import (
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
)

// JobQueue serialises all background jobs through a single goroutine to prevent
// concurrent Sonarr/Plex races.
type JobQueue struct {
	ch       chan func()
	notifyFn func()
}

// SetNotify registers a function called after each job completes.
func (q *JobQueue) SetNotify(fn func()) {
	q.notifyFn = fn
}

// NewJobQueue constructs a JobQueue with a buffer of 256 pending jobs.
func NewJobQueue() *JobQueue {
	return &JobQueue{ch: make(chan func(), 256)}
}

// Start launches the worker goroutine. Call this once at startup.
func (q *JobQueue) Start() {
	go func() {
		for job := range q.ch {
			func() {
				start := time.Now()
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[queue] job panic after %s: %v", time.Since(start), r)
					}
				}()
				job()
				log.Printf("[queue] job completed in %s", time.Since(start))
				if q.notifyFn != nil {
					q.notifyFn()
				}
			}()
		}
	}()
}

// Enqueue adds a job to the queue. Non-blocking: if the queue is full the job
// is dropped, a warning is logged and false is returned so callers can undo
// any bookkeeping that assumed the job would run.
func (q *JobQueue) Enqueue(job func()) bool {
	select {
	case q.ch <- job:
		return true
	default:
		log.Printf("[scheduler] job queue full (%d pending) — dropping job", cap(q.ch))
		return false
	}
}

// Reconciler is the interface the Scheduler needs from the reconcile package.
// Declaring it here avoids an import cycle between scheduler and reconcile.
type Reconciler interface {
	ReconcileAll() error
	ReconcileShow(tvdbId int) error
	PruneInactive() error
}

// SettingsReader is the minimal interface needed to read scheduler settings.
type SettingsReader interface {
	GetInt(key string, def int) int
}

// Scheduler manages cron jobs for periodic reconciliation and inactivity pruning.
type Scheduler struct {
	cron       *cron.Cron
	reconciler Reconciler
	settings   SettingsReader
	queue      *JobQueue
}

// NewScheduler constructs a Scheduler.
func NewScheduler(reconciler Reconciler, settings SettingsReader, queue *JobQueue) *Scheduler {
	return &Scheduler{
		cron:       cron.New(),
		reconciler: reconciler,
		settings:   settings,
		queue:      queue,
	}
}

// Start registers cron jobs and starts the cron runner. Interval settings are
// read once at startup; a restart is required to pick up changes.
func (s *Scheduler) Start() error {
	reconcileMinutes := s.settings.GetInt("reconcile_interval_minutes", 15)
	inactivityMinutes := s.settings.GetInt("inactivity_interval_minutes", 60)

	reconcileSpec := minutesToCron(reconcileMinutes)
	inactivitySpec := minutesToCron(inactivityMinutes)

	reconcileAll := s.coalesced("ReconcileAll", s.reconciler.ReconcileAll)
	if _, err := s.cron.AddFunc(reconcileSpec, reconcileAll); err != nil {
		return fmt.Errorf("scheduler: add reconcile job: %w", err)
	}

	pruneInactive := s.coalesced("PruneInactive", s.reconciler.PruneInactive)
	if _, err := s.cron.AddFunc(inactivitySpec, pruneInactive); err != nil {
		return fmt.Errorf("scheduler: add inactivity job: %w", err)
	}

	s.cron.Start()
	log.Printf("[scheduler] reconcile every %d min, inactivity prune every %d min",
		reconcileMinutes, inactivityMinutes)
	return nil
}

// Stop gracefully shuts down the cron runner.
func (s *Scheduler) Stop() {
	s.cron.Stop()
}

// coalesced wraps a periodic job so at most one instance is queued or running.
// When a full pass takes longer than its interval, cron would otherwise keep
// queueing more full passes behind it, filling the queue and starving
// webhook-triggered reconciles.
func (s *Scheduler) coalesced(name string, job func() error) func() {
	var pending atomic.Bool
	return func() {
		if !pending.CompareAndSwap(false, true) {
			log.Printf("[scheduler] %s still queued or running — skipping this tick", name)
			return
		}
		queued := s.queue.Enqueue(func() {
			defer pending.Store(false)
			if err := job(); err != nil {
				log.Printf("[scheduler] %s error: %v", name, err)
			}
		})
		if !queued {
			pending.Store(false)
		}
	}
}

// minutesToCron converts a minute interval into a cron spec. "@every" runs at
// exactly the configured interval; the old */N form had to snap to divisors
// of 60 and truncated anything over an hour to whole hours (90 → 60).
func minutesToCron(minutes int) string {
	if minutes <= 0 {
		minutes = 15
	}
	return fmt.Sprintf("@every %dm", minutes)
}
