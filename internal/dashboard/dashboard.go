package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/scheduler"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

type Server struct {
	password     string
	db           *storage.DB
	sched        *scheduler.Scheduler
	logger       *slog.Logger
	server       *http.Server
	sessions     map[string]time.Time
	templates    *template.Template
	cookieSecure bool
}

func NewServer(password string, db *storage.DB, sched *scheduler.Scheduler, logger *slog.Logger, cookieSecure bool) *Server {
	tmpl := template.Must(template.New("").ParseGlob("internal/dashboard/templates/*.html"))
	return &Server{
		password:     password,
		db:           db,
		sched:        sched,
		logger:       logger,
		sessions:     make(map[string]time.Time),
		templates:    tmpl,
		cookieSecure: cookieSecure,
	}
}

func (s *Server) Run(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/", s.authMiddleware(s.handleIndex))
	mux.HandleFunc("/host/", s.authMiddleware(s.handleHost))
	mux.HandleFunc("/compare", s.authMiddleware(s.handleCompare))
	mux.HandleFunc("/projects", s.authMiddleware(s.handleProjects))
	mux.HandleFunc("/alerts", s.authMiddleware(s.handleAlerts))
	mux.HandleFunc("/monitor", s.authMiddleware(s.handleMonitor))
	mux.HandleFunc("/api/", s.authMiddleware(s.handleAPI))
	mux.HandleFunc("/api/hosts", s.authMiddleware(s.handleAPIHosts))
	mux.HandleFunc("/api/host/", s.authMiddleware(s.handleAPIHost))
	mux.HandleFunc("/api/compare", s.authMiddleware(s.handleAPICompare))
	mux.HandleFunc("/api/projects", s.authMiddleware(s.handleAPIProjects))

	s.server = &http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	go func() {
		s.logger.Info("dashboard server starting", "addr", ":8080")
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("dashboard server error", "error", err)
		}
	}()

	<-ctx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.server.Shutdown(ctx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.render(w, "login.html", nil)
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}
		password := r.FormValue("password")
		if password == s.password {
			sessionID := generateSessionID()
			s.sessions[sessionID] = time.Now().Add(24 * time.Hour)
			http.SetCookie(w, &http.Cookie{
				Name:     "session",
				Value:    sessionID,
				Path:     "/",
				HttpOnly: true,
				Secure:   s.cookieSecure,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   24 * 60 * 60,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		s.render(w, "login.html", map[string]string{"Error": "Invalid password"})
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie("session")
	if cookie != nil {
		delete(s.sessions, cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cookieSecure,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/login" || r.URL.Path == "/logout" {
			next(w, r)
			return
		}
		cookie, err := r.Cookie("session")
		if err != nil || !s.isValidSession(cookie.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) isValidSession(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	expiry, ok := s.sessions[sessionID]
	if !ok || time.Now().After(expiry) {
		delete(s.sessions, sessionID)
		return false
	}
	return true
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.db.GetHosts()
	if err != nil {
		s.logger.Error("failed to get hosts", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	statuses := s.sched.GetAllHostStatuses()
	data := struct {
		Hosts    []storage.Host
		Statuses map[int64]*scheduler.HostStatus
	}{
		Hosts:    hosts,
		Statuses: statuses,
	}
	s.render(w, "index.html", data)
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.db.GetHosts()
	if err != nil {
		s.logger.Error("failed to get hosts", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/host/")
	if path == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	var host *storage.Host
	for _, h := range hosts {
		if fmt.Sprintf("%d", h.ID) == path || h.Name == path {
			host = &h
			break
		}
	}
	if host == nil {
		http.NotFound(w, r)
		return
	}

	s.render(w, "host.html", host)
}

func (s *Server) handleCompare(w http.ResponseWriter, r *http.Request) {
	s.render(w, "compare.html", nil)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.GetProjects(r.Context())
	if err != nil {
		s.logger.Error("failed to get projects", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	statuses := s.sched.GetAllHostStatuses()
	data := struct {
		Projects []storage.Project
		Statuses map[int64]*scheduler.HostStatus
	}{
		Projects: projects,
		Statuses: statuses,
	}
	s.render(w, "projects.html", data)
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	s.render(w, "alerts.html", nil)
}

func (s *Server) handleMonitor(w http.ResponseWriter, r *http.Request) {
	statuses := s.sched.GetAllHostStatuses()
	hosts, _ := s.db.GetHosts()
	data := struct {
		Hosts    []storage.Host
		Statuses map[int64]*scheduler.HostStatus
	}{
		Hosts:    hosts,
		Statuses: statuses,
	}
	s.render(w, "monitor.html", data)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleAPIProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.db.GetProjects(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(projects)
}

func (s *Server) handleAPIHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.db.GetHosts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type HostInfo struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}

	result := make([]HostInfo, len(hosts))
	for i, h := range hosts {
		result[i] = HostInfo{ID: h.ID, Name: h.Name}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleAPIHost(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/host/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 {
		http.NotFound(w, r)
		return
	}

	hostID := parts[0]

	if len(parts) == 1 {
		hosts, _ := s.db.GetHosts()
		for _, h := range hosts {
			if fmt.Sprintf("%d", h.ID) == hostID || h.Name == hostID {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(h)
				return
			}
		}
		http.NotFound(w, r)
		return
	}

	if len(parts) >= 3 && parts[1] == "metrics" {
		s.handleAPIHostMetrics(w, r, hostID)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleAPIHostMetrics(w http.ResponseWriter, r *http.Request, hostID string) {
	timeRange := r.URL.Query().Get("timeRange")
	resolution := r.URL.Query().Get("resolution")

	var from, to time.Time
	now := time.Now()

	switch timeRange {
	case "1h":
		from = now.Add(-1 * time.Hour)
	case "6h":
		from = now.Add(-6 * time.Hour)
	case "24h":
		from = now.Add(-24 * time.Hour)
	case "7d":
		from = now.Add(-7 * 24 * time.Hour)
	case "30d":
		from = now.Add(-30 * 24 * time.Hour)
	default:
		from = now.Add(-6 * time.Hour)
	}
	to = now

	if resolution == "" {
		resolution = "1m"
	}

	hosts, _ := s.db.GetHosts()
	var h *storage.Host
	for _, host := range hosts {
		if fmt.Sprintf("%d", host.ID) == hostID || host.Name == hostID {
			h = &host
			break
		}
	}

	if h == nil {
		http.NotFound(w, r)
		return
	}

	metrics := []string{
		"cpu.user_pct", "cpu.system_pct", "cpu.idle_pct", "cpu.iowait_pct",
		"mem.used_bytes", "mem.free_bytes", "mem.cached_bytes",
		"disk.used_bytes", "disk.free_bytes",
		"net.eth0.rx_bytes", "net.eth0.tx_bytes",
	}

	result := make(map[string]interface{})
	for _, m := range metrics {
		samples, err := s.db.GetSamples(r.Context(), h.ID, m, from, to, resolution)
		if err != nil {
			s.logger.Error("failed to get samples", "metric", m, "error", err)
			continue
		}

		data := make([][2]float64, len(samples))
		for i, s := range samples {
			data[i] = [2]float64{float64(s.Timestamp.Unix()), s.Value}
		}
		result[m] = data
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"metrics": result})
}

func (s *Server) handleAPICompare(w http.ResponseWriter, r *http.Request) {
	metric := r.URL.Query().Get("metric")
	hostsParam := r.URL.Query().Get("hosts")
	timeRange := r.URL.Query().Get("timeRange")
	resolution := r.URL.Query().Get("resolution")

	if metric == "" || hostsParam == "" {
		http.Error(w, "metric and hosts required", http.StatusBadRequest)
		return
	}

	hostIDs := strings.Split(hostsParam, ",")

	var from, to time.Time
	now := time.Now()

	switch timeRange {
	case "1h":
		from = now.Add(-1 * time.Hour)
	case "6h":
		from = now.Add(-6 * time.Hour)
	case "24h":
		from = now.Add(-24 * time.Hour)
	case "7d":
		from = now.Add(-7 * 24 * time.Hour)
	default:
		from = now.Add(-6 * time.Hour)
	}
	to = now

	if resolution == "" {
		resolution = "1m"
	}

	type Series struct {
		Host string       `json:"host"`
		Data [][2]float64 `json:"data"`
	}

	var series []Series
	for _, hostID := range hostIDs {
		hosts, _ := s.db.GetHosts()
		var h *storage.Host
		for _, host := range hosts {
			if fmt.Sprintf("%d", host.ID) == hostID || host.Name == hostID {
				h = &host
				break
			}
		}
		if h == nil {
			continue
		}

		samples, err := s.db.GetSamples(r.Context(), h.ID, metric, from, to, resolution)
		if err != nil {
			continue
		}

		data := make([][2]float64, len(samples))
		for i, s := range samples {
			data[i] = [2]float64{float64(s.Timestamp.Unix()), s.Value}
		}
		series = append(series, Series{Host: h.Name, Data: data})
	}

	labels := []string{}
	if len(series) > 0 && len(series[0].Data) > 0 {
		for _, d := range series[0].Data {
			labels = append(labels, time.Unix(int64(d[0]), 0).Format("15:04"))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"labels": labels,
		"series": series,
	})
}

func (s *Server) render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("template render failed", "template", name, "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
	}
}

func generateSessionID() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
