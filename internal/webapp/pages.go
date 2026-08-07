package webapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ups-pi-node/internal/iniconfig"
)

type flashMessage struct {
	Kind string
	Text string
}

type portalStatusView struct {
	ConnectedSSID      string
	ConnectedSSIDLabel string
	IPAddress          string
	IPAddressLabel     string
	PortalMode         string
	PortalModeLabel    string
	Interface          string
}

type wifiScanView struct {
	Networks []wifiNetworkView
}

type wifiNetworkView struct {
	SSID          string
	Signal        int
	Security      string
	SecurityLabel string
	Connected     bool
	IsOpen        bool
}

type cardStatusView struct {
	VoltageV        float64
	CurrentMA       float64
	OutputVoltageV  float64
	OutputCurrentMA float64
	OutputPowerW    float64
	State           string
	StateLabel      string
	Color           template.CSS
	AC              bool
	ACLabel         string
	Percent         int
	CPU             float64
}

type upsOverviewView struct {
	Percent       int
	Color         template.CSS
	StatusClass   string
	StatusLabel   string
	Direction     string
	Mode          string
	Mains         string
	Load          string
	Route         string
	Summary       string
	Voltage       string
	Current       string
	Power         string
	OutputVoltage string
	OutputCurrent string
	OutputPower   string
}

type dashboardWidgetView struct {
	Language      string
	ID            string
	Kind          string
	LabelKey      string
	Label         string
	Value         string
	Detail        string
	LiveKey       string
	DetailLiveKey string
	Data          cardStatusView
	Overview      upsOverviewView
}

func (server *Server) configSnapshot() Config {
	server.configMu.RLock()
	defer server.configMu.RUnlock()
	return server.config
}

func (server *Server) setConfig(mutator func(*Config), values map[string]map[string]string) error {
	server.configMu.Lock()
	defer server.configMu.Unlock()
	if server.config.ConfigPath == "" {
		return fmt.Errorf("configuration file path is not set")
	}
	file, err := iniconfig.Read(server.config.ConfigPath)
	if err != nil {
		return err
	}
	for section, sectionValues := range values {
		for key, value := range sectionValues {
			file.Set(section, key, value)
		}
	}
	if err := iniconfig.Write(server.config.ConfigPath, file); err != nil {
		return err
	}
	next := server.config
	mutator(&next)
	if err := next.Validate(); err != nil {
		return err
	}
	server.config = next
	return nil
}

func (server *Server) basePageData(w http.ResponseWriter, r *http.Request, activePage string) (pageData, bool) {
	config := server.configSnapshot()
	claims, _ := server.security.session(r)
	csrf, err := server.security.csrfFor(w, r)
	if err != nil {
		http.Error(w, "CSRF unavailable", http.StatusInternalServerError)
		return pageData{}, false
	}
	initialStatus := "{}"
	if status, ok := server.hub.Current(); ok {
		if encoded, encodeErr := json.Marshal(status); encodeErr == nil {
			initialStatus = string(encoded)
		}
	}
	widgetOptions := server.widgets.listOptions()
	customWidgets := make([]widgetStyle, 0)
	for _, style := range widgetOptions {
		if style.Custom {
			customWidgets = append(customWidgets, style)
		}
	}
	return pageData{
		Language: normalizeLanguage(config.Language), NodeID: config.NodeID, Username: claims.Username,
		CSRF: csrf, InitialStatus: initialStatus, ActivePage: activePage, Flash: flashFromRequest(r),
		SupportedLanguages: supportedLanguages, WidgetStyleOptions: widgetOptions, CustomWidgetStyles: customWidgets,
		WidgetStyleOptionsJSON: mustJSON(widgetOptions), ClientI18NJSON: mustJSON(clientTranslations(config.Language)),
		Timeout1: config.LoadTimeout1, Timeout2: config.LoadTimeout2, HotspotSSID: config.HotspotSSID,
		HotspotPassword: config.HotspotPassword, WiFiIPv6: config.WiFiIPv6, AuthMode: config.AuthMode,
		HelperSocket: config.HelperSocket, StatusInterval: config.StatusInterval.String(),
	}, true
}

