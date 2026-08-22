package storage

import (
	"context"
	"testing"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/config"
)

func newTestProjectDB(t *testing.T) *DB {
	t.Helper()
	db, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	return db
}

func seedProjectAlertFixture(t *testing.T, db *DB) (projectA, projectB int64) {
	t.Helper()
	ctx := context.Background()

	var err error
	projectA, err = db.CreateProject(ctx, &Project{Name: "proj-a", Type: "explicit"})
	if err != nil {
		t.Fatalf("create project A: %v", err)
	}
	projectB, err = db.CreateProject(ctx, &Project{Name: "proj-b", Type: "explicit"})
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}

	hostIDs := make([]int64, 0, 2)
	for i, name := range []string{"host-a", "host-b"} {
		id, err := db.CreateHost(ctx, &Host{Name: name, Connection: "ssh", Endpoint: "10.0.0." + string(rune('1'+i)), Port: 22, Timeout: 10 * time.Second})
		if err != nil {
			t.Fatalf("create host %s: %v", name, err)
		}
		hostIDs = append(hostIDs, id)
	}
	if _, err := db.ExecContext(ctx, `UPDATE hosts SET project_id = ? WHERE id = ?`, projectA, hostIDs[0]); err != nil {
		t.Fatalf("assign host A: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE hosts SET project_id = ? WHERE id = ?`, projectB, hostIDs[1]); err != nil {
		t.Fatalf("assign host B: %v", err)
	}

	alerts := []Alert{
		{HostID: hostIDs[0], Type: "threshold", Metric: "cpu.user_pct", Severity: "warning", Message: "cpu high", Value: 90, Threshold: 80, FiredAt: time.Now()},
		{HostID: hostIDs[1], Type: "threshold", Metric: "mem.used_pct", Severity: "critical", Message: "mem high", Value: 95, Threshold: 90, FiredAt: time.Now()},
	}
	for _, a := range alerts {
		if err := db.InsertAlert(ctx, a); err != nil {
			t.Fatalf("insert alert: %v", err)
		}
	}
	return projectA, projectB
}

func TestGetAlertsByProject(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()
	projectA, _ := seedProjectAlertFixture(t, db)

	scoped, err := db.GetAlertsByProject(ctx, &projectA)
	if err != nil {
		t.Fatalf("GetAlertsByProject: %v", err)
	}
	if len(scoped) != 1 {
		t.Fatalf("expected 1 alert for project A, got %d", len(scoped))
	}
	if scoped[0].Metric != "cpu.user_pct" {
		t.Errorf("expected cpu.user_pct, got %q", scoped[0].Metric)
	}
	if scoped[0].HostName != "host-a" {
		t.Errorf("expected host name host-a, got %q", scoped[0].HostName)
	}

	all, err := db.GetAlertsByProject(ctx, nil)
	if err != nil {
		t.Fatalf("GetAlertsByProject(nil): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 alerts with nil filter, got %d", len(all))
	}
}

