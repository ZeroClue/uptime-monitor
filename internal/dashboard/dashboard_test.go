package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestDeleteDefaultProject_Blocked(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	id, err := s.db.CreateProject(ctx, &storage.Project{Name: "defy", Type: "explicit", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/projects/"+strconv.FormatInt(id, 10), nil)
	rec := httptest.NewRecorder()
	s.handleAPIProjectByID(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 deleting default project, got %d", rec.Code)
	}
}

func TestAPIHosts_UnscopedReturnsAll(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.handleAPIHosts(rec, httptest.NewRequest(http.MethodGet, "/api/hosts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var hosts []storage.Host
	if err := json.Unmarshal(rec.Body.Bytes(), &hosts); err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Fatalf("typed-nil scope filter leaked: expected all 2 hosts, got %d", len(hosts))
	}
	for _, h := range hosts {
		if h.Endpoint == "" || h.Connection == "" || h.Port == 0 {
			t.Errorf("host %q: /api/hosts must return full records for the config table/edit modal: %+v", h.Name, h)
		}
	}
}

type fakeScriptRunner struct {
	got         collector.Host
	hadDeadline bool
	samples     []collector.Sample
	err         error
}

func (f *fakeScriptRunner) Name() string { return "custom" }

func (f *fakeScriptRunner) Collect(ctx context.Context, h collector.Host) ([]collector.Sample, error) {
	f.got = h
	_, f.hadDeadline = ctx.Deadline()
	return f.samples, f.err
}

func scriptTestServer(t *testing.T) (*Server, *storage.DB, int64, *fakeScriptRunner) {
	t.Helper()
	s := newTestServer(t)
	db := s.db
	runner := &fakeScriptRunner{}
	s.scriptRunner = runner
	id, err := db.CreateHost(context.Background(), &storage.Host{
		Name: "script-host", Connection: "ssh", Endpoint: "10.9.9.9", Port: 2200,
		User: "svc", TimeoutRaw: (15 * time.Second).Nanoseconds(),
		ScriptName: "stored-script", ScriptCommand: `stats --host {{.Host}}`, ScriptParse: "json",
	})
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	return s, db, id, runner
}

func TestAPIHostScriptTest_AppliesOverridesAndReturnsSamples(t *testing.T) {
	s, _, id, runner := scriptTestServer(t)
	runner.samples = []collector.Sample{
		{Metric: "custom.t.depth", Value: 3, Timestamp: time.Now(), Collector: "custom"},
	}

	body := strings.NewReader(`{"script_name":"draft","script_command":"run --port {{.Port}}","script_parse":"csv"}`)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/hosts/%d/scripts/test", id), body)
	rec := httptest.NewRecorder()
	s.handleAPIHostScriptTest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if runner.got.ScriptName != "draft" || runner.got.ScriptCommand != "run --port {{.Port}}" || runner.got.ScriptParse != "csv" {
		t.Errorf("overrides not applied: %+v", runner.got)
	}
	if runner.got.Endpoint != "10.9.9.9" || runner.got.Port != 2200 || runner.got.User != "svc" || runner.got.Timeout != 15*time.Second {
		t.Errorf("stored connection details not mapped: %+v", runner.got)
	}

	var out struct {
		Count   int                `json:"count"`
		Samples []collector.Sample `json:"samples"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if out.Count != 1 || len(out.Samples) != 1 || out.Samples[0].Metric != "custom.t.depth" || out.Samples[0].Value != 3 {
		t.Errorf("unexpected response: %+v", out)
	}
	if out.Samples[0].Timestamp.IsZero() {
		t.Error("timestamp lost in encoding")
	}
}

func TestAPIHostScriptTest_UsesStoredScriptWhenBodyEmpty(t *testing.T) {
	s, _, id, runner := scriptTestServer(t)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/hosts/%d/scripts/test", id), nil)
	rec := httptest.NewRecorder()
	s.handleAPIHostScriptTest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if runner.got.ScriptName != "stored-script" || runner.got.ScriptCommand != `stats --host {{.Host}}` || runner.got.ScriptParse != "json" {
		t.Errorf("stored script not used: %+v", runner.got)
	}
}

func TestAPIHostScriptTest_CollectorTimeoutBoundsRequest(t *testing.T) {
	s, db, id, runner := scriptTestServer(t)
	ctx := context.Background()

	collected, err := db.GetHost(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	timeoutMs := int64(5000)
	collected.CollectorTimeoutMs = &timeoutMs
	if err := db.UpdateHost(ctx, collected); err != nil {
		t.Fatal(err)
	}
	runner.hadDeadline = false

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/hosts/%d/scripts/test", id), nil)
	rec := httptest.NewRecorder()
	s.handleAPIHostScriptTest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !runner.hadDeadline {
		t.Error("collector timeout did not bound the test run context")
	}
}

func TestAPIHostScriptTest_UnknownHost(t *testing.T) {
	s, _, _, _ := scriptTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/hosts/999/scripts/test", nil)
	rec := httptest.NewRecorder()
	s.handleAPIHostScriptTest(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAPIHostScriptTest_BadJSON(t *testing.T) {
	s, _, id, _ := scriptTestServer(t)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/hosts/%d/scripts/test", id), strings.NewReader("{nope"))
	rec := httptest.NewRecorder()
	s.handleAPIHostScriptTest(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestAPIHostScriptTest_RunErrorSurfaces(t *testing.T) {
	s, _, id, runner := scriptTestServer(t)
	runner.err = errors.New("exit status 1: stats not found")

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/hosts/%d/scripts/test", id), nil)
	rec := httptest.NewRecorder()
	s.handleAPIHostScriptTest(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "stats not found") {
		t.Errorf("error detail missing from response: %q", rec.Body.String())
	}
}

func TestAPIHosts_RoutesScriptTestPath(t *testing.T) {
	s, _, id, _ := scriptTestServer(t)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/hosts/%d/scripts/test", id), nil)
	rec := httptest.NewRecorder()
	s.handleAPIHosts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected script-test routing via handleAPIHosts, got %d: %s", rec.Code, rec.Body.String())
	}
}
