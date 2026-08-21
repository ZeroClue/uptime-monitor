package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/config"
	"github.com/ZeroClue/uptime-monitor/internal/scheduler"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

func TestToRateSeries(t *testing.T) {
	data := [][2]float64{
		{1000, 10},
		{1060, 20},
		{1120, 30},
	}
	got := toRateSeries(data)
	if len(got) != 2 {
		t.Fatalf("got %d points, want 2", len(got))
	}
	if got[0][0] != 1060 || got[0][1] != 10.0/60.0 {
		t.Errorf("got %v, want rate 10/60 at t=1060", got[0])
	}
	if got[1][1] != 10.0/60.0 {
		t.Errorf("got %v, want 10/60 at t=1120", got[1])
	}
}

func TestToRateSeries_Empty(t *testing.T) {
	if got := toRateSeries(nil); len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}

func newTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func TestAllTemplatesParse(t *testing.T) {
	s := &Server{logger: newTestLogger(t)}
	s.loadTemplates()

	want := []string{"index.html", "host.html", "compare.html", "projects.html", "alerts.html", "monitor.html", "login.html"}
	for _, name := range want {
		if _, ok := s.templates[name]; !ok {
			t.Errorf("template %s not loaded", name)
			continue
		}
		entry := "base"
		if name == "login.html" {
			entry = "login.html"
		}
		if tpl := s.templates[name].Lookup(entry); tpl == nil {
			t.Errorf("template %s has no %q entry", name, entry)
		}
	}
}

func TestBaseTemplateExecutesContent(t *testing.T) {
	s := &Server{logger: newTestLogger(t)}
	s.loadTemplates()
	// compare.html is rendered with nil data by its handler, so it exercises
	// the base shell + content block end to end without needing storage data.
	tmpl, ok := s.templates["compare.html"]
	if !ok {
		t.Fatal("compare.html not loaded")
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", nil); err != nil {
		t.Fatalf("base template failed to execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"<nav", "theme-toggle", "Skip to content", "Multi-Host Comparison"} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("rendered base+compare missing %q", want)
		}
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	tmpDir := t.TempDir()
	db, err := storage.New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	hosts := []config.Host{
		{Name: "vm-a", Connection: "ssh", Endpoint: "10.0.0.1", User: "test", KeyPath: "/keys/a", Port: 22, Sudo: false, Timeout: 10 * time.Second, Tags: []string{"prod"}},
		{Name: "vm-b", Connection: "ssh", Endpoint: "10.0.0.2", User: "test", KeyPath: "/keys/b", Port: 22, Sudo: false, Timeout: 10 * time.Second},
	}
	if err := db.SeedHosts(hosts); err != nil {
		t.Fatalf("seed hosts failed: %v", err)
	}
	retrieved, _ := db.GetHosts()
	if len(retrieved) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(retrieved))
	}

	now := time.Now().Add(-time.Minute)
	var samples []collector.Sample
	for _, h := range retrieved {
		samples = append(samples,
			collector.Sample{HostID: h.ID, Metric: "cpu.user_pct", Value: 40.0, Timestamp: now, Collector: "procfs"},
			collector.Sample{HostID: h.ID, Metric: "cpu.system_pct", Value: 10.0, Timestamp: now, Collector: "procfs"},
			collector.Sample{HostID: h.ID, Metric: "mem.used_bytes", Value: 50.0, Timestamp: now, Collector: "procfs"},
			collector.Sample{HostID: h.ID, Metric: "mem.total_bytes", Value: 100.0, Timestamp: now, Collector: "procfs"},
			collector.Sample{HostID: h.ID, Metric: "uptime.seconds", Value: 3600.0, Timestamp: now, Collector: "procfs"},
		)
	}
	if err := db.SaveSamples(samples); err != nil {
		t.Fatalf("save samples failed: %v", err)
	}

	sched := scheduler.New(30*time.Second, db, collector.NewChain(), newTestLogger(t))
	return NewServer("pw", db, sched, newTestLogger(t), false)
}