func mustJSON(value any) template.JS {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return template.JS(encoded)
}

func flashFromRequest(r *http.Request) []flashMessage {
	query := r.URL.Query()
	messages := make([]flashMessage, 0, 2)
	if text := strings.TrimSpace(query.Get("notice")); text != "" {
		messages = append(messages, flashMessage{Kind: "info", Text: text})
	}
	if text := strings.TrimSpace(query.Get("error")); text != "" {
		messages = append(messages, flashMessage{Kind: "error", Text: text})
	}
	return messages
}

func redirectWithMessage(w http.ResponseWriter, r *http.Request, path string, kind string, text string) {
	values := url.Values{}
	if kind == "error" {
		values.Set("error", text)
	} else {
		values.Set("notice", text)
	}
	http.Redirect(w, r, path+"?"+values.Encode(), http.StatusSeeOther)
}

func (server *Server) renderTemplate(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := server.templates.ExecuteTemplate(w, name, data); err != nil {
		server.logger.Error("render template failed", "template", name, "error", err)
	}
}

func (server *Server) renderDashboardPage(w http.ResponseWriter, r *http.Request) {
	data, ok := server.basePageData(w, r, "ups")
	if !ok {
		return
	}
	status, _ := server.hub.Current()
	data.PortalStatus = portalStatus(status, server.configSnapshot(), data.Language)
	data.DashboardWidgets = server.dashboardWidgetViews(status, data.Language)
	server.renderTemplate(w, "dashboard.html", data)
}

func (server *Server) handleWiFiPage(w http.ResponseWriter, r *http.Request) {
	data, ok := server.basePageData(w, r, "wifi")
	if !ok {
		return
	}
	status, _ := server.hub.Current()
	data.PortalStatus = portalStatus(status, server.configSnapshot(), data.Language)
	scan, err := server.scanNetworks(r.Context())
	if err != nil {
		data.Flash = append(data.Flash, flashMessage{Kind: "error", Text: err.Error()})
	}
	data.ScanResult = scan
	server.renderTemplate(w, "wifi.html", data)
}

func (server *Server) handleSystemPage(w http.ResponseWriter, r *http.Request) {
	data, ok := server.basePageData(w, r, "system")
	if !ok {
		return
	}
	status, _ := server.hub.Current()
	data.PortalStatus = portalStatus(status, server.configSnapshot(), data.Language)
	data.ActiveDashboardWidgets = server.dashboard.listActive()
	data.AvailableWidgets = server.dashboard.listAvailable()
	server.renderTemplate(w, "system.html", data)
}

func (server *Server) handleWiFiConnectForm(w http.ResponseWriter, r *http.Request) {
	if !server.security.verifyCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectWithMessage(w, r, "/wifi", "error", "Некоректний запит.")
		return
	}
	ssid := strings.TrimSpace(r.FormValue("ssid"))
	if ssid == "" {
		redirectWithMessage(w, r, "/wifi", "error", translate(server.configSnapshot().Language, "flash.wifi_missing_ssid"))
		return
	}
	config := server.configSnapshot()
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	var result map[string]any
	err := server.helper.Call(ctx, "wifi.connect", map[string]any{
		"interface": config.WiFiInterface, "ssid": ssid, "password": r.FormValue("password"),
		"hidden": formBool(r.FormValue("hidden")), "ipv6": config.WiFiIPv6,
	}, &result)
	if err != nil {
		redirectWithMessage(w, r, "/wifi", "error", err.Error())
		return
	}
	message, _ := result["message"].(string)
	if message == "" {
		message = fmt.Sprintf("Підключення до мережі %q виконано.", ssid)
	}
	redirectWithMessage(w, r, "/wifi", "notice", message)
}

