package wifi

import (
	"testing"
	"time"
)

func TestFallbackDecisions(t *testing.T) {
	policy := Policy{
		GracePeriod:           15 * time.Second,
		ActivationCooldown:    20 * time.Second,
		MaximumClientAttempts: 2,
	}

	tests := []struct {
		name        string
		observation Observation
		want        Action
	}{
		{
			name: "active hotspot is stable",
			observation: Observation{
				State: StateConnected, ActiveConnection: "ups-pi-node-hotspot",
				HotspotConnection: "ups-pi-node-hotspot",
			},
			want: ActionNone,
		},
		{
			name: "active client is stable",
			observation: Observation{
				State: StateConnected, ActiveConnection: "home",
				HotspotConnection: "ups-pi-node-hotspot",
			},
			want: ActionNone,
		},
		{
			name:        "activation is allowed to finish",
			observation: Observation{State: StateConnecting},
			want:        ActionWait,
		},
		{
			name: "manual connection suppresses fallback",
			observation: Observation{
				State: StateDisconnected, ManualConnectInProgress: true,
				DisconnectedFor: time.Minute,
			},
			want: ActionWait,
		},
		{
			name: "grace period precedes profile retry",
			observation: Observation{
				State: StateDisconnected, DisconnectedFor: 14 * time.Second,
				KnownClientProfiles: 1,
			},
			want: ActionWait,
		},
		{
			name: "known client is tried before hotspot",
			observation: Observation{
				State: StateDisconnected, DisconnectedFor: time.Minute,
				KnownClientProfiles: 1, KnownClientAttempts: 1,
			},
			want: ActionActivateKnown,
		},
		{
			name: "recent activation is not repeated",
			observation: Observation{
				State: StateDisconnected, DisconnectedFor: time.Minute,
				LastAction: ActionActivateHotspot, LastActionAgo: 10 * time.Second,
			},
			want: ActionWait,
		},
		{
			name: "hotspot follows exhausted client retries",
			observation: Observation{
				State: StateDisconnected, DisconnectedFor: time.Minute,
				KnownClientProfiles: 2, KnownClientAttempts: 2,
			},
			want: ActionActivateHotspot,
		},
		{
			name:        "unavailable device is not activated",
			observation: Observation{State: StateUnavailable},
			want:        ActionReportState,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := policy.Decide(test.observation)
			if decision.Action != test.want {
				t.Fatalf("Decide() action = %q, want %q (reason: %s)", decision.Action, test.want, decision.Reason)
			}
		})
	}
}
