package webapp

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ups-pi-node/internal/helperclient"
)

//go:embed assets/templates/*.html assets/static/* assets/static/css/* assets/static/js/*
var webAssets embed.FS

type Server struct {
	config       Config
	helper       helperclient.Caller
	security     *security
	auth         authenticator
	hub          *statusHub
	templates    *template.Template
	handler      http.Handler
	loginLimiter *attemptWindow
	streams      *streamLimiter
	logger       *slog.Logger
}

type pageData struct {
	Language      string
	NodeID        string
	Username      string
	CSRF          string
	Error         string
	InitialStatus string
}

func NewServer(config Config, helper helperclient.Caller, logger *slog.Logger) (*Server, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if helper == nil {
		return nil, fmt.Errorf("system helper client is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	templates, err := template.ParseFS(webAssets, "assets/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse embedded web templates: %w", err)
	}
	server := &Server{
		config: config, helper: helper, security: newSecurity(config),
		auth: authenticator{config: config, helper: helper},
		hub:  newStatusHub(config, helper), templates: templates,
		loginLimiter: newAttemptWindow(), streams: newStreamLimiter(16, 2), logger: logger,
	}
	handler, err := server.routes()
	if err != nil {
		return nil, err
	}
	server.handler = server.securityHeaders(handler)
	return server, nil
}

func (server *Server) Handler() http.Handler { return server.handler }

func (server *Server) RunStatusHub(ctx context.Context) { server.hub.Run(ctx) }

func (server *Server) routes() (http.Handler, error) {
	staticRoot, err := fs.Sub(webAssets, "assets/static")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", cacheAssets(http.FileServer(http.FS(staticRoot)))))
	mux.HandleFunc("GET /assets/fonts/ubuntu-regular.ttf", server.font(false))
	mux.HandleFunc("GET /assets/fonts/ubuntu-bold.ttf", server.font(true))
	mux.HandleFunc("GET /", server.handleRoot)
	mux.HandleFunc("GET /login", server.handleLoginPage)
	mux.HandleFunc("POST /login", server.handleLogin)
	mux.HandleFunc("POST /logout", server.requirePage(server.handleLogout))
	mux.HandleFunc("GET /dashboard", server.requirePage(server.handleDashboard))
	mux.HandleFunc("GET /api/v1/status", server.requireAPI(server.handleStatus))
	mux.HandleFunc("GET /api/v1/events", server.requireAPI(server.handleEvents))
	mux.HandleFunc("POST /api/v1/wifi/scan", server.requireAPI(server.requireCSRF(server.handleWiFiScan)))
	mux.HandleFunc("POST /api/v1/wifi/connect", server.requireAPI(server.requireCSRF(server.handleWiFiConnect)))
	mux.HandleFunc("GET /api/integrations/metrics", server.handleIntegrationMetrics)
	mux.HandleFunc("GET /api/integrations/zabbix", server.handleZabbixMetrics)
	mux.HandleFunc("GET /api/integrations/zabbix/discovery", server.handleZabbixDiscovery)
	mux.HandleFunc("GET /api/integrations/zabbix/{metric}", server.handleZabbixMetric)
	mux.HandleFunc("GET /api/integrations/home-assistant", server.handleHomeAssistantDiscovery)
	mux.HandleFunc("GET /healthz", server.handleHealth)
	return mux, nil
}

func (server *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if _, ok := server.security.session(r); ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (server *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := server.security.session(r); ok {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	server.renderLogin(w, r, "", http.StatusOK)
}

func (server *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := r.ParseForm(); err != nil {
		server.renderLogin(w, r, "Некоректний запит.", http.StatusBadRequest)
		return
	}
	if !server.security.verifyCSRF(r) {
		server.renderLogin(w, r, "Сторінка застаріла. Оновіть її та спробуйте ще раз.", http.StatusForbidden)
		return
	}
	client := clientIP(r)
	if !server.loginLimiter.Allow(client, 5, 5*time.Minute) {
		w.Header().Set("Retry-After", "300")
		server.renderLogin(w, r, "Забагато спроб входу. Зачекайте п’ять хвилин.", http.StatusTooManyRequests)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if username == "" || password == "" || len(username) > 128 || len(password) > 4096 {
		server.renderLogin(w, r, "Вкажіть коректні ім’я користувача та пароль.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	authenticated, err := server.auth.Authenticate(ctx, username, password)
	if err != nil {
		server.logger.Warn("authentication backend unavailable", "error", err)
		server.renderLogin(w, r, "Служба автентифікації тимчасово недоступна.", http.StatusServiceUnavailable)
		return
	}
	if !authenticated {
		server.renderLogin(w, r, "Невірне ім’я користувача або пароль.", http.StatusUnauthorized)
		return
	}
	if err := server.security.setSession(w, username); err != nil {
		http.Error(w, "session unavailable", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (server *Server) renderLogin(w http.ResponseWriter, r *http.Request, message string, status int) {
	csrf, err := server.security.csrfFor(w, r)
	if err != nil {
		http.Error(w, "CSRF unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = server.templates.ExecuteTemplate(w, "login.html", pageData{
		Language: server.config.Language, NodeID: server.config.NodeID, CSRF: csrf, Error: message,
	})
}

func (server *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	claims, _ := server.security.session(r)
	csrf, err := server.security.csrfFor(w, r)
	if err != nil {
		http.Error(w, "CSRF unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	initialStatus := "{}"
	if status, ok := server.hub.Current(); ok {
		if encoded, encodeErr := json.Marshal(status); encodeErr == nil {
			initialStatus = string(encoded)
		}
	}
	_ = server.templates.ExecuteTemplate(w, "dashboard.html", pageData{
		Language: server.config.Language, NodeID: server.config.NodeID,
		Username: claims.Username, CSRF: csrf, InitialStatus: initialStatus,
	})
}

func (server *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !server.security.verifyCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	server.security.clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (server *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status, ok := server.hub.Current()
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "status is initializing"})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (server *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	release, ok := server.streams.Acquire(clientIP(r))
	if !ok {
		w.Header().Set("Retry-After", "10")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many event streams"})
		return
	}
	defer release()
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, "retry: 2500\n\n")
	flusher.Flush()
	events, unsubscribe := server.hub.Subscribe()
	defer unsubscribe()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case event, open := <-events:
			if !open {
				return
			}
			payload, err := json.Marshal(event.Status)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: status\ndata: %s\n\n", event.Sequence, payload)
			flusher.Flush()
		}
	}
}

func (server *Server) handleWiFiScan(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var result map[string]any
	err := server.helper.Call(ctx, "wifi.scan", map[string]any{"interface": server.config.WiFiInterface}, &result)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (server *Server) handleWiFiConnect(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var request struct {
		SSID     string `json:"ssid"`
		Password string `json:"password"`
		Hidden   bool   `json:"hidden"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.SSID) == "" ||
		len(request.SSID) > 256 || len(request.Password) > 4096 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Wi-Fi request"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	var result map[string]any
	err := server.helper.Call(ctx, "wifi.connect", map[string]any{
		"interface": server.config.WiFiInterface, "ssid": strings.TrimSpace(request.SSID),
		"password": request.Password, "hidden": request.Hidden,
	}, &result)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (server *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	status := "initializing"
	code := http.StatusServiceUnavailable
	if current, ok := server.hub.Current(); ok && current.Status == "ok" {
		status = "ok"
		code = http.StatusOK
	} else if ok {
		status = "degraded"
	}
	writeJSON(w, code, map[string]string{"status": status})
}

func (server *Server) requirePage(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := server.security.session(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (server *Server) requireAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := server.security.session(r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		next(w, r)
	}
}

func (server *Server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !server.security.verifyCSRF(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid CSRF token"})
			return
		}
		next(w, r)
	}
}

func (server *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func (server *Server) font(bold bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := "/usr/share/fonts/truetype/ubuntu/Ubuntu-R.ttf"
		if bold {
			path = "/usr/share/fonts/truetype/ubuntu/Ubuntu-B.ttf"
		}
		file, err := os.Open(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "font/ttf")
		w.Header().Set("Cache-Control", "public, max-age=604800")
		http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
	}
}

func cacheAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	parsed := net.ParseIP(host)
	if parsed != nil && parsed.IsLoopback() {
		if forwarded := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); forwarded != nil {
			return forwarded.String()
		}
	}
	if parsed != nil {
		return parsed.String()
	}
	return host
}