func (server *Server) handleSystemLanguage(w http.ResponseWriter, r *http.Request) {
	if !server.security.verifyCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectWithMessage(w, r, "/system", "error", "Некоректний запит.")
		return
	}
	language := strings.TrimSpace(r.FormValue("language"))
	if language != "uk" && language != "en" {
		redirectWithMessage(w, r, "/system", "error", translate(server.configSnapshot().Language, "flash.language_invalid"))
		return
	}
	err := server.setConfig(func(config *Config) { config.Language = language }, map[string]map[string]string{
		"ui": {"language": language},
	})
	if err != nil {
		redirectWithMessage(w, r, "/system", "error", err.Error())
		return
	}
	redirectWithMessage(w, r, "/system", "notice", translate(language, "flash.language_saved"))
}

func (server *Server) handleSystemHotspot(w http.ResponseWriter, r *http.Request) {
	if !server.security.verifyCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectWithMessage(w, r, "/system", "error", "Некоректний запит.")
		return
	}
	config := server.configSnapshot()
	ssid := strings.TrimSpace(r.FormValue("hotspot_ssid"))
	password := r.FormValue("hotspot_password")
	if ssid == "" {
		redirectWithMessage(w, r, "/system", "error", translate(config.Language, "flash.hotspot_ssid_invalid"))
		return
	}
	if len([]byte(ssid)) > 32 {
		redirectWithMessage(w, r, "/system", "error", translate(config.Language, "flash.hotspot_ssid_too_long"))
		return
	}
	if len(password) < 8 || len(password) > 63 {
		redirectWithMessage(w, r, "/system", "error", translate(config.Language, "flash.hotspot_password_invalid"))
		return
	}
	err := server.setConfig(func(config *Config) {
		config.HotspotSSID = ssid
		config.HotspotPassword = password
	}, map[string]map[string]string{
		"wifi": {"hotspot_ssid": ssid, "hotspot_password": password},
	})
	if err != nil {
		redirectWithMessage(w, r, "/system", "error", err.Error())
		return
	}
	redirectWithMessage(w, r, "/system", "notice", translate(config.Language, "flash.hotspot_saved"))
}

func (server *Server) handleSystemTimeouts(w http.ResponseWriter, r *http.Request) {
	if !server.security.verifyCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectWithMessage(w, r, "/system", "error", "Некоректний запит.")
		return
	}
	timeout1, err1 := boundedInt(r.FormValue("timeout_1"), 1, 3600)
	timeout2, err2 := boundedInt(r.FormValue("timeout_2"), 1, 3600)
	if err1 != nil || err2 != nil {
		redirectWithMessage(w, r, "/system", "error", "Таймаути мають бути від 1 до 3600 секунд.")
		return
	}
	config := server.configSnapshot()
	err := server.setConfig(func(config *Config) {
		config.LoadTimeout1 = timeout1
		config.LoadTimeout2 = timeout2
	}, map[string]map[string]string{
		"load": {"timeout_1": strconv.Itoa(timeout1), "timeout_2": strconv.Itoa(timeout2)},
	})
	if err != nil {
		redirectWithMessage(w, r, "/system", "error", err.Error())
		return
	}
	redirectWithMessage(w, r, "/system", "notice", translate(config.Language, "flash.settings_saved"))
}

func (server *Server) handleWidgetUpload(w http.ResponseWriter, r *http.Request) {
	if !server.security.verifyCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWidgetZipBytes+64*1024)
	if err := r.ParseMultipartForm(maxWidgetZipBytes + 64*1024); err != nil {
		redirectWithMessage(w, r, "/system", "error", err.Error())
		return
	}
	file, header, err := r.FormFile("widget_file")
	if err != nil {
		redirectWithMessage(w, r, "/system", "error", widgetErrorText(server.configSnapshot().Language, widgetUploadError{key: "widget.error.select_zip"}))
		return
	}
	_ = file.Close()
	style, err := server.widgets.installPackage(header)
	if err != nil {
		redirectWithMessage(w, r, "/system", "error", widgetErrorText(server.configSnapshot().Language, err))
		return
	}
	redirectWithMessage(w, r, "/system", "notice", fmt.Sprintf("Віджет %q встановлено з ZIP.", style.Label))
}

func (server *Server) handleWidgetDelete(w http.ResponseWriter, r *http.Request) {
	if !server.security.verifyCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	config := server.configSnapshot()
	if err := server.widgets.deleteCustom(r.PathValue("style")); err != nil {
		redirectWithMessage(w, r, "/system", "error", widgetErrorText(config.Language, err))
		return
	}
	redirectWithMessage(w, r, "/system", "notice", translate(config.Language, "flash.widget_deleted"))
}

