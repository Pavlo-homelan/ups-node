package webapp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

type fakeHelper struct {
	mu      sync.Mutex
	actions []string
	calls   []helperCall
	errors  map[string]error
}

type helperCall struct {
	action string
	params map[string]any
}

func (helper *fakeHelper) Call(_ context.Context, action string, params map[string]any, target any) error {
	helper.mu.Lock()
	helper.actions = append(helper.actions, action)
	helper.calls = append(helper.calls, helperCall{action: action, params: cloneParams(params)})
	err := helper.errors[action]
	helper.mu.Unlock()
	if err != nil {
		return err
	}
	var value any
	switch action {
	case "ups.status":
		value = map[string]any{
			"backend": "ina3221", "v": 12.1, "current_ma": 149.0, "i": 149.0,
			"power_w": 1.8, "output_channel": 2, "output_voltage_v": 18.96,
			"output_current_ma": 757.0, "output_power_w": 14.35, "state": "CHARGE",
			"color": "yellow", "ac": true, "percent": 84, "battery_status": "High",
			"battery_direction": "Charging", "load_source": "UPS output to load",
			"battery_route": "Battery routed to charger",
		}
	case "wifi.status":
		value = map[string]any{
			"portal_mode": "client", "state": "connected", "connected_ssid": "MyRouter",
			"ip_address": "192.0.2.40",
		}
	case "auth.pam":
		value = map[string]any{"authenticated": true}
	case "wifi.scan":
		value = map[string]any{"networks": []any{map[string]any{"ssid": "MyRouter", "signal": 88}}}
	case "wifi.connect":
		value = map[string]any{"message": "started"}
	default:
		return fmt.Errorf("unexpected action %s", action)
	}
	encoded, _ := json.Marshal(value)
	return json.Unmarshal(encoded, target)
}

func cloneParams(params map[string]any) map[string]any {
	copy := map[string]any{}
	for key, value := range params {
		copy[key] = value
	}
	return copy
}

func TestHubCombinesUPSAndWiFiIntoOneSSESnapshot(t *testing.T) {
	config := secureTestConfig()
	helper := &fakeHelper{}
	hub := newStatusHub(config, helper)
	hub.refresh(context.Background())
	status, ok := hub.Current()
	if !ok || status.Status != "ok" || status.Sequence != 1 {
		t.Fatalf("status = %#v, ok=%v", status, ok)
	}
	if status.VoltageV != 12.1 || status.OutputVoltageV != 18.96 || status.OutputChannel != 2 {
		t.Fatalf("power fields = %#v", status)
	}
	if status.ConnectedSSID != "MyRouter" || status.IPAddress != "192.0.2.40" {
		t.Fatalf("network fields = %#v", status)
	}
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	event := <-events
	if event.Sequence != 1 || event.Status.UPSState != "CHARGE" {
		t.Fatalf("event = %#v", event)
	}
}

func TestHubPublishesDegradedSnapshotInsteadOfDroppingStream(t *testing.T) {
	helper := &fakeHelper{errors: map[string]error{"wifi.status": fmt.Errorf("offline")}}
	hub := newStatusHub(secureTestConfig(), helper)
	hub.refresh(context.Background())
	status, _ := hub.Current()
	if status.Status != "degraded" || status.CollectionError != "Wi-Fi: offline" || status.VoltageV != 12.1 {
		t.Fatalf("degraded status = %#v", status)
	}
}
