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
	mux.HandleFunc("/api/alerts", s.authMiddleware(s.handleAPIAlerts))

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

	// Build host status info for project health
	hostStatusInfo := make(map[int64]storage.HostStatusInfo)
	for id, st := range statuses {
		hostStatusInfo[id] = storage.HostStatusInfo{ConsecutiveFails: st.ConsecutiveFails}
	}

	// Compute health for each project
	type ProjectWithHealth struct {
		storage.Project
		Health string
	}

	projectsWithHealth := make([]ProjectWithHealth, len(projects))
	for i, p := range projects {
		health, _ := s.db.GetProjectHealth(r.Context(), p, hostStatusInfo)
		projectsWithHealth[i] = ProjectWithHealth{Project: p, Health: health}
	}

	data := struct {
		Projects []ProjectWithHealth
		Statuses map[int64]*scheduler.HostStatus
	}{
		Projects: projectsWithHealth,
		Statuses: statuses,
	}
	s.render(w, "projects.html", data)
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	alerts, err := s.db.GetAllAlerts(r.Context())
	if err != nil {
		s.logger.Error("failed to get alerts", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "alerts.html", struct{ Alerts []storage.AlertWithHost }{Alerts: alerts})
}

func (s *Server) handleMonitor(w http.ResponseWriter, r *http.Request) {
	statuses := s.sched.GetAllHostStatuses()
	hosts, _ := s.db.GetHosts()
	data := struct {
		Hosts    []storage.Host
		Statuses map[int64]*scheduler.HostStatus
		DBSize   float64
	}{
		Hosts:    hosts,
		Statuses: statuses,
		DBSize:   s.db.DBSizeMB(),
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

	// Support both /api/host/:id/metrics (all metrics) and /api/host/:id/metric/:metric (single metric)
	if len(parts) >= 2 {
		if parts[1] == "metrics" {
			s.handleAPIHostMetrics(w, r, hostID)
			return
		}
		if parts[1] == "metric" && len(parts) >= 3 {
			s.handleAPIHostMetric(w, r, hostID, parts[2])
			return
		}
	}

	http.NotFound(w, r)
}

func timeRangeToFrom(timeRange string) time.Time {
	now := time.Now()
	switch timeRange {
	case "1h":
		return now.Add(-1 * time.Hour)
	case "24h":
		return now.Add(-24 * time.Hour)
	case "7d":
		return now.Add(-7 * 24 * time.Hour)
	case "30d":
		return now.Add(-30 * 24 * time.Hour)
	default: // 6h
		return now.Add(-6 * time.Hour)
	}
}

func resolveResolution(resolution string) string {
	if resolution == "" {
		return "1m"
	}
	return resolution
}

func (s *Server) handleAPIHostMetrics(w http.ResponseWriter, r *http.Request, hostID string) {
	from := timeRangeToFrom(r.URL.Query().Get("timeRange"))
	to := time.Now()
	resolution := resolveResolution(r.URL.Query().Get("resolution"))

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

	metrics, err := s.db.GetAvailableMetrics(r.Context(), h.ID)
	if err != nil {
		s.logger.Error("failed to get available metrics", "host", h.ID, "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	baseMetrics := []string{
		"cpu.user_pct", "cpu.system_pct", "cpu.idle_pct", "cpu.iowait_pct",
		"cpu.load_1m", "cpu.load_5m", "cpu.load_15m",
		"mem.used_bytes", "mem.free_bytes", "mem.available_bytes", "mem.cached_bytes", "mem.total_bytes",
		"disk.used_bytes", "disk.free_bytes", "disk.total_bytes",
		"uptime.seconds",
	}

	allMetrics := append(baseMetrics, metrics...)
	metricSet := make(map[string]bool)
	for _, m := range allMetrics {
		metricSet[m] = true
	}
	uniqueMetrics := make([]string, 0, len(metricSet))
	for m := range metricSet {
		uniqueMetrics = append(uniqueMetrics, m)
	}

	result := make(map[string]interface{})
	for _, m := range uniqueMetrics {
		samples, err := s.db.GetSamples(r.Context(), h.ID, m, from, to, resolution)
		if err != nil {
			s.logger.Error("failed to get samples", "metric", m, "error", err)
			continue
		}
		if len(samples) == 0 {
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

func (s *Server) handleAPIHostMetric(w http.ResponseWriter, r *http.Request, hostID, metric string) {
	from := timeRangeToFrom(r.URL.Query().Get("timeRange"))
	to := time.Now()
	resolution := resolveResolution(r.URL.Query().Get("resolution"))

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

	data := s.getMetricSeries(r.Context(), h.ID, metric, from, to, resolution)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"metrics": map[string]interface{}{metric: data}})
}

func (s *Server) getMetricSeries(ctx context.Context, hostID int64, metric string, from, to time.Time, resolution string) [][2]float64 {
	// Derive percentage metrics from stored byte metrics
	if metric == "mem.used_pct" || metric == "disk.used_pct" {
		usedMetric := strings.Replace(metric, "used_pct", "used_bytes", 1)
		totalMetric := strings.Replace(metric, "used_pct", "total_bytes", 1)
		used, err1 := s.db.GetSamples(ctx, hostID, usedMetric, from, to, resolution)
		total, err2 := s.db.GetSamples(ctx, hostID, totalMetric, from, to, resolution)
		if err1 != nil || err2 != nil {
			return nil
		}
		totalByTS := make(map[int64]float64, len(total))
		for _, t := range total {
			totalByTS[t.Timestamp.Unix()] = t.Value
		}
		data := make([][2]float64, 0, len(used))
		for _, u := range used {
			totalVal, ok := totalByTS[u.Timestamp.Unix()]
			if !ok || totalVal == 0 {
				continue
			}
			data = append(data, [2]float64{float64(u.Timestamp.Unix()), u.Value / totalVal * 100})
		}
		return data
	}

	samples, err := s.db.GetSamples(ctx, hostID, metric, from, to, resolution)
	if err != nil {
		return nil
	}
	data := make([][2]float64, len(samples))
	for i, s := range samples {
		data[i] = [2]float64{float64(s.Timestamp.Unix()), s.Value}
	}

	// Network counters are cumulative; convert to per-second rates.
	if strings.HasSuffix(metric, ".rx_bytes") || strings.HasSuffix(metric, ".tx_bytes") {
		return toRateSeries(data)
	}
	return data
}

func toRateSeries(data [][2]float64) [][2]float64 {
	out := make([][2]float64, 0, len(data))
	for i := 1; i < len(data); i++ {
		dt := data[i][0] - data[i-1][0]
		if dt <= 0 {
			continue
		}
		out = append(out, [2]float64{data[i][0], (data[i][1] - data[i-1][1]) / dt})
	}
	return out
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

	from := timeRangeToFrom(timeRange)
	to := time.Now()
	resolution = resolveResolution(resolution)

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

		data := s.getMetricSeries(r.Context(), h.ID, metric, from, to, resolution)
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

func (s *Server) handleAPIAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		action := r.URL.Query().Get("action")
		alertID := r.URL.Query().Get("id")
		if action == "silence" && alertID != "" {
			durationStr := r.URL.Query().Get("duration")
			duration, err := time.ParseDuration(durationStr)
			if err != nil {
				duration = 1 * time.Hour // default 1 hour
			}
			if err := s.db.SilenceAlert(r.Context(), parseInt64(alertID), duration); err != nil {
				s.logger.Error("failed to silence alert", "error", err)
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if action == "acknowledge" && alertID != "" {
			if err := s.db.AcknowledgeAlert(r.Context(), parseInt64(alertID)); err != nil {
				s.logger.Error("failed to acknowledge alert", "error", err)
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	// GET - list alerts
	hostID := r.URL.Query().Get("host_id")
	if hostID == "" {
		http.Error(w, "host_id required", http.StatusBadRequest)
		return
	}

	alerts, err := s.db.GetAlerts(r.Context(), parseInt64(hostID))
	if err != nil {
		s.logger.Error("failed to get alerts", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(alerts)
}

func parseInt64(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
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
