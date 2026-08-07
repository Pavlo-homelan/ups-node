package webapp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ups-pi-node/internal/helperclient"
)

const (
	sessionCookieName = "ups_pi_node_go"
	csrfCookieName    = "ups_pi_node_csrf"
)

type security struct {
	secret []byte
	ttl    time.Duration
	secure bool
	now    func() time.Time
}

type sessionClaims struct {
	Username string `json:"u"`
	Expires  int64  `json:"e"`
	Nonce    string `json:"n"`
}

func newSecurity(config Config) *security {
	return &security{
		secret: append([]byte(nil), config.SessionSecret...),
		ttl:    config.SessionTTL, secure: config.SecureCookies, now: time.Now,
	}
}

func (security *security) setSession(w http.ResponseWriter, username string) error {
	nonce, err := randomToken(18)
	if err != nil {
		return err
	}
	claims := sessionClaims{Username: username, Expires: security.now().Add(security.ttl).Unix(), Nonce: nonce}
	payload, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	value := security.seal("session", payload)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: value, Path: "/", MaxAge: int(security.ttl.Seconds()),
		HttpOnly: true, Secure: security.secure, SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func (security *security) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: security.secure, SameSite: http.SameSiteStrictMode,
	})
}

func (security *security) session(r *http.Request) (sessionClaims, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return sessionClaims{}, false
	}
	payload, ok := security.open("session", cookie.Value)
	if !ok {
		return sessionClaims{}, false
	}
	var claims sessionClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Username == "" ||
		claims.Expires <= security.now().Unix() || claims.Nonce == "" {
		return sessionClaims{}, false
	}
	return claims, true
}

func (security *security) csrfFor(w http.ResponseWriter, r *http.Request) (string, error) {
	if cookie, err := r.Cookie(csrfCookieName); err == nil {
		if _, ok := security.open("csrf", cookie.Value); ok {
			return cookie.Value, nil
		}
	}
	random, err := randomToken(32)
	if err != nil {
		return "", err
	}
	token := security.seal("csrf", []byte(random))
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: token, Path: "/", MaxAge: int(security.ttl.Seconds()),
		HttpOnly: true, Secure: security.secure, SameSite: http.SameSiteStrictMode,
	})
	return token, nil
}

func (security *security) verifyCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return false
	}
	supplied := r.Header.Get("X-CSRF-Token")
	if supplied == "" {
		supplied = r.FormValue("csrf_token")
	}
	if supplied == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(supplied)) != 1 {
		return false
	}
	_, ok := security.open("csrf", supplied)
	return ok
}

func (security *security) seal(purpose string, payload []byte) string {
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, security.secret)
	mac.Write([]byte(purpose))
	mac.Write([]byte{0})
	mac.Write([]byte(encoded))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + signature
}

func (security *security) open(purpose string, value string) ([]byte, bool) {
	encoded, suppliedSignature, ok := strings.Cut(value, ".")
	if !ok || encoded == "" || suppliedSignature == "" {
		return nil, false
	}
	mac := hmac.New(sha256.New, security.secret)
	mac.Write([]byte(purpose))
	mac.Write([]byte{0})
	mac.Write([]byte(encoded))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(suppliedSignature)) != 1 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	return payload, err == nil
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

type authenticator struct {
	config Config
	helper helperclient.Caller
}

func (auth authenticator) Authenticate(ctx context.Context, username string, password string) (bool, error) {
	switch auth.config.AuthMode {
	case "pam":
		var response struct {
			Authenticated bool `json:"authenticated"`
		}
		err := auth.helper.Call(ctx, "auth.pam", map[string]any{
			"username": username, "password": password, "service": auth.config.PAMService,
		}, &response)
		return response.Authenticated, err
	case "env":
		usernameOK := subtle.ConstantTimeCompare([]byte(username), []byte(auth.config.PortalUsername)) == 1
		passwordOK := subtle.ConstantTimeCompare([]byte(password), []byte(auth.config.PortalPassword)) == 1
		return usernameOK && passwordOK, nil
	case "mock":
		return username != "" && password != "", nil
	default:
		return false, fmt.Errorf("unsupported authentication mode")
	}
}
