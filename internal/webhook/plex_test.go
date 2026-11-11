package webhook

import "testing"

// The Plex webhook is account-scoped: plays the account (or a Plex Home user
// under it) makes on somebody else's server are delivered to us identically to
// local plays. Acting on those dereferences a foreign, server-local ratingKey
// against our PMS and can reconcile — or reactivate — an unrelated show.
func TestIsLocalPlayback(t *testing.T) {
	const localID = "abc123localserver"

	payload := func(serverUUID string, owner, user bool) plexPayload {
		var p plexPayload
		p.Event = "media.scrobble"
		p.Owner = owner
		p.User = user
		p.Server.UUID = serverUUID
		return p
	}

	tests := []struct {
		name    string
		payload plexPayload
		localID string
		want    bool
	}{
		{
			name:    "owner watching on own server",
			payload: payload(localID, true, true),
			localID: localID,
			want:    true,
		},
		{
			// The case that must keep working: a shared/managed user's play on
			// our server arrives with owner=true, user=false. The gate is
			// server-scoped, so it passes.
			name:    "other user watching on our server",
			payload: payload(localID, true, false),
			localID: localID,
			want:    true,
		},
		{
			// The bug being fixed: our account plays on a friend's server.
			name:    "our account watching on a foreign server",
			payload: payload("deadbeefremoteserver", false, true),
			localID: localID,
			want:    false,
		},
		{
			name:    "unknown local identifier fails closed",
			payload: payload(localID, true, true),
			localID: "",
			want:    false,
		},
		{
			name:    "payload without a server uuid fails closed",
			payload: payload("", true, true),
			localID: localID,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLocalPlayback(tt.payload, tt.localID); got != tt.want {
				t.Errorf("isLocalPlayback() = %v, want %v", got, tt.want)
			}
		})
	}
}
