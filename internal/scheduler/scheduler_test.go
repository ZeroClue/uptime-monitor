package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/config"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

func TestScheduler_PollHostSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.New(tmpDir)
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
	host := retrieved[0]

	mockColl := &mockCollector{
		name: "mock",
		samples: []collector.Sample{
			{HostID: host.ID, Metric: "cpu.user_pct", Value: 50, Timestamp: time.Now(), Collector: "mock"},
		},
	}
	chain := collector.NewChain(mockColl)

	sched := New(30*time.Second, db, chain, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sched.pollHost(ctx, host)

	status := sched.GetHostStatus(host.ID)
	if status == nil {
		t.Fatal("expected host status to be set")
	}
	if status.ConsecutiveFails != 0 {
		t.Fatalf("expected 0 consecutive fails, got %d", status.ConsecutiveFails)
	}
	if status.LastCollector != "mock" {
		t.Fatalf("expected collector 'mock', got '%s'", status.LastCollector)
	}
	if status.LastSuccess.IsZero() {
		t.Fatal("expected LastSuccess to be set")
	}
}

func TestScheduler_PollHostFailure(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.New(tmpDir)
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
	host := retrieved[0]

	mockColl := &failingCollector{name: "failing"}
	chain := collector.NewChain(mockColl)

	sched := New(30*time.Second, db, chain, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sched.pollHost(ctx, host)

	status := sched.GetHostStatus(host.ID)
	if status == nil {
		t.Fatal("expected host status to be set")
	}
	if status.ConsecutiveFails != 1 {
		t.Fatalf("expected 1 consecutive fail, got %d", status.ConsecutiveFails)
	}
	if status.LastError == "" {
		t.Fatal("expected LastError to be set")
	}
}

type mockCollector struct {
	name    string
	samples []collector.Sample
}

func (m *mockCollector) Name() string { return m.name }
func (m *mockCollector) Collect(ctx context.Context, host collector.Host) ([]collector.Sample, error) {
	return m.samples, nil
}

type failingCollector struct {
	name string
}

func (f *failingCollector) Name() string { return f.name }
func (f *failingCollector) Collect(ctx context.Context, host collector.Host) ([]collector.Sample, error) {
	return nil, collector.ErrCollectorFailed
}