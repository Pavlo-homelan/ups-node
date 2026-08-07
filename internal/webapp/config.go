package webapp

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"ups-pi-node/internal/iniconfig"
)

type Config struct {
	ConfigPath        string
	NodeID            string
	Language          string
	IntegrationsToken string
	HelperSocket      string
	WiFiInterface     string
	HotspotConnection string
	HotspotSSID       string
	HotspotPassword   string
	WiFiIPv6          string
	PortalMode        string
	AuthMode          string
	PAMService        string
	PortalUsername    string
	PortalPassword    string
	WidgetsDir        string
	DashboardWidgets  string
	LoadTimeout1      int
	LoadTimeout2      int
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
		HotspotSSID:       "Ups-Node",
		HotspotPassword:   "12345678",
		WiFiIPv6:          "disabled",
		PortalMode:        "auto",
		AuthMode:          "pam",
		PAMService:        "ups-pi-node",
		WidgetsDir:        "/var/lib/ups-pi-node/widgets",
		DashboardWidgets:  "/var/lib/ups-pi-node/dashboard-widgets.json",
		LoadTimeout1:      30,
		LoadTimeout2:      300,
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
	config.ConfigPath = path
	config.NodeID = values.Get("integrations", "node_id", config.NodeID)
	config.IntegrationsToken = values.Get("integrations", "token", config.IntegrationsToken)
	config.Language = strings.ToLower(values.Get("ui", "language", config.Language))
	config.HelperSocket = values.Get("system", "helper_socket", config.HelperSocket)
	config.WiFiInterface = values.Get("wifi", "interface", config.WiFiInterface)
	config.HotspotConnection = values.Get("wifi", "hotspot_connection", config.HotspotConnection)
	config.HotspotSSID = values.Get("wifi", "hotspot_ssid", config.HotspotSSID)
	config.HotspotPassword = values.Get("wifi", "hotspot_password", config.HotspotPassword)
	config.WiFiIPv6 = normalizeWiFiIPv6Mode(values.Get("wifi", "ipv6", config.WiFiIPv6))
	config.PortalMode = strings.ToLower(values.Get("wifi", "portal_mode", config.PortalMode))
	config.AuthMode = strings.ToLower(values.Get("auth", "mode", config.AuthMode))
	config.PAMService = values.Get("auth", "pam_service", config.PAMService)
	config.LoadTimeout1 = intValue(values.Get("load", "timeout_1", ""), config.LoadTimeout1)
	config.LoadTimeout2 = intValue(values.Get("load", "timeout_2", ""), config.LoadTimeout2)
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
	if config.LoadTimeout1 < 1 || config.LoadTimeout1 > 3600 || config.LoadTimeout2 < 1 || config.LoadTimeout2 > 3600 {
		return fmt.Errorf("load timeouts must be between 1 and 3600 seconds")
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

func intValue(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}
