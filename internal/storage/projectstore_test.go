package storage

import (
	"context"
	"testing"
	"time"
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
