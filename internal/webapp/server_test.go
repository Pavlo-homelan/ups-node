package webapp

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testServer(t *testing.T) (*Server, *fakeHelper) {
	t.Helper()
	config := secureTestConfig()
	config.AuthMode = "env"
	config.PortalUsername = "admin"
	config.PortalPassword = "secret"
	return testServerWithConfig(t, config)
}

func testServerWithConfig(t *testing.T, config Config) (*Server, *fakeHelper) {
	t.Helper()
	directory := t.TempDir()
	if config.ConfigPath == "" {
		config.ConfigPath = filepath.Join(directory, "main.conf")
		if err := os.WriteFile(config.ConfigPath, []byte(
			"[integrations]\nnode_id = "+config.NodeID+"\n\n"+
				"[system]\nhelper_socket = "+config.HelperSocket+"\n\n"+
				"[wifi]\ninterface = "+config.WiFiInterface+"\nhotspot_connection = "+config.HotspotConnection+"\nhotspot_ssid = "+config.HotspotSSID+"\nhotspot_password = "+config.HotspotPassword+"\nipv6 = "+config.WiFiIPv6+"\nportal_mode = "+config.PortalMode+"\n\n"+
				"[auth]\nmode = "+config.AuthMode+"\npam_service = "+config.PAMService+"\n\n"+
				"[load]\ntimeout_1 = 30\ntimeout_2 = 300\n\n"+
				"[ui]\nlanguage = "+config.Language+"\n",
		), 0o660); err != nil {
			t.Fatal(err)
		}
	}
	if config.WidgetsDir == DefaultConfig().WidgetsDir {
		config.WidgetsDir = filepath.Join(directory, "widgets")
	}
	if config.DashboardWidgets == DefaultConfig().DashboardWidgets {
		config.DashboardWidgets = filepath.Join(directory, "dashboard-widgets.json")
	}
	helper := &fakeHelper{}
	server, err := NewServer(config, helper, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server.hub.refresh(context.Background())
	return server, helper
}

func csrfFromLogin(t *testing.T, server *Server) (*http.Cookie, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/login", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "viewport") || !strings.Contains(recorder.Body.String(), "csrf_token") {
		t.Fatal("login page is missing responsive or CSRF markup")
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			return cookie, cookie.Value
		}
	}
	t.Fatal("CSRF cookie was not set")
	return nil, ""
}

