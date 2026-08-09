package webapp

import (
	"net/http"
	"strings"
)

const providerTelemetrySchema = "provider.ups.telemetry.v1"

type providerTelemetry struct {
	Schema     string         `json:"schema"`
	NodeID     string         `json:"nodeId"`
	Generation string         `json:"generation"`
	Sequence   uint64         `json:"sequence"`
	ObservedAt string         `json:"observedAt"`
	Health     string         `json:"health"`
	Input      providerInput   `json:"input"`
	Battery    providerBattery `json:"battery"`
	Output     providerOutput  `json:"output"`
	System     providerSystem  `json:"system"`
}

type providerInput struct {
	ACPresent bool `json:"acPresent"`
}

type providerBattery struct {
	LevelPercent int     `json:"levelPercent"`
	VoltageV     float64 `json:"voltageV"`
	CurrentMA    float64 `json:"currentMA"`
	PowerW       float64 `json:"powerW"`
	Direction    string  `json:"direction"`
}

type providerOutput struct {
	VoltageV  float64 `json:"voltageV"`
	CurrentMA float64 `json:"currentMA"`
	PowerW    float64 `json:"powerW"`
}

type providerSystem struct {
	CPUTemperatureC   float64 `json:"cpuTemperatureC"`
	MemoryUsedMB      float64 `json:"memoryUsedMB"`
	MemoryUsedPercent float64 `json:"memoryUsedPercent"`
}

func (server *Server) handleProviderTelemetry(w http.ResponseWriter, r *http.Request) {
	if server.config.ProviderToken == "" {
		http.NotFound(w, r)
		return
	}
	prefix := "Bearer "
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, prefix) ||
		!constantTimeEqual(strings.TrimPrefix(authorization, prefix), server.config.ProviderToken) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	status, ok := server.hub.Current()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "telemetry is initializing"})
		return
	}
	health := "ok"
	if status.Status != "ok" {
		health = "degraded"
	}
	writeJSON(w, http.StatusOK, providerTelemetry{
		Schema: providerTelemetrySchema, NodeID: server.config.NodeID,
		Generation: server.providerGeneration, Sequence: status.Sequence,
		ObservedAt: status.UpdatedAt, Health: health,
		Input: providerInput{ACPresent: status.AC},
		Battery: providerBattery{
			LevelPercent: status.BatteryPercent, VoltageV: status.VoltageV,
			CurrentMA: status.CurrentMA, PowerW: status.PowerW,
			Direction: providerBatteryDirection(status.BatteryDirection),
		},
		Output: providerOutput{
			VoltageV: status.OutputVoltageV, CurrentMA: status.OutputCurrentMA,
			PowerW: status.OutputPowerW,
		},
		System: providerSystem{
			CPUTemperatureC: status.CPUTemperatureC, MemoryUsedMB: status.RAMUsedMB,
			MemoryUsedPercent: status.RAMPercent,
		},
	})
}

func providerBatteryDirection(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "charging":
		return "charging"
	case "discharging":
		return "discharging"
	default:
		return "idle"
	}
}
