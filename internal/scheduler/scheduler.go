package scheduler

import (
	"fmt"
	"log"
	"time"

	"github.com/robfig/cron/v3"
)


// JobQueue serialises all background jobs through a single goroutine to prevent
// concurrent Sonarr/Plex races.
type JobQueue struct {
	ch chan func()
}

// NewJobQueue constructs a JobQueue with a buffer of 64 pending jobs.
func NewJobQueue() *JobQueue {
	return &JobQueue{ch: make(chan func(), 64)}
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
			}()
		}
	}()
}

// Enqueue adds a job to the queue. Non-blocking: if the queue is full the job
// is dropped and a warning is logged.
func (q *JobQueue) Enqueue(job func()) {
	select {
	case q.ch <- job:
	default:
		log.Printf("[scheduler] job queue full — dropping job")
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

	if _, err := s.cron.AddFunc(reconcileSpec, func() {
		s.queue.Enqueue(func() {
			if err := s.reconciler.ReconcileAll(); err != nil {
				log.Printf("[scheduler] ReconcileAll error: %v", err)
			}
		})
	}); err != nil {
		return fmt.Errorf("scheduler: add reconcile job: %w", err)
	}

	if _, err := s.cron.AddFunc(inactivitySpec, func() {
		s.queue.Enqueue(func() {
			if err := s.reconciler.PruneInactive(); err != nil {
				log.Printf("[scheduler] PruneInactive error: %v", err)
			}
		})
	}); err != nil {
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

// minutesToCron converts a minute interval into a cron expression.
// It snaps to the nearest valid cron stride (1, 2, 3, 5, 6, 10, 15, 20, 30).
func minutesToCron(minutes int) string {
	if minutes <= 0 {
		minutes = 15
	}
	// For intervals >= 60, run hourly.
	if minutes >= 60 {
		hours := minutes / 60
		return fmt.Sprintf("0 */%d * * *", hours)
	}
	// Snap to valid divisors of 60.
	valid := []int{1, 2, 3, 5, 6, 10, 15, 20, 30}
	snapped := valid[0]
	for _, v := range valid {
		if v <= minutes {
			snapped = v
		}
	}
	return fmt.Sprintf("*/%d * * * *", snapped)
}

// RunAt is a helper that schedules a one-shot job after a delay.
func RunAt(delay time.Duration, job func()) {
	go func() {
		time.Sleep(delay)
		job()
	}()
}
