package remotewrite

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

func TestQueue_DropsOldestOnOverflow(t *testing.T) {
	q := NewQueue(3)
	for i := 0; i < 5; i++ {
		q.Push(queuedSample{Metric: fmt.Sprintf("m%d", i)})
	}
	if q.Depth() != 3 {
		t.Fatalf("depth: want 3 got %d", q.Depth())
	}
	if q.DroppedTotal() != 2 {
		t.Fatalf("dropped: want 2 got %d", q.DroppedTotal())
	}
	batch := q.PopBatch(10)
	if batch[0].Metric != "m2" || batch[2].Metric != "m4" {
		t.Errorf("oldest not dropped first: %+v", batch)
	}
}

func TestQueue_ConcurrentPushPop(t *testing.T) {
	q := NewQueue(64)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				q.Push(queuedSample{})
				q.PopBatch(7)
			}
		}(i)
	}
	wg.Wait()
	if q.Depth() < 0 || q.Depth() > 64 {
		t.Errorf("depth out of bounds: %d", q.Depth())
	}
}

func TestSend_HeadersAndAuth(t *testing.T) {
	cases := []struct {
		name       string
		authType   string
		user, pass string
		token      string
		wantAuth   string
	}{
		{name: "none", authType: "", wantAuth: ""},
		{name: "basic", authType: "basic", user: "prom", pass: "pw",
			wantAuth: "Basic cHJvbTpwdw=="}, // base64("prom:pw")
		{name: "bearer", authType: "bearer", token: "tok-9",
			wantAuth: "Bearer tok-9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotReq *http.Request
			var gotBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotReq = r
				gotBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			e := NewExporter(nil, discardLogger())
			payload := SnappyEncode(nil, []byte("probe"))
			err := e.send(context.Background(), srv.URL, authFromConfig(&storage.RemoteWriteConfig{
				AuthType: tc.authType, Username: tc.user, Password: tc.pass, BearerToken: tc.token,
			}), payload)
			if err != nil {
				t.Fatalf("send: %v", err)
			}
			if got := gotReq.Header.Get("Content-Type"); got != "application/x-protobuf" {
				t.Errorf("content-type: %q", got)
			}
			if got := gotReq.Header.Get("Content-Encoding"); got != "snappy" {
				t.Errorf("content-encoding: %q", got)
			}
			if got := gotReq.Header.Get("X-Prometheus-Remote-Write-Version"); got == "" {
				t.Error("missing remote-write version header")
			}
			if got := gotReq.Header.Get("Authorization"); got != tc.wantAuth {
				t.Errorf("authorization: want %q got %q", tc.wantAuth, got)
			}
			if !bytes.Equal(gotBody, payload) {
				t.Error("payload corrupted in transit")
			}
		})
	}
}

func TestSend_PermanentVsRetryable(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	e := NewExporter(nil, discardLogger())
	e.maxAttempts = 4
	e.backoffBase = time.Millisecond
	e.queue.Push(queuedSample{HostName: "h", Metric: "cpu.load_1m", Value: 1})
	cfg := &storage.RemoteWriteConfig{URL: srv.URL, BatchSize: 10, Enabled: true}
	err := e.flushOnce(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected eventual error")
	}
	if attempts != 1 {
		t.Errorf("400 must be permanent: want 1 attempt, got %d", attempts)
	}
	if e.failedBatches.Load() != 1 {
		t.Errorf("failed batches: %d", e.failedBatches.Load())
	}
}

