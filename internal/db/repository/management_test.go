package repository_test

import (
	"testing"
	"time"

	"github.com/kiwi3007/rollarr/internal/db"
	"github.com/kiwi3007/rollarr/internal/db/repository"
)

func TestShouldManage(t *testing.T) {
	excluded := map[string]bool{"111": true}
	seerr := func(user string) repository.UserRequest {
		return repository.UserRequest{PlexUserID: user, RequestedSeason: 1}
	}
	discovered := func(user string) repository.UserRequest {
		return repository.UserRequest{PlexUserID: user, RequestedSeason: 0}
	}

	tests := []struct {
		name string
		reqs []repository.UserRequest
		want bool
	}{
		{"no requests", nil, true},
		{"only excluded requester", []repository.UserRequest{seerr("111")}, false},
		{"managed requester", []repository.UserRequest{seerr("222")}, true},
		{"excluded and managed requesters", []repository.UserRequest{seerr("111"), seerr("222")}, true},
		{"excluded requester plus discovered managed watcher", []repository.UserRequest{seerr("111"), discovered("222")}, false},
		{"only discovered watchers", []repository.UserRequest{discovered("222")}, true},
		{"unresolved seerr requester", []repository.UserRequest{seerr("seerr:bob")}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repository.ShouldManage(tt.reqs, excluded); got != tt.want {
				t.Errorf("ShouldManage = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShowUpsertPreservesManagedFlag(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	shows := repository.NewShowRepository(database, repository.NewSettingsRepository(database))

	if err := shows.Upsert(repository.Show{TVDBId: 1, SonarrId: 10, Title: "A", Status: "active", Unmanaged: true}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// A later metadata refresh with a zero-value Show must not re-manage it.
	if err := shows.Upsert(repository.Show{TVDBId: 1, SonarrId: 10, Title: "A (renamed)", Status: "active"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := shows.FindByTVDB(1)
	if err != nil || got == nil {
		t.Fatalf("find: %v", err)
	}
	if !got.Unmanaged {
		t.Error("Upsert flipped an unmanaged show to managed")
	}
	if got.Title != "A (renamed)" {
		t.Errorf("title = %q, want update applied", got.Title)
	}
}

func TestShowUpsertPreservesOverrides(t *testing.T) {
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.RunMigrations(database); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	shows := repository.NewShowRepository(database, repository.NewSettingsRepository(database))

	buf, inact := 5, 60
	seen := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := shows.Upsert(repository.Show{
		TVDBId: 1, SonarrId: 10, Title: "A", Status: "inactive",
		CustomBufferSize: &buf, CustomInactivityDays: &inact, LastActivityAt: &seen,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// Re-request: webhook upserts Sonarr metadata only.
	if err := shows.Upsert(repository.Show{TVDBId: 1, SonarrId: 10, Title: "A", Status: "active"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := shows.FindByTVDB(1)
	if err != nil || got == nil {
		t.Fatalf("find: %v", err)
	}
	if got.CustomBufferSize == nil || *got.CustomBufferSize != buf {
		t.Errorf("custom_buffer_size = %v, want %d", got.CustomBufferSize, buf)
	}
	if got.CustomInactivityDays == nil || *got.CustomInactivityDays != inact {
		t.Errorf("custom_inactivity_days = %v, want %d", got.CustomInactivityDays, inact)
	}
	if got.LastActivityAt == nil || !got.LastActivityAt.Equal(seen) {
		t.Errorf("last_activity_at = %v, want %v", got.LastActivityAt, seen)
	}
	if got.Status != "active" {
		t.Errorf("status = %q, want re-request to reactivate", got.Status)
	}
}