func (server *Server) handleDashboardWidgetAdd(w http.ResponseWriter, r *http.Request) {
	if !server.security.verifyCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	config := server.configSnapshot()
	ok, err := server.dashboard.add(r.PathValue("widget"))
	if err != nil {
		redirectWithMessage(w, r, "/system", "error", err.Error())
		return
	}
	if !ok {
		redirectWithMessage(w, r, "/system", "error", translate(config.Language, "flash.dashboard_widget_not_found"))
		return
	}
	redirectWithMessage(w, r, "/system", "notice", translate(config.Language, "flash.dashboard_widget_added"))
}

func (server *Server) handleDashboardWidgetRemove(w http.ResponseWriter, r *http.Request) {
	if !server.security.verifyCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	config := server.configSnapshot()
	ok, err := server.dashboard.remove(r.PathValue("widget"))
	if err != nil {
		redirectWithMessage(w, r, "/system", "error", err.Error())
		return
	}
	if !ok {
		redirectWithMessage(w, r, "/system", "error", translate(config.Language, "flash.dashboard_widget_not_found"))
		return
	}
	redirectWithMessage(w, r, "/system", "notice", translate(config.Language, "flash.dashboard_widget_removed"))
}

func (server *Server) handleWidgetCSS(w http.ResponseWriter, r *http.Request) {
	path, ok := server.widgets.cssPath(r.PathValue("filename"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, path)
}

func (server *Server) handleWidgetFile(w http.ResponseWriter, r *http.Request) {
	path, ok := server.widgets.filePath(r.PathValue("style"), r.PathValue("filename"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", widgetMimeType(path))
	http.ServeFile(w, r, path)
}

func (server *Server) handleAPIData(w http.ResponseWriter, _ *http.Request) {
	status, ok := server.hub.Current()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "status is initializing"})
		return
	}
	writeJSON(w, http.StatusOK, buildLegacyLivePayload(status, server.configSnapshot().Language))
}

func (server *Server) scanNetworks(parent context.Context) (wifiScanView, error) {
	config := server.configSnapshot()
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	var result map[string]any
	err := server.helper.Call(ctx, "wifi.scan", map[string]any{"interface": config.WiFiInterface}, &result)
	if err != nil {
		return wifiScanView{}, err
	}
	rawNetworks, _ := result["networks"].([]any)
	networks := make([]wifiNetworkView, 0, len(rawNetworks))
	for _, rawNetwork := range rawNetworks {
		network, ok := rawNetwork.(map[string]any)
		if !ok {
			continue
		}
		security := strings.TrimSpace(stringFromAny(network["security"]))
		if security == "" {
			security = strings.TrimSpace(stringFromAny(network["security_label"]))
		}
		isOpen := strings.EqualFold(security, "open") || strings.EqualFold(security, "none") || security == ""
		securityLabel := security
		if isOpen {
			securityLabel = translate(config.Language, "wifi.security_open")
		}
		networks = append(networks, wifiNetworkView{
			SSID: stringFromAny(network["ssid"]), Signal: int(numberFromAny(network["signal"])),
			Security: security, SecurityLabel: securityLabel, Connected: boolFromAny(network["connected"]), IsOpen: isOpen,
		})
	}
	return wifiScanView{Networks: networks}, nil
}

func portalStatus(status LiveStatus, config Config, language string) portalStatusView {
	ssid := status.ConnectedSSID
	ssidLabel := ssid
	if ssidLabel == "" {
		ssidLabel = translate(language, "wifi.none")
	}
	ipAddress := status.IPAddress
	ipLabel := ipAddress
	if ipLabel == "" {
		ipLabel = "--"
	}
	return portalStatusView{
		ConnectedSSID: ssid, ConnectedSSIDLabel: ssidLabel, IPAddress: ipAddress, IPAddressLabel: ipLabel,
		PortalMode: status.PortalMode, PortalModeLabel: portalModeLabel(status.PortalMode, status.WiFiState),
		Interface: config.WiFiInterface,
	}
}