func TestAPIHostsStatus(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/hosts/status", nil)
	rec := httptest.NewRecorder()
	s.handleAPIHostsStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var out []HostStatusSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 host summaries, got %d", len(out))
	}
	for _, h := range out {
		if h.Name != "vm-a" && h.Name != "vm-b" {
			t.Errorf("unexpected host name %q", h.Name)
		}
		if h.CPU == nil || *h.CPU != 50.0 {
			t.Errorf("host %s: expected CPU 50.0 (user+system), got %v", h.Name, h.CPU)
		}
		if h.Mem == nil || *h.Mem != 50.0 {
			t.Errorf("host %s: expected MEM 50.0, got %v", h.Name, h.Mem)
		}
		if h.Uptime == nil || *h.Uptime != 3600.0 {
			t.Errorf("host %s: expected uptime 3600, got %v", h.Name, h.Uptime)
		}
		if h.Status != "unknown" {
			t.Errorf("host %s: expected status unknown (scheduler not run), got %q", h.Name, h.Status)
		}
	}
}

func TestAPIMonitor(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/monitor", nil)
	rec := httptest.NewRecorder()
	s.handleAPIMonitor(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var out struct {
		DBSizeMB float64 `json:"db_size_mb"`
		Hosts    []struct {
			HostID int64  `json:"host_id"`
			Name   string `json:"name"`
		} `json:"hosts"`
		Interval string `json:"interval"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if out.Interval != "30s" {
		t.Errorf("expected interval 30s, got %q", out.Interval)
	}
	if len(out.Hosts) != 2 {
		t.Errorf("expected 2 hosts, got %d", len(out.Hosts))
	}
	if out.DBSizeMB <= 0 {
		t.Errorf("expected positive db size, got %v", out.DBSizeMB)
	}
}

func TestAPIAlertsGetAll(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
	rec := httptest.NewRecorder()
	s.handleAPIAlerts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var out []storage.AlertWithHost
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected no alerts, got %d", len(out))
	}
}

func TestAPIAlertsRequiresAction(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/alerts?action=acknowledge&id=999", nil)
	rec := httptest.NewRecorder()
	s.handleAPIAlerts(rec, req)
	// ack of a nonexistent alert should not panic; treat as error status or OK
	if rec.Code != http.StatusOK && rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 200 or 500, got %d", rec.Code)
	}
}

func TestAPIAlerts_ProjectScoped(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	projectA, err := s.db.CreateProject(ctx, &storage.Project{Name: "proj-a", Type: "explicit"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	hosts, _ := s.db.GetHosts()
	if len(hosts) < 2 {
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE hosts SET project_id = ? WHERE id = ?`, projectA, hosts[0].ID); err != nil {
		t.Fatalf("assign host to project: %v", err)
	}
	if err := s.db.InsertAlert(ctx, storage.Alert{HostID: hosts[0].ID, Type: "threshold", Metric: "cpu.user_pct", Severity: "warning", Message: "m", Value: 90, Threshold: 80, FiredAt: time.Now()}); err != nil {
		t.Fatalf("insert alert A: %v", err)
	}
	if err := s.db.InsertAlert(ctx, storage.Alert{HostID: hosts[1].ID, Type: "threshold", Metric: "mem.used_pct", Severity: "critical", Message: "m", Value: 95, Threshold: 90, FiredAt: time.Now()}); err != nil {
		t.Fatalf("insert alert B: %v", err)
	}

	handler := s.projectMiddleware(s.handleAPIAlerts)

	req := httptest.NewRequest(http.MethodGet, "/api/alerts?project_id="+strconv.FormatInt(projectA, 10), nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var scoped []storage.AlertWithHost
	if err := json.Unmarshal(rec.Body.Bytes(), &scoped); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(scoped) != 1 || scoped[0].Metric != "cpu.user_pct" {
		t.Fatalf("expected only project A's cpu alert, got %+v", scoped)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var all []storage.AlertWithHost
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected all alerts without filter, got %d", len(all))
	}

	req = httptest.NewRequest(http.MethodGet, "/api/alerts?project_id=notanumber", nil)
	rec = httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid project_id, got %d", rec.Code)
	}
}

func TestProjectsConfigPage_Renders(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/projects/config", nil)
	rec := httptest.NewRecorder()
	s.handleProjectsConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("Projects Config")) {
		t.Error("page content missing")
	}
}

func TestIndex_IncludesProjectColumnWhenMultiple(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	// Single project (Default from bootstrap): column hidden.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.handleIndex(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, `data-sort="project"`) {
		t.Error("project column should be hidden with <=1 project")
	}

	if _, err := s.db.CreateProject(ctx, &storage.Project{Name: "second", Type: "explicit"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.CreateProject(ctx, &storage.Project{Name: "third", Type: "explicit"}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	s.handleIndex(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `data-sort="project"`) {
		t.Error("project column should appear with >1 project")
	}
}
