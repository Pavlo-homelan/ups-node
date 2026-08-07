package webapp

import (
	"fmt"
	"math"
	"net/http"
	"strings"
)

type metricDefinition struct {
	Name          string `json:"name"`
	Unit          string `json:"unit"`
	Kind          string `json:"kind"`
	HAPlatform    string `json:"ha_platform,omitempty"`
	HADeviceClass string `json:"ha_device_class,omitempty"`
	HAStateClass  string `json:"ha_state_class,omitempty"`
}

type metricEntry struct {
	Key        string
	Definition metricDefinition
}

var metricDefinitions = []metricEntry{
	{"app.health", metricDefinition{"Application health", "", "numeric", "binary_sensor", "connectivity", ""}},
	{"ups.ac_present", metricDefinition{"AC present", "", "numeric", "binary_sensor", "power", ""}},
	{"ups.battery.percent", metricDefinition{"Battery percent", "%", "numeric", "sensor", "battery", "measurement"}},
	{"ups.battery.status_code", metricDefinition{"Battery status code", "", "numeric", "sensor", "", ""}},
	{"ups.battery.charging", metricDefinition{"Battery charging", "", "numeric", "binary_sensor", "battery_charging", ""}},
	{"ups.battery.discharging", metricDefinition{"Battery discharging", "", "numeric", "binary_sensor", "", ""}},
	{"ups.voltage.v", metricDefinition{"UPS voltage", "V", "numeric", "sensor", "voltage", "measurement"}},
	{"ups.current.ma", metricDefinition{"UPS current", "mA", "numeric", "sensor", "current", "measurement"}},
	{"ups.current.abs_ma", metricDefinition{"UPS absolute current", "mA", "numeric", "sensor", "current", "measurement"}},
	{"ups.power.w", metricDefinition{"UPS power", "W", "numeric", "sensor", "power", "measurement"}},
	{"ups.output.voltage_v", metricDefinition{"Output voltage", "V", "numeric", "sensor", "voltage", "measurement"}},
	{"ups.output.current_ma", metricDefinition{"Output current", "mA", "numeric", "sensor", "current", "measurement"}},
	{"ups.output.power_w", metricDefinition{"Output power", "W", "numeric", "sensor", "power", "measurement"}},
	{"system.cpu.temp_c", metricDefinition{"CPU temperature", "C", "numeric", "sensor", "temperature", "measurement"}},
	{"system.ram.used_mb", metricDefinition{"RAM used", "MB", "numeric", "sensor", "", "measurement"}},
	{"system.ram.percent", metricDefinition{"RAM percent", "%", "numeric", "sensor", "", "measurement"}},
}

var batteryStatusCodes = map[string]int{
	"Critical": 0,
	"Low":      1,
	"Medium":   2,
	"High":     3,
	"Full":     4,
}

func (server *Server) integrationsAllowed(r *http.Request) bool {
	config := server.configSnapshot()
	if config.IntegrationsToken == "" {
		return true
	}
	supplied := r.Header.Get("X-UPS-PI-NODE-Token")
	if supplied == "" {
		supplied = r.URL.Query().Get("token")
	}
	return supplied != "" && constantTimeEqual(supplied, config.IntegrationsToken)
}

func (server *Server) integrationStatus(w http.ResponseWriter, r *http.Request) (LiveStatus, bool) {
	if !server.integrationsAllowed(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return LiveStatus{}, false
	}
	status, ok := server.hub.Current()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "status is initializing"})
		return LiveStatus{}, false
	}
	return status, true
}

func (server *Server) handleIntegrationMetrics(w http.ResponseWriter, r *http.Request) {
	status, ok := server.integrationStatus(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, buildMetricsPayload(status, server.configSnapshot().NodeID))
}

func (server *Server) handleZabbixMetrics(w http.ResponseWriter, r *http.Request) {
	status, ok := server.integrationStatus(w, r)
	if !ok {
		return
	}
	payload := buildMetricsPayload(status, server.configSnapshot().NodeID)
	writeJSON(w, http.StatusOK, map[string]any{
		"schema":  payload["schema"],
		"node":    payload["node"],
		"metrics": payload["metrics"],
	})
}

