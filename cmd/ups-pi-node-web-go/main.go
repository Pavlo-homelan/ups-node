package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ups-pi-node/internal/helperclient"
	"ups-pi-node/internal/webapp"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Go web service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var configPath string
	var listenAddress string
	var socketGroup string
	var helperSocket string
	var secureCookies bool
	var allowInsecureAuth bool
	flag.StringVar(&configPath, "config", "/etc/ups-pi-node/main.conf", "existing ups-pi-node INI config")
	flag.StringVar(&listenAddress, "listen", "unix:/run/ups-pi-node-web/web.sock", "unix:/path or tcp:host:port")
	flag.StringVar(&socketGroup, "socket-group", "www-data", "group allowed to connect to the web Unix socket")
	flag.StringVar(&helperSocket, "helper-socket", "", "override privileged helper Unix socket")
	flag.BoolVar(&secureCookies, "secure-cookies", false, "send cookies only over HTTPS")
	flag.BoolVar(&allowInsecureAuth, "allow-insecure-auth", false, "allow mock authentication for isolated development only")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	config, err := webapp.LoadConfig(configPath)
	if err != nil {
		return err
	}
	config.SessionSecret = []byte(os.Getenv("UPS_PI_NODE_SECRET_KEY"))
	if token := os.Getenv("UPS_PI_NODE_INTEGRATIONS_TOKEN"); token != "" {
		config.IntegrationsToken = token
	}
	if token := os.Getenv("UPS_PI_NODE_PROVIDER_TOKEN"); token != "" {
		config.ProviderToken = token
	} else if token, err := readCredential("provider-token"); err != nil {
		return err
	} else {
		config.ProviderToken = token
	}
	if nodeID := os.Getenv("UPS_PI_NODE_NODE_ID"); nodeID != "" {
		config.NodeID = nodeID
	}
	if helperSocket := os.Getenv("UPS_PI_NODE_SYSTEM_HELPER_SOCKET"); helperSocket != "" {
		config.HelperSocket = helperSocket
	}
	if interfaceName := os.Getenv("UPS_PI_NODE_WIFI_INTERFACE"); interfaceName != "" {
		config.WiFiInterface = interfaceName
	}
	if hotspotConnection := os.Getenv("UPS_PI_NODE_HOTSPOT_CONNECTION_NAME"); hotspotConnection != "" {
		config.HotspotConnection = hotspotConnection
	}
	if hotspotSSID := os.Getenv("UPS_PI_NODE_HOTSPOT_SSID"); hotspotSSID != "" {
		config.HotspotSSID = hotspotSSID
	}
	if hotspotPassword := os.Getenv("UPS_PI_NODE_HOTSPOT_PASSWORD"); hotspotPassword != "" {
		config.HotspotPassword = hotspotPassword
	}
	if portalMode := os.Getenv("UPS_PI_NODE_PORTAL_MODE"); portalMode != "" {
		config.PortalMode = portalMode
	}
	if ipv6Mode := os.Getenv("UPS_PI_NODE_WIFI_IPV6"); ipv6Mode != "" {
		config.WiFiIPv6 = webapp.NormalizeWiFiIPv6Mode(ipv6Mode)
	}
	if authMode := os.Getenv("UPS_PI_NODE_AUTH_MODE"); authMode != "" {
		config.AuthMode = authMode
	}
	if pamService := os.Getenv("UPS_PI_NODE_PAM_SERVICE"); pamService != "" {
		config.PAMService = pamService
	}
	config.PortalUsername = firstNonEmpty(os.Getenv("UPS_PI_NODE_PORTAL_USERNAME"), os.Getenv("UPS_PI_NODE_USERNAME"))
	config.PortalPassword = firstNonEmpty(os.Getenv("UPS_PI_NODE_PORTAL_PASSWORD"), os.Getenv("UPS_PI_NODE_PASSWORD"))
	if widgetsDir := os.Getenv("UPS_PI_NODE_WIDGETS_DIR"); widgetsDir != "" {
		config.WidgetsDir = widgetsDir
	}
	if dashboardWidgets := os.Getenv("UPS_PI_NODE_DASHBOARD_WIDGETS_FILE"); dashboardWidgets != "" {
		config.DashboardWidgets = dashboardWidgets
	}
	config.SecureCookies = secureCookies
	config.AllowInsecureAuth = allowInsecureAuth
	if helperSocket != "" {
		config.HelperSocket = helperSocket
	}
	helper := helperclient.Client{SocketPath: config.HelperSocket, Timeout: 25 * time.Second}
	application, err := webapp.NewServer(config, helper, logger)
	if err != nil {
		return err
	}
	listener, err := webapp.Listen(listenAddress, socketGroup)
	if err != nil {
		return err
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go application.RunStatusHub(ctx)

	httpServer := &http.Server{
		Handler:           application.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- httpServer.Serve(listener) }()
	logger.Info("Go web service listening", "address", listenAddress, "helper", config.HelperSocket)
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			logger.Warn("Go web service graceful shutdown timed out", "error", err)
			if closeErr := httpServer.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
				return closeErr
			}
		}
		return nil
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
}

func readCredential(name string) (string, error) {
	directory := strings.TrimSpace(os.Getenv("CREDENTIALS_DIRECTORY"))
	if directory == "" {
		return "", nil
	}
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() < 32 || info.Size() > 1024 {
		return "", errors.New("Provider telemetry credential is invalid")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(payload))
	if len(value) < 32 {
		return "", errors.New("Provider telemetry credential is too short")
	}
	return value, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
