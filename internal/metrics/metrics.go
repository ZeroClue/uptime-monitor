package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/scheduler"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

type Server struct {
	db    *storage.DB
	sched *scheduler.Scheduler
	logger *slog.Logger
	server *http.Server
}

func NewServer(db *storage.DB, sched *scheduler.Scheduler, logger *slog.Logger) *Server {
	return &Server{
		db:    db,
		sched: sched,
		logger: logger,
	}
}

func (s *Server) Run(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/api/hosts", s.handleAPIHosts)
	mux.HandleFunc("/api/host/", s.handleAPIHost)
	mux.HandleFunc("/api/compare", s.handleAPICompare)

	s.server = &http.Server{
		Addr:    ":8081",
		Handler: mux,
	}

	go func() {
		s.logger.Info("metrics server starting", "addr", ":8081")
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("metrics server error", "error", err)
		}
	}()

	<-ctx.Done()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.server.Shutdown(ctx)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	
	statuses := s.sched.GetAllHostStatuses()
	hosts, _ := s.db.GetHosts()
	
	for _, h := range hosts {
		st := statuses[h.ID]
		collector := "unknown"
		lastSuccess := int64(0)
		fails := 0
		if st != nil {
			collector = st.LastCollector
			if !st.LastSuccess.IsZero() {
				lastSuccess = st.LastSuccess.Unix()
			}
			fails = st.ConsecutiveFails
		}
		
		w.Write([]byte(fmt.Sprintf("poll_total{host=\"%s\",collector=\"%s\",result=\"success\"} 1\npoll_latency_seconds{host=\"%s\",collector=\"%s\"} 0\ncollector_last_success{host=\"%s\"} %d\nhost_status{host=\"%s\"} %d\n", h.Name, collector, h.Name, collector, h.Name, lastSuccess, h.Name, fails)))
	}
	
	dbSize := int64(0)
	if fi, err := os.Stat("/data/monitor.db"); err == nil {
		dbSize = fi.Size()
	}
	w.Write([]byte(fmt.Sprintf("db_size_bytes %d\n", dbSize)))
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
	
	hosts, err := s.db.GetHosts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
		Host string      `json:"host"`
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