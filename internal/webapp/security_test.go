package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func secureTestConfig() Config {
	config := DefaultConfig()
	config.SessionSecret = []byte("0123456789abcdef0123456789abcdef")
	return config
}

func TestSignedSessionRejectsTamperingAndExpiry(t *testing.T) {
	security := newSecurity(secureTestConfig())
	now := time.Unix(1_800_000_000, 0)
	security.now = func() time.Time { return now }
	recorder := httptest.NewRecorder()
	if err := security.setSession(recorder, "admin"); err != nil {
		t.Fatal(err)
	}
	response := recorder.Result()
	var sessionCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == sessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %#v", sessionCookie)
	}
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(sessionCookie)
	claims, ok := security.session(request)
	if !ok || claims.Username != "admin" {
		t.Fatalf("claims = %#v, ok=%v", claims, ok)
	}
	tampered := *sessionCookie
	tampered.Value += "x"
	request = httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(&tampered)
	if _, ok := security.session(request); ok {
		t.Fatal("tampered session was accepted")
	}
	security.now = func() time.Time { return now.Add(security.ttl + time.Second) }
	request = httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(sessionCookie)
	if _, ok := security.session(request); ok {
		t.Fatal("expired session was accepted")
	}
}

func TestCSRFMustMatchSignedHttpOnlyCookie(t *testing.T) {
	security := newSecurity(secureTestConfig())
	get := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	recorder := httptest.NewRecorder()
	token, err := security.csrfFor(recorder, get)
	if err != nil {
		t.Fatal(err)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Value != token {
		t.Fatalf("CSRF cookie = %#v", cookies)
	}
	post := httptest.NewRequest(http.MethodPost, "/api/v1/wifi/scan", nil)
	post.AddCookie(cookies[0])
	post.Header.Set("X-CSRF-Token", token)
	if !security.verifyCSRF(post) {
		t.Fatal("valid CSRF pair was rejected")
	}
	post.Header.Set("X-CSRF-Token", token+"x")
	if security.verifyCSRF(post) {
		t.Fatal("mismatched CSRF token was accepted")
	}
}
