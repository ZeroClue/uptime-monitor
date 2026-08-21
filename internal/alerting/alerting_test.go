package alerting

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

func TestExceedsThreshold(t *testing.T) {
	tests := []struct {
		name      string
		value     float64
		threshold float64
		below     bool
		want      bool
	}{
		{"above-over", 95, 90, false, true},
		{"above-exact", 90, 90, false, true},
		{"above-under", 85, 90, false, false},
		{"below-under", 30, 60, true, true},
		{"below-exact", 60, 60, true, true},
		{"below-over", 300, 60, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exceedsThreshold(tt.value, tt.threshold, tt.below); got != tt.want {
				t.Errorf("exceedsThreshold(%v, %v, %v) = %v, want %v", tt.value, tt.threshold, tt.below, got, tt.want)
			}
		})
	}
}

func TestRuleMatchesProject(t *testing.T) {
	projA := int64(1)
	projB := int64(2)
	cases := []struct {
		name  string
		rule  storage.AlertRule
		hostP *int64
		want  bool
	}{
		{"global rule matches unassigned host", storage.AlertRule{}, nil, true},
		{"global rule matches host in project", storage.AlertRule{}, &projA, true},
		{"project rule misses unassigned host", storage.AlertRule{ProjectID: &projA}, nil, false},
		{"project rule hits same project", storage.AlertRule{ProjectID: &projA}, &projA, true},
		{"project rule misses other project", storage.AlertRule{ProjectID: &projA}, &projB, false},
	}
	for _, tc := range cases {
		if got := ruleMatchesProject(tc.rule, tc.hostP); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestChannelsForProject(t *testing.T) {
	pA, pB := int64(1), int64(2)
	channels := []storage.NotificationChannel{
		{ID: 1, Name: "global", Enabled: true},
		{ID: 2, Name: "proj-a", ProjectID: &pA, Enabled: true},
		{ID: 3, Name: "proj-b", ProjectID: &pB, Enabled: true},
	}

	cases := []struct {
		name    string
		hostP   *int64
		wantIDs []int64
	}{
		{"unassigned host -> global only", nil, []int64{1}},
		{"host in A -> global + A", &pA, []int64{1, 2}},
		{"host in B -> global + B", &pB, []int64{1, 3}},
	}
	for _, tc := range cases {
		got := channelsForProject(channels, tc.hostP)
		ids := make([]int64, 0, len(got))
		for _, c := range got {
			ids = append(ids, c.ID)
		}
		if len(ids) != len(tc.wantIDs) {
			t.Errorf("%s: got %v, want %v", tc.name, ids, tc.wantIDs)
			continue
		}
		for i := range ids {
			if ids[i] != tc.wantIDs[i] {
				t.Errorf("%s: got %v, want %v", tc.name, ids, tc.wantIDs)
			}
		}
	}
}

func TestDelivery_ProjectScopedChannels(t *testing.T) {
	db, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}

	var hitsA, hitsB, hitsGlobal int32
	srvA, srvB, srvG := webhookCounter(&hitsA), webhookCounter(&hitsB), webhookCounter(&hitsGlobal)
	defer srvA.Close()
	defer srvB.Close()
	defer srvG.Close()

	ctx := context.Background()
	projA, _ := db.CreateProject(ctx, &storage.Project{Name: "delivery-a", Type: "explicit"})
	projB, _ := db.CreateProject(ctx, &storage.Project{Name: "delivery-b", Type: "explicit"})

	hostA, _ := db.CreateHost(ctx, &storage.Host{Name: "host-a", Connection: "ssh", Endpoint: "a", Port: 22})
	hostB, _ := db.CreateHost(ctx, &storage.Host{Name: "host-b", Connection: "ssh", Endpoint: "b", Port: 22})
	db.ExecContext(ctx, `UPDATE hosts SET project_id = ? WHERE id = ?`, projA, hostA)
	db.ExecContext(ctx, `UPDATE hosts SET project_id = ? WHERE id = ?`, projB, hostB)

	mustChan := func(name string, url string, proj *int64) {
		if _, err := db.CreateNotificationChannel(ctx, &storage.NotificationChannel{
			Name: name, Type: "webhook", Config: `{"url":"` + url + `"}`, Enabled: true, ProjectID: proj,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mustChan("chan-a", srvA.URL, &projA)
	mustChan("chan-b", srvB.URL, &projB)
	mustChan("chan-global", srvG.URL, nil)

	engine := NewEngine(db, nil, newAlertLogger())
	if err := engine.refreshFromDB(); err != nil {
		t.Fatal(err)
	}

	// Fire an alert for host A directly: delivery filtering is the unit under
	// test (threshold evaluation has its own coverage).
	engine.fireAlert(ctx, &projA, storage.Alert{
		HostID:   hostA,
		Type:     "metric_threshold",
		Metric:   "cpu.user_pct",
		Severity: "critical",
		Message:  "cpu high",
		Value:    95,
		FiredAt:  time.Now(),
	})

	waitFor(t, 2*time.Second, func() bool { return atomic.LoadInt32(&hitsA) > 0 })

	if atomic.LoadInt32(&hitsB) != 0 {
		t.Error("project-B channel must not receive host-A alerts")
	}
	if atomic.LoadInt32(&hitsGlobal) == 0 {
		t.Error("global channel should receive host-A alerts")
	}
}

func newAlertLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func webhookCounter(n *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(n, 1)
		w.WriteHeader(200)
	}))
}

func waitFor(t *testing.T, d time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
