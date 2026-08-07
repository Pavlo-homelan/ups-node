package webapp

import (
	"fmt"
	"strings"
	"time"

	"ups-pi-node/internal/iniconfig"
)

type Config struct {
	NodeID            string
	Language          string
	IntegrationsToken string
	HelperSocket      string
	WiFiInterface     string
	HotspotConnection string
	WiFiIPv6          string
	PortalMode        string
	AuthMode          string
	PAMService        string
	PortalUsername    string
	PortalPassword    string
	AllowInsecureAuth bool
	SessionSecret     []byte
	SessionTTL        time.Duration
	SecureCookies     bool
	StatusInterval    time.Duration
}

func DefaultConfig() Config {
	return Config{
		NodeID:            "ups-pi-node",
		Language:          "uk",
		HelperSocket:      "/run/ups-pi-node/helper.sock",
		WiFiInterface:     "wlan0",
		HotspotConnection: "ups-pi-node-hotspot",
		WiFiIPv6:          "disabled",
		PortalMode:        "auto",
		AuthMode:          "pam",
		PAMService:        "ups-pi-node",
		SessionTTL:        12 * time.Hour,
		StatusInterval:    time.Second,
	}
}

func LoadConfig(path string) (Config, error) {
	values, err := iniconfig.Read(path)
	if err != nil {
		return Config{}, err
	}
	config := DefaultConfig()
	config.NodeID = values.Get("integrations", "node_id", config.NodeID)
	config.IntegrationsToken = values.Get("integrations", "token", config.IntegrationsToken)
	config.Language = strings.ToLower(values.Get("ui", "language", config.Language))
	config.HelperSocket = values.Get("system", "helper_socket", config.HelperSocket)
	config.WiFiInterface = values.Get("wifi", "interface", config.WiFiInterface)
	config.HotspotConnection = values.Get("wifi", "hotspot_connection", config.HotspotConnection)
	config.WiFiIPv6 = normalizeWiFiIPv6Mode(values.Get("wifi", "ipv6", config.WiFiIPv6))
	config.PortalMode = strings.ToLower(values.Get("wifi", "portal_mode", config.PortalMode))
	config.AuthMode = strings.ToLower(values.Get("auth", "mode", config.AuthMode))
	config.PAMService = values.Get("auth", "pam_service", config.PAMService)
	// Environment-only secrets and credentials are added by the process entry
	// point before the complete configuration is validated by NewServer.
	return config, nil
}

func NormalizeWiFiIPv6Mode(value string) string {
	return normalizeWiFiIPv6Mode(value)
}

func normalizeWiFiIPv6Mode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "disabled", "disable", "off", "false", "no", "0", "ignore":
		return "disabled"
	case "auto", "enabled", "enable", "on", "true", "yes", "1":
		return "auto"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func (config Config) Validate() error {
	if config.NodeID == "" || config.HelperSocket == "" || config.WiFiInterface == "" {
		return fmt.Errorf("node id, helper socket, and Wi-Fi interface are required")
	}
	if config.Language != "uk" && config.Language != "en" {
		return fmt.Errorf("unsupported UI language %q", config.Language)
	}
	if config.AuthMode != "pam" && config.AuthMode != "env" && config.AuthMode != "mock" {
		return fmt.Errorf("unsupported auth mode %q", config.AuthMode)
	}
	if config.WiFiIPv6 != "disabled" && config.WiFiIPv6 != "auto" {
		return fmt.Errorf("unsupported Wi-Fi IPv6 mode %q", config.WiFiIPv6)
	}
	if config.AuthMode == "mock" && !config.AllowInsecureAuth {
		return fmt.Errorf("mock authentication requires explicit --allow-insecure-auth")
	}
	if config.AuthMode == "env" && (config.PortalUsername == "" || config.PortalPassword == "") {
		return fmt.Errorf("environment authentication requires a username and password")
	}
	if len(config.SessionSecret) < 32 {
		return fmt.Errorf("UPS_PI_NODE_SECRET_KEY must contain at least 32 bytes")
	}
	if config.SessionTTL <= 0 || config.StatusInterval < 500*time.Millisecond {
		return fmt.Errorf("session and status durations are invalid")
	}
	return nil
}