func (server *Server) handleZabbixDiscovery(w http.ResponseWriter, r *http.Request) {
	if !server.integrationsAllowed(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	writeJSON(w, http.StatusOK, buildZabbixDiscoveryPayload())
}

func (server *Server) handleZabbixMetric(w http.ResponseWriter, r *http.Request) {
	status, ok := server.integrationStatus(w, r)
	if !ok {
		return
	}
	metrics := buildMetricValues(status)
	value, exists := metricValueAsText(metrics, r.PathValue("metric"))
	if !exists {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintln(w, value)
}

func (server *Server) handleHomeAssistantDiscovery(w http.ResponseWriter, r *http.Request) {
	if !server.integrationsAllowed(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	writeJSON(w, http.StatusOK, buildHomeAssistantPayload(server.configSnapshot().NodeID))
}

func buildMetricsPayload(status LiveStatus, nodeID string) map[string]any {
	return map[string]any{
		"schema":      "ups-pi-node.metrics.v1",
		"node":        nodeID,
		"metrics":     buildMetricValues(status),
		"definitions": buildMetricDefinitions(),
	}
}

func buildMetricValues(status LiveStatus) map[string]any {
	health := 1
	if status.Status != "ok" {
		health = 0
	}
	return map[string]any{
		"app.health":              health,
		"ups.ac_present":          boolInt(status.AC),
		"ups.battery.percent":     status.BatteryPercent,
		"ups.battery.status_code": batteryStatusCode(status.BatteryStatus),
		"ups.battery.charging":    boolInt(status.BatteryDirection == "Charging"),
		"ups.battery.discharging": boolInt(status.BatteryDirection == "Discharging"),
		"ups.voltage.v":           round(status.VoltageV, 3),
		"ups.current.ma":          round(status.CurrentMA, 1),
		"ups.current.abs_ma":      round(math.Abs(status.CurrentMA), 1),
		"ups.power.w":             round(status.PowerW, 3),
		"ups.output.voltage_v":    round(status.OutputVoltageV, 3),
		"ups.output.current_ma":   round(status.OutputCurrentMA, 1),
		"ups.output.power_w":      round(status.OutputPowerW, 3),
		"system.cpu.temp_c":       round(status.CPUTemperatureC, 1),
		"system.ram.used_mb":      round(status.RAMUsedMB, 1),
		"system.ram.percent":      round(status.RAMPercent, 1),
	}
}

func batteryStatusCode(status string) int {
	code, ok := batteryStatusCodes[strings.TrimSpace(status)]
	if !ok {
		return -1
	}
	return code
}

func buildMetricDefinitions() map[string]metricDefinition {
	definitions := make(map[string]metricDefinition, len(metricDefinitions))
	for _, entry := range metricDefinitions {
		definitions[entry.Key] = entry.Definition
	}
	return definitions
}

func buildZabbixDiscoveryPayload() map[string]any {
	data := make([]map[string]string, 0, len(metricDefinitions))
	for _, entry := range metricDefinitions {
		data = append(data, map[string]string{
			"{#METRIC_KEY}":  entry.Key,
			"{#METRIC_NAME}": entry.Definition.Name,
			"{#METRIC_UNIT}": entry.Definition.Unit,
			"{#METRIC_KIND}": entry.Definition.Kind,
		})
	}
	return map[string]any{"data": data}
}

func buildHomeAssistantPayload(nodeID string) map[string]any {
	stateTopic := nodeID + "/state"
	availabilityTopic := nodeID + "/availability"
	device := map[string]any{
		"identifiers":  []string{nodeID},
		"name":         nodeID,
		"manufacturer": "ups-pi-node",
		"model":        "UPS node",
	}
	discovery := make([]map[string]any, 0, len(metricDefinitions))
	for _, entry := range metricDefinitions {
		definition := entry.Definition
		platform := definition.HAPlatform
		if platform == "" {
			platform = "sensor"
		}
		objectID := strings.ReplaceAll(entry.Key, ".", "_")
		payload := map[string]any{
			"name":                  definition.Name,
			"unique_id":             nodeID + "_" + objectID,
			"state_topic":           stateTopic,
			"availability_topic":    availabilityTopic,
			"payload_available":     "online",
			"payload_not_available": "offline",
			"value_template":        "{{ value_json.metrics['" + entry.Key + "'] }}",
			"device":                device,
		}
		if definition.Unit != "" {
			payload["unit_of_measurement"] = definition.Unit
		}
		if definition.HADeviceClass != "" {
			payload["device_class"] = definition.HADeviceClass
		}
		if definition.HAStateClass != "" {
			payload["state_class"] = definition.HAStateClass
		}
		if platform == "binary_sensor" {
			payload["payload_on"] = "1"
			payload["payload_off"] = "0"
		}
		discovery = append(discovery, map[string]any{
			"platform":  platform,
			"object_id": objectID,
			"topic":     "homeassistant/" + platform + "/" + nodeID + "/" + objectID + "/config",
			"payload":   payload,
		})
	}
	return map[string]any{
		"schema":             "ups-pi-node.home-assistant.v1",
		"node":               nodeID,
		"state_topic":        stateTopic,
		"availability_topic": availabilityTopic,
		"discovery":          discovery,
	}
}

func metricValueAsText(metrics map[string]any, key string) (string, bool) {
	value, ok := metrics[key]
	if !ok {
		return "", false
	}
	if value == nil {
		return "", true
	}
	return fmt.Sprint(value), true
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func round(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}

func constantTimeEqual(left string, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var result byte
	for index := range left {
		result |= left[index] ^ right[index]
	}
	return result == 0
}
