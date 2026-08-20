package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/config"
)

func TestDB_MigrateAndSeed(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	hosts := []config.Host{
		{Name: "test-host", Connection: "ssh", Endpoint: "10.0.0.1", User: "test", KeyPath: "/keys/test", Port: 22, Sudo: false, Timeout: 10 * time.Second},
	}
	if err := db.SeedHosts(hosts); err != nil {
		t.Fatalf("seed hosts failed: %v", err)
	}

	retrieved, err := db.GetHosts()
	if err != nil {
		t.Fatalf("get hosts failed: %v", err)
	}
	if len(retrieved) != 1 {
		t.Fatalf("expected 1 host, got %d", len(retrieved))
	}
	if retrieved[0].Name != "test-host" {
		t.Fatalf("expected host name 'test-host', got '%s'", retrieved[0].Name)
	}
}

func TestDB_SaveAndGetSamples(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	hosts := []config.Host{
		{Name: "test-host", Connection: "ssh", Endpoint: "10.0.0.1", User: "test", KeyPath: "/keys/test", Port: 22, Sudo: false, Timeout: 10 * time.Second},
	}
	if err := db.SeedHosts(hosts); err != nil {
		t.Fatalf("seed hosts failed: %v", err)
	}

	retrieved, _ := db.GetHosts()
	hostID := retrieved[0].ID

	samples := []collector.Sample{
		{HostID: hostID, Metric: "cpu.user_pct", Value: 50.0, Timestamp: time.Now(), Collector: "procfs"},
		{HostID: hostID, Metric: "mem.used_bytes", Value: 1024.0, Timestamp: time.Now(), Collector: "procfs"},
	}
	if err := db.SaveSamples(samples); err != nil {
		t.Fatalf("save samples failed: %v", err)
	}

	from := time.Now().Add(-time.Hour)
	to := time.Now().Add(time.Hour)
	got, err := db.GetSamples(context.Background(), hostID, "cpu.user_pct", from, to, "raw")
	if err != nil {
		t.Fatalf("get samples failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(got))
	}
	if got[0].Metric != "cpu.user_pct" || got[0].Value != 50.0 {
		t.Fatalf("unexpected sample: %+v", got[0])
	}
}

func TestDB_GetLatestSample(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	hosts := []config.Host{
		{Name: "test-host", Connection: "ssh", Endpoint: "10.0.0.1", User: "test", KeyPath: "/keys/test", Port: 22, Sudo: false, Timeout: 10 * time.Second},
	}
	if err := db.SeedHosts(hosts); err != nil {
		t.Fatalf("seed hosts failed: %v", err)
	}

	retrieved, _ := db.GetHosts()
	hostID := retrieved[0].ID

	base := time.Now().Add(-time.Minute)
	samples := []collector.Sample{
		{HostID: hostID, Metric: "cpu.user_pct", Value: 10.0, Timestamp: base, Collector: "procfs"},
		{HostID: hostID, Metric: "cpu.user_pct", Value: 20.0, Timestamp: base.Add(30 * time.Second), Collector: "procfs"},
		{HostID: hostID, Metric: "cpu.user_pct", Value: 30.0, Timestamp: base.Add(60 * time.Second), Collector: "procfs"},
	}
	if err := db.SaveSamples(samples); err != nil {
		t.Fatalf("save samples failed: %v", err)
	}

	latest, err := db.GetLatestSample(context.Background(), hostID, "cpu.user_pct")
	if err != nil {
		t.Fatalf("get latest sample failed: %v", err)
	}
	if latest == nil {
		t.Fatal("expected a latest sample, got nil")
	}
	if latest.Value != 30.0 {
		t.Errorf("expected latest value 30.0, got %v", latest.Value)
	}
	if latest.Metric != "cpu.user_pct" {
		t.Errorf("expected metric cpu.user_pct, got %q", latest.Metric)
	}

	// unknown metric -> nil, no error
	none, err := db.GetLatestSample(context.Background(), hostID, "does.not.exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if none != nil {
		t.Errorf("expected nil for unknown metric, got %+v", none)
	}
}

func TestDB_Downsample(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	hosts := []config.Host{
		{Name: "test-host", Connection: "ssh", Endpoint: "10.0.0.1", User: "test", KeyPath: "/keys/test", Port: 22, Sudo: false, Timeout: 10 * time.Second},
	}
	if err := db.SeedHosts(hosts); err != nil {
		t.Fatalf("seed hosts failed: %v", err)
	}

	retrieved, _ := db.GetHosts()
	hostID := retrieved[0].ID

	bucket := time.Now().Truncate(time.Minute).Add(-time.Minute)
	for i := 0; i < 5; i++ {
		samples := []collector.Sample{
			{HostID: hostID, Metric: "cpu.user_pct", Value: float64(50 + i), Timestamp: bucket.Add(time.Duration(i) * 10 * time.Second), Collector: "procfs"},
		}
		if err := db.SaveSamples(samples); err != nil {
			t.Fatalf("save samples failed: %v", err)
		}
	}

	if err := db.Downsample(context.Background()); err != nil {
		t.Fatalf("downsample failed: %v", err)
	}

	from := bucket.Add(-time.Hour)
	to := bucket.Add(time.Hour)
	got, err := db.GetSamples(context.Background(), hostID, "cpu.user_pct", from, to, "1m")
	if err != nil {
		t.Fatalf("get 1m samples failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 aggregated sample, got %d", len(got))
	}
	expectedAvg := 52.0
	if got[0].Value != expectedAvg {
		t.Fatalf("expected avg %f, got %f", expectedAvg, got[0].Value)
	}
}

func TestDB_Cleanup(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	hosts := []config.Host{
		{Name: "test-host", Connection: "ssh", Endpoint: "10.0.0.1", User: "test", KeyPath: "/keys/test", Port: 22, Sudo: false, Timeout: 10 * time.Second},
	}
	if err := db.SeedHosts(hosts); err != nil {
		t.Fatalf("seed hosts failed: %v", err)
	}

	retrieved, _ := db.GetHosts()
	hostID := retrieved[0].ID

	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	samples := []collector.Sample{
		{HostID: hostID, Metric: "cpu.user_pct", Value: 50.0, Timestamp: oldTime, Collector: "procfs"},
	}
	if err := db.SaveSamples(samples); err != nil {
		t.Fatalf("save samples failed: %v", err)
	}

	if err := db.Cleanup(context.Background()); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	from := time.Now().Add(-time.Hour)
	to := time.Now().Add(time.Hour)
	got, err := db.GetSamples(context.Background(), hostID, "cpu.user_pct", from, to, "raw")
	if err != nil {
		t.Fatalf("get samples failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 samples after cleanup, got %d", len(got))
	}
}

func TestDB_FileCreation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "monitor.db")

	db, err := New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}
	db.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("database file was not created at %s, tmpDir=%s", dbPath, tmpDir)
	}
}
