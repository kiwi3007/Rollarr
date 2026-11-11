package plex

import "testing"

func TestTvdbFromGuids(t *testing.T) {
	cases := []struct {
		name    string
		primary string
		guids   []string
		want    int
	}{
		{"modern agent", "plex://show/5d9c", []string{"imdb://tt123", "tvdb://121361"}, 121361},
		{"legacy agent", "com.plexapp.agents.thetvdb://121361?lang=en", nil, 121361},
		// The old substring match found tvdb 12 inside "…thetvdb://123".
		{"no prefix match", "com.plexapp.agents.thetvdb://123?lang=en", nil, 123},
		{"no tvdb guid", "plex://show/5d9c", []string{"imdb://tt123"}, 0},
		{"garbage id", "", []string{"tvdb://abc"}, 0},
	}
	for _, tc := range cases {
		if got := tvdbFromGuids(tc.primary, tc.guids); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
