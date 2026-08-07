package webapp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaultsWiFiIPv6ToDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.conf")
	if err := os.WriteFile(path, []byte("[wifi]\ninterface = wlan1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.WiFiIPv6 != "disabled" {
		t.Fatalf("WiFiIPv6 = %q, want disabled", config.WiFiIPv6)
	}
}

func TestLoadConfigNormalizesWiFiIPv6Mode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.conf")
	if err := os.WriteFile(path, []byte("[wifi]\nipv6 = ignore\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.WiFiIPv6 != "disabled" {
		t.Fatalf("WiFiIPv6 = %q, want disabled", config.WiFiIPv6)
	}
}

func TestConfigRejectsUnsupportedWiFiIPv6Mode(t *testing.T) {
	config := secureTestConfig()
	config.WiFiIPv6 = "manual"
	if err := config.Validate(); err == nil {
		t.Fatal("unsupported IPv6 mode was accepted")
	}
}
