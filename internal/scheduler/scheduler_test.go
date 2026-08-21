package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/config"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

type flakyCollector struct {
	failFirst int
	samples   []collector.Sample
	calls     int
}

func (f *flakyCollector) Name() string { return "flaky" }
func (f *flakyCollector) Collect(ctx context.Context, host collector.Host) ([]collector.Sample, error) {
	f.calls++
	if f.calls <= f.failFirst {
		return nil, errors.New("connection refused")
	}
	return f.samples, nil
}

type errorCollector struct {
	err error
}

func (e *errorCollector) Name() string { return "err" }
func (e *errorCollector) Collect(ctx context.Context, host collector.Host) ([]collector.Sample, error) {
	return nil, e.err
}

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

	sched := NewWithRetry(30*time.Second, db, chain, nil, config.RetryConfig{MaxRetries: 1})
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
	if status.LastPollAttempts != 1 {
		t.Fatalf("expected 1 attempt with retries disabled, got %d", status.LastPollAttempts)
	}
}

func TestScheduler_RetriesThenSucceeds(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if err := db.SeedHosts([]config.Host{{Name: "flaky", Connection: "ssh", Endpoint: "10.0.0.1", Port: 22, Timeout: time.Second}}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	retrieved, _ := db.GetHosts()
	host := retrieved[0]

	flaky := &flakyCollector{failFirst: 2, samples: []collector.Sample{
		{Metric: "cpu.user_pct", Value: 42, Timestamp: time.Now(), Collector: "mock"},
	}}
	sched := NewWithRetry(30*time.Second, db, collector.NewChain(flaky), nil, config.RetryConfig{
		MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond,
	})

	sched.pollHost(context.Background(), host)

	status := sched.GetHostStatus(host.ID)
	if status == nil {
		t.Fatal("no status")
	}
	if status.ConsecutiveFails != 0 {
		t.Fatalf("expected success after retries, got ConsecutiveFails=%d (err=%s)", status.ConsecutiveFails, status.LastError)
	}
	if status.LastPollAttempts != 3 {
		t.Fatalf("expected 3 attempts (2 failures + success), got %d", status.LastPollAttempts)
	}
}

func TestScheduler_NoRetryOnAuthFailure(t *testing.T) {
	tmpDir := t.TempDir()
	db, err := storage.New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if err := db.SeedHosts([]config.Host{{Name: "locked", Connection: "ssh", Endpoint: "10.0.0.1", Port: 22, Timeout: time.Second}}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	retrieved, _ := db.GetHosts()
	host := retrieved[0]

	authFail := &errorCollector{err: errors.New("ssh: Permission denied (publickey,password)")}
	sched := NewWithRetry(30*time.Second, db, collector.NewChain(authFail), nil, config.RetryConfig{
		MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond,
	})

	sched.pollHost(context.Background(), host)

	status := sched.GetHostStatus(host.ID)
	if status == nil {
		t.Fatal("no status")
	}
	if status.LastPollAttempts != 1 {
		t.Fatalf("auth failures must not retry; got %d attempts", status.LastPollAttempts)
	}
}

func TestRetryable_Classification(t *testing.T) {
	cases := []struct {
		err         error
		retryWanted bool
	}{
		{errors.New("connection refused"), true},
		{errors.New("i/o timeout"), true},
		{errors.New("failed to parse psutil JSON"), true},
		{errors.New("Permission denied (publickey)"), false},
		{errors.New("authentication failed"), false},
		{errors.New("Host key verification failed."), false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := retryable(tc.err); got != tc.retryWanted {
			t.Errorf("retryable(%v) = %v, want %v", tc.err, got, tc.retryWanted)
		}
	}
}

func TestBackoffDelay_ExponentialWithCeiling(t *testing.T) {
	cfg := config.RetryConfig{BaseDelay: time.Second, MaxDelay: 8 * time.Second, JitterFraction: 0}

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for attempt, w := range want {
		got := backoffDelay(cfg, attempt)
		if got != w {
			t.Errorf("attempt %d: got %v, want %v", attempt, got, w)
		}
	}
}

func TestEffectivePlan_HostOverrides(t *testing.T) {
	global := config.RetryConfig{}.WithDefaults()
	max3, base500, ceil9k := int64(5), int64(500), int64(9000)
	host := storage.Host{RetryMaxRetries: &max3, RetryBaseMs: &base500, RetryMaxMs: &ceil9k}
	p := effectivePlan(global, host)
	if p.maxAttempts != 5 || p.base != 500*time.Millisecond || p.max != 9*time.Second {
		t.Fatalf("override plan wrong: %+v", p)
	}
	inherited := effectivePlan(global, storage.Host{})
	if inherited.maxAttempts != global.MaxRetries || inherited.base != global.BaseDelay {
		t.Fatalf("inheritance broken: %+v", inherited)
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

func TestScheduler_CollectorTimeoutEnforced(t *testing.T) {
	db, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	slow := int64(50) // 50ms collector budget
	if err := db.SeedHosts([]config.Host{{Name: "slow", Connection: "ssh", Endpoint: "10.0.0.1", Port: 22, Timeout: 5 * time.Second}}); err != nil {
		t.Fatal(err)
	}
	retrieved, _ := db.GetHosts()
	host := retrieved[0]
	host.CollectorTimeoutMs = &slow
	if err := db.UpdateHost(context.Background(), &host); err != nil {
		t.Fatal(err)
	}

	hang := &sleepCollector{d: 2 * time.Second}
	sched := NewWithRetry(30*time.Second, db, collector.NewChain(hang), nil, config.RetryConfig{MaxRetries: 1})

	start := time.Now()
	sched.pollHost(context.Background(), host)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("collector budget not enforced; poll took %v", elapsed)
	}
	status := sched.GetHostStatus(host.ID)
	if status == nil || status.ConsecutiveFails == 0 {
		t.Fatal("expected failure when collector exceeds budget")
	}
}

type sleepCollector struct{ d time.Duration }

func (s *sleepCollector) Name() string { return "sleep" }
func (s *sleepCollector) Collect(ctx context.Context, host collector.Host) ([]collector.Sample, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(s.d):
		return nil, errors.New("finished too late")
	}
}

func TestDurationFromMs(t *testing.T) {
	cases := []struct {
		in   *int64
		want time.Duration
	}{
		{nil, 0},
		{int64Ptr(0), 0},
		{int64Ptr(-5), 0},
		{int64Ptr(1500), 1500 * time.Millisecond},
	}
	for _, tc := range cases {
		if got := durationFromMs(tc.in); got != tc.want {
			t.Errorf("durationFromMs(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestScheduler_HealthTracking(t *testing.T) {
	db, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.SeedHosts([]config.Host{{Name: "hc", Connection: "ssh", Endpoint: "10.0.0.1", Port: 22, Timeout: time.Second}}); err != nil {
		t.Fatal(err)
	}
	host, _ := db.GetHosts()
	h := host[0]

	failThenSucceed := &flakyCollector{failFirst: 3, samples: []collector.Sample{
		{Metric: "cpu.user_pct", Value: 10, Timestamp: time.Now(), Collector: "mock"},
	}}
	sched := NewWithRetry(30*time.Second, db, collector.NewChain(failThenSucceed), nil,
		config.RetryConfig{MaxRetries: 2, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond})

	sched.pollHost(context.Background(), h)
	st := sched.GetHostStatus(h.ID)
	if st == nil || st.LastFailure.IsZero() {
		t.Fatal("expected LastFailure recorded when all attempts failed")
	}
	if st.ConsecutiveFails != 1 {
		t.Fatalf("expected 1 fail, got %d", st.ConsecutiveFails)
	}
	before := st.LastFailure

	sched.pollHost(context.Background(), h)
	st = sched.GetHostStatus(h.ID)
	if st.ConsecutiveFails != 0 {
		t.Fatalf("expected recovered, got fails=%d err=%s", st.ConsecutiveFails, st.LastError)
	}
	if !st.LastFailure.After(before) == false && st.LastFailure.Equal(before) == false {
		t.Log("failure timestamp unchanged after success (acceptable)")
	}
	if st.LastLatency <= 0 {
		t.Error("expected positive latency on successful poll")
	}
}
