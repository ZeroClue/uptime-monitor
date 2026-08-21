package dashboard

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/scheduler"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

//go:embed templates static
var embeddedFiles embed.FS

type Server struct {
	password     string
	db           *storage.DB
	sched        *scheduler.Scheduler
	logger       *slog.Logger
	server       *http.Server
	sessions     map[string]time.Time
	templates    map[string]*template.Template
	static       http.Handler
	cookieSecure bool
}

func NewServer(password string, db *storage.DB, sched *scheduler.Scheduler, logger *slog.Logger, cookieSecure bool) *Server {
	s := &Server{
		password:     password,
		db:           db,
		sched:        sched,
		logger:       logger,
		sessions:     make(map[string]time.Time),
		cookieSecure: cookieSecure,
	}
	s.loadTemplates()
	staticSub, err := fs.Sub(embeddedFiles, "static")
	if err != nil {
		logger.Error("failed to open embedded static dir", "error", err)
		staticSub = nil
	}
	if staticSub != nil {
		s.static = http.StripPrefix("/static/", http.FileServer(http.FS(staticSub)))
	}
	return s
}

func (s *Server) loadTemplates() {
	pages := []string{"index", "host", "compare", "projects", "alerts", "monitor", "alerts_history", "alerts_config", "hosts_config"}
	tmpls := make(map[string]*template.Template, len(pages)+1)
	for _, p := range pages {
		t, err := template.ParseFS(embeddedFiles, "templates/base.html", "templates/"+p+".html")
		if err != nil {
			s.logger.Error("failed to parse template", "page", p, "error", err)
			continue
		}
		tmpls[p+".html"] = t
	}
	t, err := template.ParseFS(embeddedFiles, "templates/login.html")
	if err != nil {
		s.logger.Error("failed to parse login template", "error", err)
	} else {
		tmpls["login.html"] = t
	}
	s.templates = tmpls
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
	mux.HandleFunc("/alerts/history", s.authMiddleware(s.handleAlertsHistory))
	mux.HandleFunc("/alerts/config", s.authMiddleware(s.handleAlertsConfig))
	mux.HandleFunc("/hosts/config", s.authMiddleware(s.handleHostsConfig))
	mux.HandleFunc("/monitor", s.authMiddleware(s.handleMonitor))
	mux.HandleFunc("/api/", s.authMiddleware(s.handleAPI))
	mux.HandleFunc("/api/hosts", s.authMiddleware(s.handleAPIHosts))
	mux.HandleFunc("/api/hosts/", s.authMiddleware(s.handleAPIHosts))
	mux.HandleFunc("/api/hosts/status", s.authMiddleware(s.handleAPIHostsStatus))
	mux.HandleFunc("/api/host/", s.authMiddleware(s.handleAPIHost))
	mux.HandleFunc("/api/compare", s.authMiddleware(s.handleAPICompare))
	mux.HandleFunc("/api/alerts", s.authMiddleware(s.handleAPIAlerts))
	mux.HandleFunc("/api/alert-rules", s.authMiddleware(s.handleAPIAlertRules))
	mux.HandleFunc("/api/alert-rules/", s.authMiddleware(s.handleAPIAlertRuleByID))
	mux.HandleFunc("/api/notification-channels", s.authMiddleware(s.handleAPINotificationChannels))
	mux.HandleFunc("/api/notification-channels/", s.authMiddleware(s.handleAPINotificationChannelByID))
	mux.HandleFunc("/api/api-tokens", s.authMiddleware(s.handleAPIAPITokens))
	mux.HandleFunc("/api/api-tokens/", s.authMiddleware(s.handleAPIAPITokenByID))
	mux.HandleFunc("/api/projects", s.authMiddleware(s.handleAPIProjects))
	mux.HandleFunc("/api/projects/", s.authMiddleware(s.handleAPIProjectByID))
	mux.HandleFunc("/api/monitor", s.authMiddleware(s.handleAPIMonitor))
	if s.static != nil {
		mux.Handle("/static/", s.static)
	}

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

func (s *Server) projectMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract project ID from header, query param, or cookie
		projectIDStr := r.Header.Get("X-Project-ID")
		if projectIDStr == "" {
			projectIDStr = r.URL.Query().Get("project_id")
		}
		if projectIDStr == "" {
			if cookie, err := r.Cookie("project_id"); err == nil {
				projectIDStr = cookie.Value
			}
		}

		var projectID int64
		if projectIDStr != "" {
			if id, err := strconv.ParseInt(projectIDStr, 10, 64); err == nil {
				projectID = id
			}
		}

		// If no project specified, try to get default project
		if projectID == 0 {
			projects, err := s.db.GetProjects(r.Context())
			if err == nil {
				for _, p := range projects {
					if p.IsDefault {
						projectID = p.ID
						break
					}
				}
				// Fallback to first project
				if projectID == 0 && len(projects) > 0 {
					projectID = projects[0].ID
				}
			}
		}

		ctx := context.WithValue(r.Context(), "project_id", projectID)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	projectID := r.Context().Value("project_id")
	hosts, err := s.db.GetHostsByProject(r.Context(), projectID)
	if err != nil {
		s.logger.Error("failed to get hosts", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	statuses := s.sched.GetAllHostStatuses()
	type hostBrief struct {
		ID   int64    `json:"id"`
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	briefs := make([]hostBrief, 0, len(hosts))
	for _, h := range hosts {
		briefs = append(briefs, hostBrief{ID: h.ID, Name: h.Name, Tags: h.Tags})
	}
	hostsJSON, err := json.Marshal(briefs)
	if err != nil {
		s.logger.Error("failed to marshal hosts", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	data := struct {
		Hosts     []storage.Host
		HostsJSON template.JS
		Statuses  map[int64]*scheduler.HostStatus
	}{
		Hosts:     hosts,
		HostsJSON: template.JS(hostsJSON),
		Statuses:  statuses,
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

func (s *Server) handleAlertsHistory(w http.ResponseWriter, r *http.Request) {
	alerts, err := s.db.GetAllAlerts(r.Context())
	if err != nil {
		s.logger.Error("failed to get alerts for history", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "alerts_history.html", struct{ Alerts []storage.AlertWithHost }{Alerts: alerts})
}

func (s *Server) handleAlertsConfig(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.db.GetHosts()
	if err != nil {
		s.logger.Error("failed to get hosts for config", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "alerts_config.html", struct{ Hosts []storage.Host }{Hosts: hosts})
}

func (s *Server) handleHostsConfig(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.db.GetHosts()
	if err != nil {
		s.logger.Error("failed to get hosts for config", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "hosts_config.html", struct{ Hosts []storage.Host }{Hosts: hosts})
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
	switch r.Method {
	case http.MethodGet:
		projects, err := s.db.GetProjects(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(projects)
	case http.MethodPost:
		var project storage.Project
		if err := json.NewDecoder(r.Body).Decode(&project); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		id, err := s.db.CreateProject(r.Context(), &project)
		if err != nil {
			s.logger.Error("failed to create project", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		project.ID = id
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(project)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPIProjectByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	id := mustParseInt64(idStr)

	switch r.Method {
	case http.MethodGet:
		project, err := s.db.GetProject(r.Context(), id)
		if err != nil {
			s.logger.Error("failed to get project", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		if project == nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(project)
	case http.MethodPut:
		var project storage.Project
		if err := json.NewDecoder(r.Body).Decode(&project); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		project.ID = id
		if err := s.db.UpdateProject(r.Context(), &project); err != nil {
			s.logger.Error("failed to update project", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(project)
	case http.MethodDelete:
		if err := s.db.DeleteProject(r.Context(), id); err != nil {
			s.logger.Error("failed to delete project", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPIHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAPIHostsGet(w, r)
	case http.MethodPost:
		s.handleAPIHostsPost(w, r)
	case http.MethodPut:
		s.handleAPIHostsPut(w, r)
	case http.MethodDelete:
		s.handleAPIHostsDelete(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPIHostsGet(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	var projectIDPtr *int64
	if projectID != "" {
		if id, err := strconv.ParseInt(projectID, 10, 64); err == nil {
			projectIDPtr = &id
		}
	}
	hosts, err := s.db.GetHostsByProject(r.Context(), projectIDPtr)
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

func (s *Server) handleAPIHostsPost(w http.ResponseWriter, r *http.Request) {
	var host storage.Host
	if err := json.NewDecoder(r.Body).Decode(&host); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	id, err := s.db.CreateHost(r.Context(), &host)
	if err != nil {
		s.logger.Error("failed to create host", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	host.ID = id
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(host)
}

func (s *Server) handleAPIHostsPut(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/hosts/")
	id := mustParseInt64(idStr)

	var host storage.Host
	if err := json.NewDecoder(r.Body).Decode(&host); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	host.ID = id
	if err := s.db.UpdateHost(r.Context(), &host); err != nil {
		s.logger.Error("failed to update host", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(host)
}

func (s *Server) handleAPIHostsDelete(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/hosts/")
	id := mustParseInt64(idStr)

	if err := s.db.DeleteHost(r.Context(), id); err != nil {
		s.logger.Error("failed to delete host", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type HostStatusSummary struct {
	HostID int64    `json:"host_id"`
	Name   string   `json:"name"`
	Status string   `json:"status"`
	CPU    *float64 `json:"cpu_pct"`
	Mem    *float64 `json:"mem_pct"`
	Uptime *float64 `json:"uptime_seconds"`
}

func (s *Server) handleAPIHostsStatus(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.db.GetHosts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	statuses := s.sched.GetAllHostStatuses()

	result := make([]HostStatusSummary, 0, len(hosts))
	for _, h := range hosts {
		summary := HostStatusSummary{
			HostID: h.ID,
			Name:   h.Name,
		}

		if st, ok := statuses[h.ID]; ok {
			switch {
			case st.ConsecutiveFails == 0:
				summary.Status = "ok"
			case st.ConsecutiveFails < 3:
				summary.Status = "warning"
			default:
				summary.Status = "down"
			}
		} else {
			summary.Status = "unknown"
		}

		ctx := r.Context()
		user, err1 := s.db.GetLatestSample(ctx, h.ID, "cpu.user_pct")
		system, err2 := s.db.GetLatestSample(ctx, h.ID, "cpu.system_pct")
		if err1 == nil && err2 == nil && user != nil && system != nil {
			cpu := user.Value + system.Value
			summary.CPU = &cpu
		}
		used, err1 := s.db.GetLatestSample(ctx, h.ID, "mem.used_bytes")
		total, err2 := s.db.GetLatestSample(ctx, h.ID, "mem.total_bytes")
		if err1 == nil && err2 == nil && used != nil && total != nil && total.Value > 0 {
			mem := used.Value / total.Value * 100
			summary.Mem = &mem
		}
		if up, err := s.db.GetLatestSample(ctx, h.ID, "uptime.seconds"); err == nil && up != nil {
			summary.Uptime = &up.Value
		}

		result = append(result, summary)
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

func (s *Server) handleAPIMonitor(w http.ResponseWriter, r *http.Request) {
	statuses := s.sched.GetAllHostStatuses()
	hosts, _ := s.db.GetHosts()

	type hostStatusInfo struct {
		HostID           int64  `json:"host_id"`
		Name             string `json:"name"`
		LastCollector    string `json:"last_collector"`
		LastSuccess      string `json:"last_success"`
		ConsecutiveFails int    `json:"consecutive_fails"`
		LastError        string `json:"last_error"`
	}

	result := make([]hostStatusInfo, 0, len(hosts))
	for _, h := range hosts {
		info := hostStatusInfo{HostID: h.ID, Name: h.Name}
		if st, ok := statuses[h.ID]; ok {
			info.LastCollector = st.LastCollector
			info.LastSuccess = ""
			if !st.LastSuccess.IsZero() {
				info.LastSuccess = st.LastSuccess.Format("2006-01-02 15:04:05")
			}
			info.ConsecutiveFails = st.ConsecutiveFails
			info.LastError = st.LastError
		}
		result = append(result, info)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"db_size_mb": s.db.DBSizeMB(),
		"hosts":      result,
		"interval":   "30s",
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
			if err := s.db.SilenceAlert(r.Context(), mustParseInt64(alertID), duration); err != nil {
				s.logger.Error("failed to silence alert", "error", err)
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if action == "acknowledge" && alertID != "" {
			if err := s.db.AcknowledgeAlert(r.Context(), mustParseInt64(alertID)); err != nil {
				s.logger.Error("failed to acknowledge alert", "error", err)
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		// Bulk actions
		if action == "acknowledge_all" {
			severity := r.URL.Query().Get("severity")
			if err := s.db.AcknowledgeAllAlerts(r.Context(), severity); err != nil {
				s.logger.Error("failed to acknowledge all alerts", "error", err)
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if action == "silence_all" {
			durationStr := r.URL.Query().Get("duration")
			duration, err := time.ParseDuration(durationStr)
			if err != nil {
				duration = 1 * time.Hour
			}
			severity := r.URL.Query().Get("severity")
			if err := s.db.SilenceAllAlerts(r.Context(), severity, duration); err != nil {
				s.logger.Error("failed to silence all alerts", "error", err)
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if action == "delete_all" {
			severity := r.URL.Query().Get("severity")
			if err := s.db.DeleteAllAlerts(r.Context(), severity); err != nil {
				s.logger.Error("failed to delete all alerts", "error", err)
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if action == "silence" && alertID != "" {
			durationStr := r.URL.Query().Get("duration")
			duration, err := time.ParseDuration(durationStr)
			if err != nil {
				duration = 1 * time.Hour // default 1 hour
			}
			if err := s.db.SilenceAlert(r.Context(), mustParseInt64(alertID), duration); err != nil {
				s.logger.Error("failed to silence alert", "error", err)
				http.Error(w, "Internal error", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if action == "acknowledge" && alertID != "" {
			if err := s.db.AcknowledgeAlert(r.Context(), mustParseInt64(alertID)); err != nil {
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
	projectID := r.URL.Query().Get("project_id")
	hostID := r.URL.Query().Get("host_id")

	var alerts []storage.AlertWithHost
	switch {
	case hostID != "":
		hostAlerts, err := s.db.GetAlerts(r.Context(), mustParseInt64(hostID))
		if err != nil {
			s.logger.Error("failed to get alerts", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		for _, a := range hostAlerts {
			alerts = append(alerts, storage.AlertWithHost{Alert: a})
		}
	case projectID != "":
		id, err := strconv.ParseInt(projectID, 10, 64)
		if err != nil {
			http.Error(w, "Invalid project_id", http.StatusBadRequest)
			return
		}
		alerts, err = s.db.GetAlertsByProject(r.Context(), &id)
		if err != nil {
			s.logger.Error("failed to get alerts", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
	default:
		var err error
		alerts, err = s.db.GetAllAlerts(r.Context())
		if err != nil {
			s.logger.Error("failed to get alerts", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(alerts)
}

func (s *Server) handleAPIAlertRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rules, err := s.db.GetAlertRules(r.Context())
		if err != nil {
			s.logger.Error("failed to get alert rules", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rules)
	case http.MethodPost:
		var rule storage.AlertRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		id, err := s.db.CreateAlertRule(r.Context(), &rule)
		if err != nil {
			s.logger.Error("failed to create alert rule", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		rule.ID = id
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(rule)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPIAlertRuleByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/alert-rules/")
	id, err := parseInt64(idStr)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		rule, err := s.db.GetAlertRule(r.Context(), id)
		if err != nil {
			s.logger.Error("failed to get alert rule", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		if rule == nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rule)
	case http.MethodPut:
		var rule storage.AlertRule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		rule.ID = id
		if err := s.db.UpdateAlertRule(r.Context(), &rule); err != nil {
			s.logger.Error("failed to update alert rule", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rule)
	case http.MethodDelete:
		if err := s.db.DeleteAlertRule(r.Context(), id); err != nil {
			s.logger.Error("failed to delete alert rule", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPINotificationChannels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		channels, err := s.db.GetNotificationChannels(r.Context())
		if err != nil {
			s.logger.Error("failed to get notification channels", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(channels)
	case http.MethodPost:
		var channel storage.NotificationChannel
		if err := json.NewDecoder(r.Body).Decode(&channel); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		id, err := s.db.CreateNotificationChannel(r.Context(), &channel)
		if err != nil {
			s.logger.Error("failed to create notification channel", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		channel.ID = id
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(channel)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPIAPITokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tokens, err := s.db.GetAPITokens(r.Context())
		if err != nil {
			s.logger.Error("failed to get API tokens", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokens)
	case http.MethodPost:
		var token storage.APIToken
		if err := json.NewDecoder(r.Body).Decode(&token); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		plainToken, id, err := s.db.CreateAPIToken(r.Context(), &token)
		if err != nil {
			s.logger.Error("failed to create API token", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		token.ID = id
		response := map[string]interface{}{
			"token": plainToken,
			"id":    id,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(response)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPIAPITokenByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/api-tokens/")
	id := mustParseInt64(idStr)

	switch r.Method {
	case http.MethodGet:
		token, err := s.db.GetAPIToken(r.Context(), id)
		if err != nil {
			s.logger.Error("failed to get API token", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		if token == nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(token)
	case http.MethodPut:
		var token storage.APIToken
		if err := json.NewDecoder(r.Body).Decode(&token); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		token.ID = id
		if err := s.db.UpdateAPIToken(r.Context(), &token); err != nil {
			s.logger.Error("failed to update API token", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(token)
	case http.MethodDelete:
		if err := s.db.DeleteAPIToken(r.Context(), id); err != nil {
			s.logger.Error("failed to delete API token", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAPINotificationChannelByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/notification-channels/")
	id, err := parseInt64(idStr)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		channel, err := s.db.GetNotificationChannel(r.Context(), id)
		if err != nil {
			s.logger.Error("failed to get notification channel", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		if channel == nil {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(channel)
	case http.MethodPut:
		var channel storage.NotificationChannel
		if err := json.NewDecoder(r.Body).Decode(&channel); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		channel.ID = id
		if err := s.db.UpdateNotificationChannel(r.Context(), &channel); err != nil {
			s.logger.Error("failed to update notification channel", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(channel)
	case http.MethodDelete:
		if err := s.db.DeleteNotificationChannel(r.Context(), id); err != nil {
			s.logger.Error("failed to delete notification channel", "error", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func mustParseInt64(s string) int64 {
	n, err := parseInt64(s)
	if err != nil {
		panic("invalid int64: " + s)
	}
	return n
}

func parseInt64(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func (s *Server) render(w http.ResponseWriter, name string, data interface{}) {
	tmpl, ok := s.templates[name]
	if !ok {
		s.logger.Error("template not found", "template", name)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	entry := "base"
	if name == "login.html" {
		entry = "login.html"
	}
	if err := tmpl.ExecuteTemplate(w, entry, data); err != nil {
		s.logger.Error("template render failed", "template", name, "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
	}
}

func generateSessionID() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}
