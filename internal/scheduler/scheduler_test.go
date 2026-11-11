package scheduler

import (
	"testing"

	"github.com/robfig/cron/v3"
)

func TestCoalescedSkipsWhileQueued(t *testing.T) {
	q := NewJobQueue() // not started: jobs stay queued
	s := &Scheduler{queue: q}
	runs := 0
	tick := s.coalesced("test", func() error { runs++; return nil })

	tick()
	tick() // previous run still queued → skipped
	if got := len(q.ch); got != 1 {
		t.Fatalf("queued jobs = %d, want 1", got)
	}

	(<-q.ch)() // run it; clears the pending flag
	tick()
	if got := len(q.ch); got != 1 {
		t.Fatalf("after run, queued jobs = %d, want 1", got)
	}
	if runs != 1 {
		t.Fatalf("runs = %d, want 1", runs)
	}
}

func TestMinutesToCronParses(t *testing.T) {
	for _, m := range []int{0, 1, 15, 90, 1440} {
		spec := minutesToCron(m)
		if _, err := cron.ParseStandard(spec); err != nil {
			t.Errorf("minutesToCron(%d) = %q: %v", m, spec, err)
		}
	}
}
