package webhook

import (
	"encoding/json"
	"testing"
)

func TestPayloadSeason(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"custom template field", `{"request":{"season":3}}`, 3},
		{"overseerr extra list", `{"extra":[{"name":"Requested Seasons","value":"4, 2, 5"}]}`, 2},
		{"field wins over extra", `{"request":{"season":1},"extra":[{"name":"Requested Seasons","value":"3"}]}`, 1},
		{"unrelated extra", `{"extra":[{"name":"Notes","value":"7"}]}`, 0},
		{"no season at all", `{"media":{"tvdbId":"123"}}`, 0},
	}
	for _, tc := range cases {
		var p SeerrPayload
		if err := json.Unmarshal([]byte(tc.body), &p); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := payloadSeason(p); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
