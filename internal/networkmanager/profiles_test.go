package networkmanager

import "testing"

func TestManagedConnectionNameMatchesPythonHelper(t *testing.T) {
	tests := map[string]string{
		"MyRouter":        "ups-pi-node-MyRouter-b3a70bc4",
		"Office UPS":      "ups-pi-node-Office-UPS-e15e7ddc",
		"Приватна мережа": "ups-pi-node-Приватна-мережа-2f60bfba",
	}
	for ssid, expected := range tests {
		if actual := managedConnectionName(ssid); actual != expected {
			t.Errorf("managedConnectionName(%q) = %q, want %q", ssid, actual, expected)
		}
	}
}

func TestConnectionSettingsDoNotExposePasswordOutsideSecuritySection(t *testing.T) {
	settings, err := buildConnectionSettings("wlan0", "managed", "router", "secret", false)
	if err != nil {
		t.Fatal(err)
	}
	security := settings["802-11-wireless-security"]
	if variantString(security["psk"]) != "secret" {
		t.Fatal("PSK missing from NetworkManager security setting")
	}
	if _, exists := settings["connection"]["psk"]; exists {
		t.Fatal("PSK leaked into connection metadata")
	}
}

func TestDeviceStateMapping(t *testing.T) {
	tests := map[uint32]string{
		0:   "unknown",
		20:  "unavailable",
		30:  "disconnected",
		40:  "connecting",
		100: "connected",
		110: "connecting",
		120: "disconnected",
	}
	for state, expected := range tests {
		if actual := deviceStateName(state); actual != expected {
			t.Errorf("deviceStateName(%d) = %q, want %q", state, actual, expected)
		}
	}
}