func (server *Server) dashboardWidgetViews(status LiveStatus, language string) []dashboardWidgetView {
	card := cardStatus(status, language)
	portal := portalStatus(status, server.configSnapshot(), language)
	widgets := server.dashboard.listActive()
	result := make([]dashboardWidgetView, 0, len(widgets))
	for _, widget := range widgets {
		view := dashboardWidgetView{
			Language: language, ID: widget.ID, Kind: widget.Kind, LabelKey: widget.LabelKey, Label: translate(language, widget.LabelKey),
		}
		switch widget.Kind {
		case "ups":
			view.Data = card
		case "ups_overview":
			view.Overview = upsOverview(status, card, language)
		case "sensor_cpu":
			view.Value = fmt.Sprintf("%.1f°C", status.CPUTemperatureC)
			view.LiveKey = "cpu-full"
		case "sensor_ram":
			view.Value = fmt.Sprintf("%.1fMB (%.1f%%)", status.RAMUsedMB, status.RAMPercent)
			view.LiveKey = "ram"
		case "status_wifi":
			view.Value = portal.ConnectedSSIDLabel
			view.Detail = portal.PortalModeLabel
			view.LiveKey = "wifi-ssid"
			view.DetailLiveKey = "portal-mode"
		}
		result = append(result, view)
	}
	return result
}

func cardStatus(status LiveStatus, language string) cardStatusView {
	state := strings.ToUpper(strings.TrimSpace(status.UPSState))
	if state == "" {
		state = "IDLE"
	}
	currentAbs := math.Abs(status.CurrentMA)
	color := "var(--muted)"
	switch {
	case !status.AC:
		state = "DISCHARGE"
		color = "var(--red)"
	case strings.EqualFold(status.BatteryStatus, "Full") && currentAbs < 150:
		state = "CHARGED"
		color = "var(--green)"
	case strings.EqualFold(status.BatteryDirection, "Charging") || state == "CHARGE":
		state = "CHARGE"
		color = "var(--yellow)"
	}
	return cardStatusView{
		VoltageV: status.VoltageV, CurrentMA: currentAbs, OutputVoltageV: status.OutputVoltageV,
		OutputCurrentMA: math.Abs(status.OutputCurrentMA), OutputPowerW: status.OutputPowerW,
		State: state, StateLabel: translate(language, "ups.state."+state), Color: template.CSS(color), AC: status.AC,
		ACLabel: translate(language, mapBool(status.AC, "ups.ac_ok", "ups.ac_lost")), Percent: status.BatteryPercent,
		CPU: status.CPUTemperatureC,
	}
}

func upsOverview(status LiveStatus, card cardStatusView, language string) upsOverviewView {
	statusKey := strings.ToLower(strings.TrimSpace(status.BatteryStatus))
	if statusKey == "" {
		statusKey = "unknown"
	}
	direction := strings.ToLower(strings.TrimSpace(status.BatteryDirection))
	if direction == "" {
		direction = "idle"
	}
	summaryKey := "ups.summary.stable"
	switch {
	case status.AC && direction == "charging":
		summaryKey = "ups.summary.charging"
	case !status.AC && direction == "discharging":
		summaryKey = "ups.summary.discharging"
	case statusKey == "full":
		summaryKey = "ups.summary.full"
	case statusKey == "critical":
		summaryKey = "ups.summary.critical"
	}
	return upsOverviewView{
		Percent: card.Percent, Color: card.Color, StatusClass: statusKey,
		StatusLabel: translate(language, "ups.battery_status."+statusKey),
		Direction:   translate(language, "ups.direction."+direction),
		Mode:        translate(language, mapBool(status.AC, "ups.mode.line", "ups.mode.backup")),
		Mains:       translate(language, mapBool(status.AC, "ups.mains.online", "ups.mains.lost")),
		Load:        translate(language, mapBool(status.AC, "ups.load.line", "ups.load.battery")),
		Route:       translate(language, mapBool(status.AC, "ups.route.charger", "ups.route.load")),
		Summary:     translate(language, summaryKey), Voltage: voltageLabel(status.VoltageV),
		Current: currentLabel(math.Abs(status.CurrentMA)), Power: powerLabel(status.PowerW),
		OutputVoltage: voltageLabel(status.OutputVoltageV), OutputCurrent: currentLabel(math.Abs(status.OutputCurrentMA)),
		OutputPower: powerLabel(status.OutputPowerW),
	}
}