func TestLoginPageDoesNotContainDecorativeCopy(t *testing.T) {
	server, _ := testServer(t)
	request := httptest.NewRequest(http.MethodGet, "/login", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, phrase := range []string{
		"POWER TELEMETRY",
		"Керування живленням, виходом 19 В і мережею ноди",
	} {
		if strings.Contains(body, phrase) {
			t.Fatalf("login page contains decorative label %q", phrase)
		}
	}
}

func loginSession(t *testing.T, server *Server) (*http.Cookie, *http.Cookie) {
	t.Helper()
	csrfCookie, token := csrfFromLogin(t, server)
	form := url.Values{"username": {"admin"}, "password": {"secret"}, "csrf_token": {token}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(csrfCookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/dashboard" {
		t.Fatalf("login response = %d %s", recorder.Code, recorder.Body.String())
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie, csrfCookie
		}
	}
	t.Fatal("session cookie was not set")
	return nil, nil
}

func postForm(t *testing.T, server *Server, session *http.Cookie, csrfCookie *http.Cookie, path string, values url.Values) {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	values.Set("csrf_token", csrfCookie.Value)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(session)
	request.AddCookie(csrfCookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("%s response = %d %s", path, recorder.Code, recorder.Body.String())
	}
}

func widgetUploadBody(t *testing.T, csrf string) (*bytes.Reader, string) {
	t.Helper()
	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	widgetJSON, err := zipWriter.Create("widget.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := widgetJSON.Write([]byte(`{"id":"my-widget","name":"My Widget","stylesheet":"style.css"}`)); err != nil {
		t.Fatal(err)
	}
	styleCSS, err := zipWriter.Create("style.css")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := styleCSS.Write([]byte(".ups-widget { border-color: #4caf50; }\n")); err != nil {
		t.Fatal(err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf_token", csrf); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("widget_file", "my-widget.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(body.Bytes()), writer.FormDataContentType()
}

func TestProtectedDashboardAndAPIRequireSignedSession(t *testing.T) {
	server, _ := testServer(t)
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/login" {
		t.Fatalf("dashboard response = %d", recorder.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("API response = %d", recorder.Code)
	}
}

func TestLoginDashboardAndCSRFProtectedMutation(t *testing.T) {
	server, _ := testServer(t)
	session, csrfCookie := loginSession(t, server)

	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(session)
	request.AddCookie(csrfCookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `href="/wifi"`) ||
		!strings.Contains(body, `href="/system"`) || !strings.Contains(body, "Живлення") ||
		!strings.Contains(body, "Батарея") {
		t.Fatalf("dashboard response = %d", recorder.Code)
	}
	for _, phrase := range []string{
		"LIVE POWER MAP",
		"Живлення під контролем",
		"Мережа активна: UPS живить вихід",
		"МАРШРУТ ЕНЕРГІЇ",
		"Звідки й куди",
		"UPS control node",
		"NETWORKMANAGER",
		"INA3221 · CHANNEL",
	} {
		if strings.Contains(body, phrase) {
			t.Fatalf("dashboard contains decorative label %q", phrase)
		}
	}
	if recorder.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("security headers are missing")
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/wifi/scan", nil)
	request.AddCookie(session)
	request.AddCookie(csrfCookie)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("mutation without CSRF = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/wifi/scan", nil)
	request.AddCookie(session)
	request.AddCookie(csrfCookie)
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "MyRouter") {
		t.Fatalf("CSRF mutation response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestWiFiAndSystemPagesRestoreFlaskNavigation(t *testing.T) {
	server, _ := testServer(t)
	session, csrfCookie := loginSession(t, server)

	for _, item := range []struct {
		path string
		want []string
	}{
		{"/wifi", []string{`href="/dashboard"`, `href="/system"`, "Поточна мережа", "MyRouter"}},
		{"/system", []string{`href="/dashboard"`, `href="/wifi"`, "Віджет UPS", "Віджети головної сторінки", "Таймаути"}},
	} {
		request := httptest.NewRequest(http.MethodGet, item.path, nil)
		request.AddCookie(session)
		request.AddCookie(csrfCookie)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		body := recorder.Body.String()
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s response = %d", item.path, recorder.Code)
		}
		for _, want := range item.want {
			if !strings.Contains(body, want) {
				t.Fatalf("%s does not contain %q", item.path, want)
			}
		}
	}
}

func TestSystemFormsPersistPythonSettings(t *testing.T) {
	server, _ := testServer(t)
	session, csrfCookie := loginSession(t, server)

	postForm(t, server, session, csrfCookie, "/system/language", url.Values{"language": {"en"}})
	if config := server.configSnapshot(); config.Language != "en" {
		t.Fatalf("language = %q", config.Language)
	}

	postForm(t, server, session, csrfCookie, "/system/hotspot", url.Values{
		"hotspot_ssid": {"NodeAP"}, "hotspot_password": {"123456789"},
	})
	postForm(t, server, session, csrfCookie, "/system", url.Values{
		"timeout_1": {"45"}, "timeout_2": {"600"},
	})

	config := server.configSnapshot()
	if config.HotspotSSID != "NodeAP" || config.HotspotPassword != "123456789" ||
		config.LoadTimeout1 != 45 || config.LoadTimeout2 != 600 {
		t.Fatalf("updated config = %#v", config)
	}
	contents, err := os.ReadFile(config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"language = en", "hotspot_ssid = NodeAP", "hotspot_password = 123456789",
		"timeout_1 = 45", "timeout_2 = 600",
	} {
		if !strings.Contains(string(contents), want) {
			t.Fatalf("config file does not contain %q:\n%s", want, contents)
		}
	}
}

func TestDashboardWidgetManagerRoutesPersistLayout(t *testing.T) {
	server, _ := testServer(t)
	session, csrfCookie := loginSession(t, server)

	postForm(t, server, session, csrfCookie, "/system/dashboard-widgets/cpu-temp/add", nil)
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(session)
	request.AddCookie(csrfCookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Температура CPU") {
		t.Fatalf("dashboard after add = %d %s", recorder.Code, recorder.Body.String())
	}

	postForm(t, server, session, csrfCookie, "/system/dashboard-widgets/ups-main/remove", nil)
	contents, err := os.ReadFile(server.configSnapshot().DashboardWidgets)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "ups-main") || !strings.Contains(string(contents), "cpu-temp") {
		t.Fatalf("layout file = %s", contents)
	}
}

func TestWidgetUploadServeAndDelete(t *testing.T) {
	server, _ := testServer(t)
	session, csrfCookie := loginSession(t, server)
	body, contentType := widgetUploadBody(t, csrfCookie.Value)
	request := httptest.NewRequest(http.MethodPost, "/system/widgets", body)
	request.Header.Set("Content-Type", contentType)
	request.AddCookie(session)
	request.AddCookie(csrfCookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("upload response = %d %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/widgets/user-my-widget/style.css", nil)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), ".ups-widget") {
		t.Fatalf("widget CSS response = %d %s", recorder.Code, recorder.Body.String())
	}

	postForm(t, server, session, csrfCookie, "/system/widgets/user-my-widget/delete", nil)
	request = httptest.NewRequest(http.MethodGet, "/widgets/user-my-widget/style.css", nil)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("deleted widget CSS response = %d", recorder.Code)
	}
}