func TestEnsureDefaultProject_CreatesAndAssigns(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()

	hostID, err := db.CreateHost(ctx, &Host{Name: "lonely", Connection: "ssh", Endpoint: "10.0.0.9", Port: 22, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("create host: %v", err)
	}

	if err := db.EnsureDefaultProject(ctx); err != nil {
		t.Fatalf("EnsureDefaultProject: %v", err)
	}

	projects, err := db.GetProjects(ctx)
	if err != nil {
		t.Fatalf("GetProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected exactly 1 project, got %d", len(projects))
	}
	if !projects[0].IsDefault || projects[0].Name != "Default" {
		t.Errorf("expected default project named 'Default', got %+v", projects[0])
	}

	var assigned *int64
	if err := db.QueryRowContext(ctx, `SELECT project_id FROM hosts WHERE id = ?`, hostID).Scan(&assigned); err != nil {
		t.Fatalf("scan host project_id: %v", err)
	}
	if assigned == nil || *assigned != projects[0].ID {
		t.Errorf("expected host assigned to default project %d, got %v", projects[0].ID, assigned)
	}
}

func TestEnsureDefaultProject_Idempotent(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()

	if err := db.EnsureDefaultProject(ctx); err != nil {
		t.Fatalf("first EnsureDefaultProject: %v", err)
	}
	if err := db.EnsureDefaultProject(ctx); err != nil {
		t.Fatalf("second EnsureDefaultProject: %v", err)
	}

	projects, err := db.GetProjects(ctx)
	if err != nil {
		t.Fatalf("GetProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected still 1 project after repeat call, got %d", len(projects))
	}
}

func TestHostTimeoutRoundtrip(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()

	sshMs, collMs := int64(5000), int64(45000)
	id, err := db.CreateHost(ctx, &Host{
		Name: "timed", Connection: "ssh", Endpoint: "10.0.0.1", Port: 22,
		Timeout: 20 * time.Second, SshTimeoutMs: &sshMs, CollectorTimeoutMs: &collMs,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.GetHost(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SshTimeoutMs == nil || *got.SshTimeoutMs != 5000 {
		t.Errorf("ssh_timeout_ms lost: %v", got.SshTimeoutMs)
	}
	if got.CollectorTimeoutMs == nil || *got.CollectorTimeoutMs != 45000 {
		t.Errorf("collector_timeout_ms lost: %v", got.CollectorTimeoutMs)
	}

	got.SshTimeoutMs = nil // clearing an override must persist
	if err := db.UpdateHost(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	cleared, _ := db.GetHost(ctx, id)
	if cleared.SshTimeoutMs != nil {
		t.Errorf("expected ssh override cleared, got %v", cleared.SshTimeoutMs)
	}
	if cleared.CollectorTimeoutMs == nil || *cleared.CollectorTimeoutMs != 45000 {
		t.Errorf("collector override should survive update: %v", cleared.CollectorTimeoutMs)
	}
}

func TestRulesAndChannels_ProjectScoping(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()

	projA, err := db.CreateProject(ctx, &Project{Name: "rules-a", Type: "explicit"})
	if err != nil {
		t.Fatal(err)
	}
	pA := projA

	globalRuleID, err := db.CreateAlertRule(ctx, &AlertRule{Metric: "cpu.user_pct", Scope: "global", Warning: 80, Critical: 90})
	if err != nil {
		t.Fatal(err)
	}
	projRuleID, err := db.CreateAlertRule(ctx, &AlertRule{Metric: "mem.used_pct", Scope: "global", Warning: 70, Critical: 85, ProjectID: &pA})
	if err != nil {
		t.Fatal(err)
	}
	_ = globalRuleID
	_ = projRuleID

	if _, err := db.CreateNotificationChannel(ctx, &NotificationChannel{Name: "chan-global", Type: "webhook", Config: "{}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateNotificationChannel(ctx, &NotificationChannel{Name: "chan-a", Type: "webhook", Config: "{}", ProjectID: &pA}); err != nil {
		t.Fatal(err)
	}

	t.Run("nil filter returns everything incl. globals", func(t *testing.T) {
		rules, err := db.GetAlertRulesByProject(ctx, nil)
		if err != nil || len(rules) != 2 {
			t.Fatalf("want 2 rules, got %d, %v", len(rules), err)
		}
		chans, err := db.GetNotificationChannelsByProject(ctx, nil)
		if err != nil || len(chans) != 2 {
			t.Fatalf("want 2 channels, got %d, %v", len(chans), err)
		}
	})

	t.Run("project filter returns globals + own", func(t *testing.T) {
		rules, err := db.GetAlertRulesByProject(ctx, &pA)
		if err != nil || len(rules) != 2 {
			t.Fatalf("want 2 (global+own), got %d, %v", len(rules), err)
		}
		chans, err := db.GetNotificationChannelsByProject(ctx, &pA)
		if err != nil || len(chans) != 2 {
			t.Fatalf("want 2 (global+own), got %d, %v", len(chans), err)
		}
	})

	t.Run("roundtrip preserves project_id", func(t *testing.T) {
		got, err := db.GetAlertRule(ctx, projRuleID)
		if err != nil || got == nil || got.ProjectID == nil || *got.ProjectID != pA {
			t.Fatalf("rule project lost: %+v %v", got, err)
		}
	})
}

func TestHostKeyPolicyRoundtrip(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()

	auto := "auto"
	id, err := db.CreateHost(ctx, &Host{Name: "kp", Connection: "ssh", Endpoint: "10.0.0.1", Port: 22, SSHHostKeyPolicy: &auto})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetHost(ctx, id)
	if err != nil || got.SSHHostKeyPolicy == nil || *got.SSHHostKeyPolicy != "auto" {
		t.Fatalf("policy lost on create: %+v %v", got, err)
	}

	strict := "strict"
	got.SSHHostKeyPolicy = &strict
	if err := db.UpdateHost(ctx, got); err != nil {
		t.Fatal(err)
	}
	got2, _ := db.GetHost(ctx, id)
	if got2.SSHHostKeyPolicy == nil || *got2.SSHHostKeyPolicy != "strict" {
		t.Fatalf("policy lost on update: %+v", got2)
	}
}

func TestSeedHosts_UpsertSemantics(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()

	// Initial seed defines the host.
	if err := db.SeedHosts([]config.Host{
		{Name: "mixed", Connection: "ssh", Endpoint: "10.0.0.1", Port: 22, Timeout: 10 * time.Second, Tags: []string{"old"}},
	}); err != nil {
		t.Fatal(err)
	}
	host, _ := db.GetHostByName(ctx, "mixed")

	// Operator tunes operational fields via the API path.
	retry := int64(5)
	base := int64(500)
	pol := "auto"
	host.TimeoutRaw = (30 * time.Second).Nanoseconds()
	host.RetryMaxRetries = &retry
	host.RetryBaseMs = &base
	host.SSHHostKeyPolicy = &pol
	if err := db.UpdateHost(ctx, host); err != nil {
		t.Fatal(err)
	}

	// yaml changes connectivity AND would have clobbered operations.
	if err := db.SeedHosts([]config.Host{
		{Name: "mixed", Connection: "ssh", Endpoint: "10.0.0.99", Port: 2222, Timeout: 10 * time.Second, Tags: []string{"new"}},
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetHostByName(ctx, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	if got.Endpoint != "10.0.0.99" || got.Port != 2222 {
		t.Errorf("identity fields should resync from yaml: %+v:%d", got.Endpoint, got.Port)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "new" {
		t.Errorf("tags are identity: %v", got.Tags)
	}
	if got.TimeoutRaw != (30 * time.Second).Nanoseconds() {
		t.Errorf("timeout override lost to yaml sync: %v", got.TimeoutRaw)
	}
	if got.RetryMaxRetries == nil || *got.RetryMaxRetries != 5 {
		t.Errorf("retry override lost: %v", got.RetryMaxRetries)
	}
	if got.SSHHostKeyPolicy == nil || *got.SSHHostKeyPolicy != "auto" {
		t.Errorf("policy override lost: %v", got.SSHHostKeyPolicy)
	}

	// Fresh hosts still get full seeding including operational values.
	freshTimeout := 42 * time.Second
	if err := db.SeedHosts([]config.Host{
		{Name: "fresh", Connection: "ssh", Endpoint: "10.0.1.1", Port: 22, Timeout: freshTimeout},
	}); err != nil {
		t.Fatal(err)
	}
	fresh, _ := db.GetHostByName(ctx, "fresh")
	if fresh.Timeout != freshTimeout {
		t.Errorf("fresh host timeout not seeded: %v", fresh.Timeout)
	}
}
