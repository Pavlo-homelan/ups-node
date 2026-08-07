package iniconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPreservesValuesAndNormalizesNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.conf")
	if err := os.WriteFile(path, []byte("[WiFi]\nInterface = wlan0\nssid = My Home\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Get("wifi", "interface", "") != "wlan0" || file.Get("WIFI", "SSID", "") != "My Home" {
		t.Fatalf("config = %#v", file)
	}
	if file.Get("missing", "key", "fallback") != "fallback" {
		t.Fatal("fallback was not returned")
	}
}