func TestLegacyAPIDataShapeMatchesFlaskWidgets(t *testing.T) {
	server, _ := testServer(t)
	session, csrfCookie := loginSession(t, server)

	request := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	request.AddCookie(session)
	request.AddCookie(csrfCookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `"connected_ssid_label":"MyRouter"`) ||
		!strings.Contains(body, `"output_voltage_label":"18.96V"`) {
		t.Fatalf("legacy API response = %d %s", recorder.Code, body)
	}
}

func TestWiFiConnectForwardsConfiguredIPv6Mode(t *testing.T) {
	config := secureTestConfig()
	config.AuthMode = "env"
	config.PortalUsername = "admin"
	config.PortalPassword = "secret"
	config.WiFiIPv6 = "auto"
	server, helper := testServerWithConfig(t, config)
	session, csrfCookie := loginSession(t, server)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/wifi/connect",
		strings.NewReader(`{"ssid":"MyRouter","password":"secret"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrfCookie.Value)
	request.AddCookie(session)
	request.AddCookie(csrfCookie)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("connect response = %d %s", recorder.Code, recorder.Body.String())
	}

	helper.mu.Lock()
	defer helper.mu.Unlock()
	for _, call := range helper.calls {
		if call.action == "wifi.connect" {
			if actual := call.params["ipv6"]; actual != "auto" {
				t.Fatalf("helper ipv6 param = %#v, want auto", actual)
			}
			return
		}
	}
	t.Fatal("wifi.connect helper call was not made")
}

func TestIntegrationEndpointsPreservePythonShapeWithoutSession(t *testing.T) {
	server, _ := testServer(t)

	for _, item := range []struct {
		path string
		want string
	}{
		{"/api/integrations/metrics", `"schema":"ups-pi-node.metrics.v1"`},
		{"/api/integrations/zabbix", `"ups.output.current_ma":757`},
		{"/api/integrations/zabbix/discovery", `{#METRIC_KEY}`},
		{"/api/integrations/home-assistant", `"schema":"ups-pi-node.home-assistant.v1"`},
	} {
		request := httptest.NewRequest(http.MethodGet, item.path, nil)
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), item.want) {
			t.Fatalf("%s response = %d %s", item.path, recorder.Code, recorder.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/integrations/zabbix/ups.battery.percent", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "84\n" ||
		recorder.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("zabbix metric response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestIntegrationTokenGateMatchesPythonAccessPattern(t *testing.T) {
	config := secureTestConfig()
	config.AuthMode = "env"
	config.PortalUsername = "admin"
	config.PortalPassword = "secret"
	config.IntegrationsToken = "metrics-token"
	server, _ := testServerWithConfig(t, config)

	request := httptest.NewRequest(http.MethodGet, "/api/integrations/metrics", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing token response = %d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/integrations/metrics", nil)
	request.Header.Set("X-UPS-PI-NODE-Token", "metrics-token")
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("header token response = %d %s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/integrations/zabbix/ups.ac_present?token=metrics-token", nil)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "1\n" {
		t.Fatalf("query token response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestLoginRejectsMissingCSRFAndWrongPassword(t *testing.T) {
	server, _ := testServer(t)
	form := url.Values{"username": {"admin"}, "password": {"secret"}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF response = %d", recorder.Code)
	}

	csrfCookie, token := csrfFromLogin(t, server)
	form = url.Values{"username": {"admin"}, "password": {"wrong"}, "csrf_token": {token}}
	request = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(csrfCookie)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password response = %d", recorder.Code)
	}
}

func TestHealthRequiresSuccessfulHelperCollection(t *testing.T) {
	server, helper := testServer(t)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"ok"`) {
		t.Fatalf("healthy response = %d %s", recorder.Code, recorder.Body.String())
	}

	helper.mu.Lock()
	helper.errors = map[string]error{"ups.status": context.DeadlineExceeded}
	helper.mu.Unlock()
	server.hub.refresh(context.Background())
	request = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable ||
		!strings.Contains(recorder.Body.String(), `"status":"degraded"`) {
		t.Fatalf("degraded response = %d %s", recorder.Code, recorder.Body.String())
	}
}
