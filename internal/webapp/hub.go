package webapp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ups-pi-node/internal/helperclient"
)

type LiveStatus struct {
	Sequence         uint64  `json:"sequence"`
	UpdatedAt        string  `json:"updated_at"`
	Status           string  `json:"status"`
	Backend          string  `json:"backend"`
	VoltageV         float64 `json:"v"`
	CurrentMA        float64 `json:"current_ma"`
	CurrentAbsMA     float64 `json:"i"`
	PowerW           float64 `json:"power_w"`
	OutputChannel    int     `json:"output_channel"`
	OutputVoltageV   float64 `json:"output_v"`
	OutputCurrentMA  float64 `json:"output_i"`
	OutputPowerW     float64 `json:"output_w"`
	UPSState         string  `json:"state"`
	Color            string  `json:"color"`
	AC               bool    `json:"ac"`
	BatteryPercent   int     `json:"percent"`
	BatteryStatus    string  `json:"battery_status"`
	BatteryDirection string  `json:"battery_direction"`
	LoadSource       string  `json:"load_source"`
	BatteryRoute     string  `json:"battery_route"`
	PortalMode       string  `json:"portal_mode"`
	WiFiState        string  `json:"wifi_state"`
	ConnectedSSID    string  `json:"connected_ssid"`
	IPAddress        string  `json:"ip_address"`
	CPUTemperatureC  float64 `json:"cpu_temp"`
	RAMUsedMB        float64 `json:"ram_used"`
	RAMPercent       float64 `json:"ram_percent"`
	HardwareError    string  `json:"hardware_error,omitempty"`
	DisplayError     string  `json:"display_error,omitempty"`
	CollectionError  string  `json:"collection_error,omitempty"`
}

type StatusEvent struct {
	Sequence uint64
	Status   LiveStatus
}

type statusHub struct {
	config Config
	helper helperclient.Caller

	mu          sync.RWMutex
	current     LiveStatus
	hasCurrent  bool
	subscribers map[chan StatusEvent]struct{}
	sequence    atomic.Uint64
}

func newStatusHub(config Config, helper helperclient.Caller) *statusHub {
	return &statusHub{config: config, helper: helper, subscribers: map[chan StatusEvent]struct{}{}}
}

func (hub *statusHub) Run(ctx context.Context) {
	hub.refresh(ctx)
	ticker := time.NewTicker(hub.config.StatusInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hub.refresh(ctx)
		}
	}
}

func (hub *statusHub) Current() (LiveStatus, bool) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return hub.current, hub.hasCurrent
}

func (hub *statusHub) Subscribe() (<-chan StatusEvent, func()) {
	channel := make(chan StatusEvent, 1)
	hub.mu.Lock()
	hub.subscribers[channel] = struct{}{}
	if hub.hasCurrent {
		channel <- StatusEvent{Sequence: hub.current.Sequence, Status: hub.current}
	}
	hub.mu.Unlock()
	return channel, func() {
		hub.mu.Lock()
		if _, ok := hub.subscribers[channel]; ok {
			delete(hub.subscribers, channel)
			close(channel)
		}
		hub.mu.Unlock()
	}
}

func (hub *statusHub) refresh(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	status := hub.collect(ctx)
	status.Sequence = hub.sequence.Add(1)
	status.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	hub.mu.Lock()
	hub.current = status
	hub.hasCurrent = true
	event := StatusEvent{Sequence: status.Sequence, Status: status}
	for subscriber := range hub.subscribers {
		select {
		case subscriber <- event:
		default:
			select {
			case <-subscriber:
			default:
			}
			subscriber <- event
		}
	}
	hub.mu.Unlock()
}

func (hub *statusHub) collect(ctx context.Context) LiveStatus {
	var ups map[string]any
	var wifi map[string]any
	var upsErr error
	var wifiErr error
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		upsErr = hub.helper.Call(ctx, "ups.status", nil, &ups)
	}()
	go func() {
		defer group.Done()
		wifiErr = hub.helper.Call(ctx, "wifi.status", map[string]any{
			"interface":               hub.config.WiFiInterface,
			"hotspot_connection_name": hub.config.HotspotConnection,
			"portal_mode":             hub.config.PortalMode,
		}, &wifi)
	}()
	group.Wait()

	status := LiveStatus{
		Status: "ok", Backend: stringField(ups, "backend"),
		VoltageV: numberField(ups, "v"), CurrentMA: numberField(ups, "current_ma"),
		CurrentAbsMA: numberField(ups, "i"), PowerW: numberField(ups, "power_w"),
		OutputChannel:   int(numberField(ups, "output_channel")),
		OutputVoltageV:  numberField(ups, "output_voltage_v"),
		OutputCurrentMA: numberField(ups, "output_current_ma"),
		OutputPowerW:    numberField(ups, "output_power_w"),
		UPSState:        stringField(ups, "state"), Color: stringField(ups, "color"),
		AC: boolField(ups, "ac"), BatteryPercent: int(numberField(ups, "percent")),
		BatteryStatus:    stringField(ups, "battery_status"),
		BatteryDirection: stringField(ups, "battery_direction"),
		LoadSource:       stringField(ups, "load_source"), BatteryRoute: stringField(ups, "battery_route"),
		PortalMode: stringField(wifi, "portal_mode"), WiFiState: stringField(wifi, "state"),
		ConnectedSSID: stringField(wifi, "connected_ssid"), IPAddress: stringField(wifi, "ip_address"),
		HardwareError: stringField(ups, "hardware_error"), DisplayError: stringField(ups, "display_error"),
	}
	status.CPUTemperatureC = cpuTemperature("/sys/class/thermal/thermal_zone0/temp")
	status.RAMUsedMB, status.RAMPercent = memoryUsage("/proc/meminfo")
	if upsErr != nil || wifiErr != nil {
		status.Status = "degraded"
		status.CollectionError = collectionError(upsErr, wifiErr)
	}
	return status
}

func stringField(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}

func numberField(values map[string]any, key string) float64 {
	switch value := values[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case jsonNumber:
		parsed, _ := strconv.ParseFloat(string(value), 64)
		return parsed
	default:
		return 0
	}
}

type jsonNumber string

func boolField(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func collectionError(upsErr error, wifiErr error) string {
	parts := make([]string, 0, 2)
	if upsErr != nil {
		parts = append(parts, "UPS: "+upsErr.Error())
	}
	if wifiErr != nil {
		parts = append(parts, "Wi-Fi: "+wifiErr.Error())
	}
	return strings.Join(parts, "; ")
}

func cpuTemperature(path string) float64 {
	contents, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(contents)), 64)
	if err != nil {
		return 0
	}
	return value / 1000
}

func memoryUsage(path string) (float64, float64) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	values := map[string]float64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		value, parseErr := strconv.ParseFloat(fields[0], 64)
		if parseErr == nil {
			values[key] = value
		}
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total <= 0 || available < 0 {
		return 0, 0
	}
	used := total - available
	if used < 0 {
		used = 0
	}
	return used / 1024, used / total * 100
}

func (status LiveStatus) String() string {
	return fmt.Sprintf("%s %.2fV %.0fmA", status.UPSState, status.VoltageV, status.CurrentMA)
}
