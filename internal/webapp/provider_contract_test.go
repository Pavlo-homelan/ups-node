package webapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderTelemetryIsSeparateTokenProtectedReadOnlyContract(t *testing.T) {
	config := secureTestConfig()
	config.ProviderToken = strings.Repeat("h", 32)
	server, _ := testServerWithConfig(t, config)

	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/provider/v1/telemetry", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/provider/v1/telemetry", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("h", 32))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d, cache=%q", response.Code, response.Header().Get("Cache-Control"))
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["schema"] != providerTelemetrySchema || payload["nodeId"] != config.NodeID || payload["generation"] == "" {
		t.Fatalf("payload = %#v", payload)
	}
	for _, forbidden := range []string{"wifi", "ssid", "ip_address", "portal", "relay", "helper"} {
		if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
			t.Fatalf("Provider payload exposes %q", forbidden)
		}
	}

	post := httptest.NewRecorder()
	postRequest := httptest.NewRequest(http.MethodPost, "/api/provider/v1/telemetry", nil)
	postRequest.Header.Set("Authorization", "Bearer "+strings.Repeat("h", 32))
	server.Handler().ServeHTTP(post, postRequest)
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d", post.Code)
	}
}

func TestProviderTelemetryIsAbsentUntilExplicitlyConfigured(t *testing.T) {
	server, _ := testServer(t)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/provider/v1/telemetry", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}
