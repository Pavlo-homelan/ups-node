// Package wifi contains policy that is independent from the NetworkManager
// transport. Keeping the policy pure makes every fallback transition testable
// without changing the host network.
package wifi

import "time"

// DeviceState is the subset of NetworkManager device states relevant to Wi-Fi
// fallback policy.
type DeviceState string

const (
	StateUnknown      DeviceState = "unknown"
	StateUnavailable  DeviceState = "unavailable"
	StateDisconnected DeviceState = "disconnected"
	StateConnecting   DeviceState = "connecting"
	StateConnected    DeviceState = "connected"
)

// Action is a single idempotent operation selected by the fallback policy.
type Action string

const (
	ActionNone            Action = "none"
	ActionWait            Action = "wait"
	ActionActivateKnown   Action = "activate-known-client"
	ActionActivateHotspot Action = "activate-hotspot"
	ActionReportState     Action = "report-state"
)

// Observation describes current NetworkManager state and recent actions. The
// D-Bus adapter is responsible for producing this snapshot atomically enough
// for one reconciliation pass.
type Observation struct {
	State                   DeviceState
	ActiveConnection        string
	HotspotConnection       string
	ManualConnectInProgress bool
	DisconnectedFor         time.Duration
	KnownClientProfiles     int
	KnownClientAttempts     int
	LastAction              Action
	LastActionAgo           time.Duration
}

// Policy contains timing and retry limits. Zero values intentionally result in
// immediate fallback, which keeps tests and explicit recovery commands simple.
type Policy struct {
	GracePeriod           time.Duration
	ActivationCooldown    time.Duration
	MaximumClientAttempts int
}

// Decision explains the single operation a controller may execute. Reason is
// suitable for structured diagnostics but must not contain network secrets.
type Decision struct {
	Action Action
	Reason string
}

// Decide returns at most one operation and never mutates host state.
func (policy Policy) Decide(observation Observation) Decision {
	switch observation.State {
	case StateConnected:
		if observation.ActiveConnection == observation.HotspotConnection {
			return Decision{Action: ActionNone, Reason: "hotspot already active"}
		}
		return Decision{Action: ActionNone, Reason: "client connection already active"}
	case StateConnecting:
		return Decision{Action: ActionWait, Reason: "network transition in progress"}
	case StateUnavailable, StateUnknown:
		return Decision{Action: ActionReportState, Reason: "Wi-Fi device is not ready"}
	case StateDisconnected:
		// Continue below.
	default:
		return Decision{Action: ActionReportState, Reason: "unsupported Wi-Fi device state"}
	}

	if observation.ManualConnectInProgress {
		return Decision{Action: ActionWait, Reason: "manual connection in progress"}
	}
	if observation.DisconnectedFor < policy.GracePeriod {
		return Decision{Action: ActionWait, Reason: "waiting for reconnect grace period"}
	}
	if (observation.LastAction == ActionActivateKnown || observation.LastAction == ActionActivateHotspot) &&
		observation.LastActionAgo < policy.ActivationCooldown {
		return Decision{Action: ActionWait, Reason: "waiting for prior activation"}
	}
	if observation.KnownClientProfiles > 0 &&
		observation.KnownClientAttempts < policy.MaximumClientAttempts {
		return Decision{Action: ActionActivateKnown, Reason: "trying a known client profile"}
	}
	return Decision{Action: ActionActivateHotspot, Reason: "client recovery exhausted"}
}