func TestFlushOnce_RetriesThenSucceeds(t *testing.T) {
	attempts := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	e := NewExporter(nil, discardLogger())
	e.maxAttempts = 5
	e.backoffBase = time.Millisecond
	e.queue.Push(queuedSample{HostName: "h", Metric: "cpu.load_1m", Value: 1, Collector: "local"})
	cfg := &storage.RemoteWriteConfig{URL: srv.URL, BatchSize: 10, Enabled: true}

	if err := e.flushOnce(context.Background(), cfg); err != nil {
		t.Fatalf("flush: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Errorf("want 3 attempts, got %d", attempts)
	}
	if e.sentTotal.Load() != 1 || e.failedBatches.Load() != 0 {
		t.Errorf("counters: sent=%d failed=%d", e.sentTotal.Load(), e.failedBatches.Load())
	}
	if e.lastSuccess.Load() == 0 {
		t.Error("last success not recorded")
	}
}

// snappyDecodeLiterals reverses the literal-only block encoding, failing on
// any copy chunk so tests notice if the encoder starts emitting them.
func snappyDecodeLiterals(t *testing.T, b []byte) []byte {
	t.Helper()
	total, n := binary.Uvarint(b)
	if n <= 0 {
		t.Fatal("bad preamble")
	}
	b = b[n:]
	var out []byte
	for len(b) > 0 {
		tag := b[0]
		if tag&3 != 0 {
			t.Fatalf("unexpected compressed chunk tag %#x", tag)
		}
		upper := tag >> 2
		litLen := 0
		if upper < 60 {
			litLen = int(upper) + 1
			b = b[1:]
		} else {
			extra := int(upper) - 59
			var v uint64
			for i := 0; i < extra; i++ {
				v |= uint64(b[1+i]) << (8 * i)
			}
			litLen = int(v) + 1
			b = b[1+extra:]
		}
		out = append(out, b[:litLen]...)
		b = b[litLen:]
	}
	if uint64(len(out)) != total {
		t.Fatalf("length mismatch: preamble %d rebuilt %d", total, len(out))
	}
	return out
}

func TestEnqueueDisabledIsNoop(t *testing.T) {
	e := NewExporter(nil, discardLogger())
	e.Enqueue("host", []collector.Sample{{Metric: "m"}})
	if e.queue.Depth() != 0 {
		t.Error("disabled exporter must not buffer samples")
	}
}

func TestFlushOnce_EndToEndPayload(t *testing.T) {
	db, err := storage.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	proj, err := db.CreateProject(ctx, &storage.Project{Name: "prod", Type: "explicit"})
	if err != nil {
		t.Fatal(err)
	}
	hostID, err := db.CreateHost(ctx, &storage.Host{Name: "web-01", Connection: "ssh",
		Endpoint: "10.0.0.1", Port: 22, ProjectID: &proj})
	if err != nil {
		t.Fatal(err)
	}

	var decoded WriteRequest
	ready := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		raw := snappyDecodeLiterals(t, body)
		decoded = parseWriteRequest(t, raw)
		once.Do(func() { close(ready) })
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := &storage.RemoteWriteConfig{
		URL: srv.URL, BatchSize: 10, Enabled: true,
		ExtraLabels: map[string]string{"env": "staging"},
	}

	e := NewExporter(db, discardLogger())
	e.enabled.Store(true) // normally set by Run's first tick
	ts := time.Now()
	e.Enqueue("web-01", []collector.Sample{
		{HostID: hostID, Metric: "cpu.user_pct", Value: 41.5, Timestamp: ts, Collector: "psutil"},
	})

	if err := e.flushOnce(context.Background(), cfg); err != nil {
		t.Fatalf("flush: %v", err)
	}
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("receiver never saw a request")
	}

	if len(decoded.TimeSeries) != 1 {
		t.Fatalf("want 1 series, got %d", len(decoded.TimeSeries))
	}
	got := decoded.TimeSeries[0]
	labels := map[string]string{}
	for _, l := range got.Labels {
		labels[l.Name] = l.Value
	}
	want := map[string]string{
		"__name__":  "cpu.user_pct",
		"host":      "web-01",
		"project":   "prod",
		"collector": "psutil",
		"job":       "uptime-monitor",
		"env":       "staging",
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("label %s: want %q got %q", k, v, labels[k])
		}
	}
	if len(got.Samples) != 1 || got.Samples[0].Value != 41.5 ||
		got.Samples[0].TimestampMs != ts.UnixMilli() {
		t.Errorf("sample mismatch: %+v (want value 41.5 ts %d)", got.Samples, ts.UnixMilli())
	}
}

func TestSnapshot_CountersExposed(t *testing.T) {
	e := NewExporter(nil, discardLogger())
	e.queue.Push(queuedSample{})
	e.sentTotal.Store(7)
	e.failedBatches.Store(2)
	e.lastSuccess.Store(123)

	m := e.Snapshot()
	if m.SentSamples != 7 || m.FailedBatches != 2 || m.QueueDepth != 1 || m.LastSuccessUnix != 123 {
		t.Errorf("snapshot: %+v", m)
	}
}

func TestRenderMetrics_TextFormat(t *testing.T) {
	m := Metrics{SentSamples: 7, FailedBatches: 2, DroppedSamples: 1, QueueDepth: 3, LastSuccessUnix: 123}
	out := RenderMetrics(m)
	for _, want := range []string{
		"uptime_remote_write_sent_samples_total 7",
		"uptime_remote_write_failed_batches_total 2",
		"uptime_remote_write_dropped_samples_total 1",
		"uptime_remote_write_queue_depth 3",
		"uptime_remote_write_last_success_timestamp_seconds 123",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// --- helpers ---

func parseWriteRequest(t *testing.T, raw []byte) WriteRequest {
	t.Helper()
	req := WriteRequest{}
	for _, f := range decodeFields(t, raw) {
		if f.num != 1 {
			continue
		}
		var ts TimeSeries
		for _, sf := range decodeFields(t, f.data) {
			switch {
			case sf.num == 1:
				var l Label
				for _, lf := range decodeFields(t, sf.data) {
					if lf.num == 1 {
						l.Name = string(lf.data)
					} else if lf.num == 2 {
						l.Value = string(lf.data)
					}
				}
				ts.Labels = append(ts.Labels, l)
			case sf.num == 2:
				var s Sample
				for _, v := range decodeFields(t, sf.data) {
					if v.num == 1 {
						s.Value = v.f64
					} else if v.num == 2 {
						s.TimestampMs = int64(v.val)
					}
				}
				ts.Samples = append(ts.Samples, s)
			}
		}
		req.TimeSeries = append(req.TimeSeries, ts)
	}
	return req
}

func authFromConfig(c *storage.RemoteWriteConfig) creds {
	return credsFor(c)
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
