package webapp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	if recorder.Code != http.StatusOK || !strings.Contains(body, "Акумулятор") ||
		!strings.Contains(body, "Вихід 19 В") || !strings.Contains(body, "MyRouter") {
		t.Fatalf("dashboard response = %d", recorder.Code)
	}
	for _, phrase := range []string{
		"LIVE POWER MAP",
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
