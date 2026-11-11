package api

import (
	"reflect"
	"testing"
)

func TestValidateSettingsDropsInternalAndSentinelKeys(t *testing.T) {
	got, err := validateSettings(map[string]string{
		"stat_bytes_deleted":     "0",            // internal counter: never writable
		"schema_version":         "1",            // internal
		"plex_token":             secretSentinel, // unchanged secret
		"sonarr_url":             "http://sonarr:8989",
		"global_inactivity_days": " 30 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"sonarr_url": "http://sonarr:8989", "global_inactivity_days": "30"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestValidateSettingsRejectsUnsafeNumbers(t *testing.T) {
	for _, tc := range []map[string]string{
		{"global_inactivity_days": "0"},
		{"global_buffer_size": "-1"},
		{"reconcile_interval_minutes": "abc"},
	} {
		if _, err := validateSettings(tc); err == nil {
			t.Errorf("%v: expected error", tc)
		}
	}
}
