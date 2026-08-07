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
	settings, err := buildConnectionSettings("wlan0", "managed", "router", "secret", false, "disabled")
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

func TestConnectionSettingsDisableIPv6ByDefault(t *testing.T) {
	settings, err := buildConnectionSettings("wlan0", "managed", "router", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if actual := variantString(settings["ipv6"]["method"]); actual != "ignore" {
		t.Fatalf("ipv6.method = %q, want ignore", actual)
	}
	if actual := settings["connection"]["autoconnect-priority"].Value(); actual != managedAutoconnectPriority {
		t.Fatalf("autoconnect-priority = %#v, want %d", actual, managedAutoconnectPriority)
	}
	if actual := settings["802-11-wireless"]["powersave"].Value(); actual != networkManagerPowerSaveDisable {
		t.Fatalf("powersave = %#v, want %d", actual, networkManagerPowerSaveDisable)
	}
	if _, exists := settings["ipv6"]["addresses"]; exists {
		t.Fatal("ipv6.addresses must not be emitted for automatic profiles")
	}
}

func TestConnectionSettingsCanEnableIPv6Auto(t *testing.T) {
	settings, err := buildConnectionSettings("wlan0", "managed", "router", "", false, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if actual := variantString(settings["ipv6"]["method"]); actual != "auto" {
		t.Fatalf("ipv6.method = %q, want auto", actual)
	}
}

func TestInvalidIPv6ModeIsRejectedBeforeNetworkManagerCall(t *testing.T) {
	if _, err := buildConnectionSettings("wlan0", "managed", "router", "", false, "manual"); err == nil {
		t.Fatal("invalid IPv6 mode was accepted")
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