func buildLegacyLivePayload(status LiveStatus, language string) map[string]any {
	card := cardStatus(status, language)
	overview := upsOverview(status, card, language)
	portal := portalStatus(status, Config{}, language)
	return map[string]any{
		"status": status.Status, "portal_mode": status.PortalMode, "portal_mode_label": portal.PortalModeLabel,
		"wifi_backend": "", "interface": "", "connected_ssid": status.ConnectedSSID,
		"connected_ssid_label": portal.ConnectedSSIDLabel, "ip_address": status.IPAddress, "ip_address_label": portal.IPAddressLabel,
		"mains_present": status.AC, "mains_label": overview.Mains, "ups_mode": overview.Mode,
		"load_source": overview.Load, "battery_route": overview.Route,
		"battery_percent": status.BatteryPercent, "battery_percent_label": fmt.Sprintf("%d%%", status.BatteryPercent),
		"battery_status": status.BatteryStatus, "battery_direction": status.BatteryDirection,
		"battery_summary": overview.Summary, "voltage_label": voltageLabel(status.VoltageV),
		"current_label": currentLabel(math.Abs(status.CurrentMA)), "power_label": powerLabel(status.PowerW),
		"output_channel": status.OutputChannel, "output_voltage_label": voltageLabel(status.OutputVoltageV),
		"output_current_label": currentLabel(math.Abs(status.OutputCurrentMA)), "output_power_label": powerLabel(status.OutputPowerW),
		"cpu_temp_label": fmt.Sprintf("%.1f°C", status.CPUTemperatureC),
		"ram_label":      fmt.Sprintf("%.1fMB (%.1f%%)", status.RAMUsedMB, status.RAMPercent),
		"v":              status.VoltageV, "i": math.Abs(status.CurrentMA), "current_ma": status.CurrentMA, "power_w": status.PowerW,
		"output_v": status.OutputVoltageV, "output_i": math.Abs(status.OutputCurrentMA), "output_w": status.OutputPowerW,
		"state": card.State, "color": card.Color, "ac": status.AC, "percent": status.BatteryPercent,
		"cpu": status.CPUTemperatureC, "cpu_temp": status.CPUTemperatureC, "ram_used": status.RAMUsedMB, "ram_percent": status.RAMPercent,
	}
}

func widgetErrorText(language string, err error) string {
	var uploadErr widgetUploadError
	if errors.As(err, &uploadErr) {
		text := translate(language, uploadErr.key)
		for key, value := range uploadErr.params {
			text = strings.ReplaceAll(text, "{"+key+"}", value)
		}
		return text
	}
	return err.Error()
}

func formBool(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "1" || value == "true" || value == "on" || value == "yes"
}

func boundedInt(value string, minimum int, maximum int) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("invalid integer")
	}
	return parsed, nil
}

func mapBool(value bool, ifTrue string, ifFalse string) string {
	if value {
		return ifTrue
	}
	return ifFalse
}

func portalModeLabel(portalMode string, wifiState string) string {
	switch strings.ToLower(strings.TrimSpace(portalMode)) {
	case "client":
		return "Client"
	case "ap":
		return "Access point"
	default:
		if wifiState != "" {
			return wifiState
		}
		return "--"
	}
}

func voltageLabel(value float64) string { return fmt.Sprintf("%.2fV", value) }
func currentLabel(value float64) string { return fmt.Sprintf("%.0fmA", value) }
func powerLabel(value float64) string   { return fmt.Sprintf("%.2fW", value) }

func stringFromAny(value any) string {
	text, _ := value.(string)
	return text
}

func numberFromAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	default:
		return 0
	}
}

func boolFromAny(value any) bool {
	typed, _ := value.(bool)
	return typed
}
