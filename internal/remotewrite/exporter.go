package remotewrite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

const (
	defaultQueueCapacity = 50000
	defaultBatchSize     = 500
	defaultTimeoutMs     = 10000
	maxBackoff           = 30 * time.Second
	jobLabel             = "uptime-monitor"
)

// Exporter batches collected samples and ships them to a remote-write
// endpoint. Run drives the flush loop; Enqueue is called by the scheduler
// for every successful poll.
type Exporter struct {
	db     *storage.DB // may be nil in tests that never tick
	logger *slog.Logger
	client *http.Client
	queue  *Queue

	enabled       atomic.Bool
	sentTotal     atomic.Int64 // samples accepted by the endpoint
	failedBatches atomic.Int64
	lastSuccess   atomic.Int64 // unix seconds

	flushInterval time.Duration // overridable for tests
	backoffBase   time.Duration
	maxAttempts   int
}

func NewExporter(db *storage.DB, logger *slog.Logger) *Exporter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Exporter{
		db:            db,
		logger:        logger,
		client:        &http.Client{},
		queue:         NewQueue(defaultQueueCapacity),
		flushInterval: 10 * time.Second,
		backoffBase:   time.Second,
		maxAttempts:   5,
	}
}

func (e *Exporter) QueueDepth() int     { return e.queue.Depth() }
func (e *Exporter) DroppedTotal() int64 { return e.queue.DroppedTotal() }

// Enqueue buffers one poll's samples; a no-op while remote write is
// disabled so nothing accumulates behind an unused feature.
func (e *Exporter) Enqueue(hostName string, samples []collector.Sample) {
	if !e.enabled.Load() {
		return
	}
	for _, s := range samples {
		e.queue.Push(queuedSample{
			HostName:    hostName,
			Metric:      s.Metric,
			Value:       s.Value,
			TimestampMs: s.Timestamp.UnixMilli(),
			Collector:   s.Collector,
		})
	}
}

// Run flushes on a fixed interval until ctx is done. The config is re-read
// each tick so dashboard edits apply without a restart.
func (e *Exporter) Run(ctx context.Context) {
	e.tick(ctx) // pick up enabled state immediately; no startup sample loss
	ticker := time.NewTicker(e.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.tick(ctx)
		}
	}
}

// tick reloads config and, when enabled, delivers one batch.
func (e *Exporter) tick(ctx context.Context) {
	cfg := e.loadConfig(ctx)
	if cfg == nil || !cfg.Enabled || cfg.URL == "" {
		e.enabled.Store(false)
		return
	}
	e.enabled.Store(true)
	batchCtx, cancel := context.WithTimeout(ctx, configTimeout(cfg))
	if err := e.flushOnce(batchCtx, cfg); err != nil {
		e.logger.Warn("remote write batch failed", "error", err)
	}
	cancel()
}

func (e *Exporter) loadConfig(ctx context.Context) *storage.RemoteWriteConfig {
	if e.db == nil {
		return nil
	}
	cfg, err := e.db.GetRemoteWriteConfig(ctx)
	if err != nil {
		e.logger.Warn("failed to load remote write config", "error", err)
		return nil
	}
	return cfg
}

func configTimeout(c *storage.RemoteWriteConfig) time.Duration {
	if c.TimeoutMs <= 0 {
		return defaultTimeoutMs * time.Millisecond
	}
	return time.Duration(c.TimeoutMs) * time.Millisecond
}

func configBatchSize(c *storage.RemoteWriteConfig) int {
	if c.BatchSize <= 0 {
		return defaultBatchSize
	}
	return c.BatchSize
}

type creds struct {
	authType string
	username string
	password string
	token    string
}

func credsFor(c *storage.RemoteWriteConfig) creds {
	return creds{authType: c.AuthType, username: c.Username, password: c.Password, token: c.BearerToken}
}

// flushOnce pops up to one batch and delivers it with retries. A batch that
// exhausts its attempts is dropped (counted), never requeued, so a poisoned
// payload cannot wedge the queue.
func (e *Exporter) flushOnce(ctx context.Context, cfg *storage.RemoteWriteConfig) error {
	items := e.queue.PopBatch(configBatchSize(cfg))
	if len(items) == 0 {
		return nil
	}

	projectByHost := e.hostProjects(ctx)
	payload := SnappyEncode(nil, EncodeWriteRequest(buildRequest(items, cfg.ExtraLabels, projectByHost)))

	var lastErr error
	for attempt := 0; attempt < e.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = e.send(ctx, cfg.URL, credsFor(cfg), payload)
		if lastErr == nil {
			e.sentTotal.Add(int64(len(items)))
			e.lastSuccess.Store(time.Now().Unix())
			return nil
		}
		if !isRetryable(lastErr) {
			break
		}
		delay := e.backoffBase << uint(attempt)
		if delay > maxBackoff || delay <= 0 {
			delay = maxBackoff
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	e.failedBatches.Add(1)
	e.queue.DroppedTotal()
	return fmt.Errorf("dropping batch of %d samples after %d attempts: %w", len(items), e.maxAttempts, lastErr)
}

// buildRequest converts queued samples into series, grouping samples that
// share a label set into one TimeSeries.
func buildRequest(items []queuedSample, extra map[string]string, projectByHost map[string]string) WriteRequest {
	grouped := map[string][]Sample{}
	order := []string{}
	seriesMeta := map[string]TimeSeries{}

	for _, it := range items {
		labels := []Label{{Name: "__name__", Value: it.Metric}, {Name: "host", Value: it.HostName}}
		if p, ok := projectByHost[it.HostName]; ok && p != "" {
			labels = append(labels, Label{Name: "project", Value: p})
		}
		if it.Collector != "" {
			labels = append(labels, Label{Name: "collector", Value: it.Collector})
		}
		labels = append(labels, Label{Name: "job", Value: jobLabel})
		for k, v := range extra {
			labels = append(labels, Label{Name: k, Value: v})
		}

		var sb strings.Builder
		for _, l := range labels {
			sb.WriteString(l.Name)
			sb.WriteByte('=')
			sb.WriteString(l.Value)
			sb.WriteByte(',')
		}
		id := sb.String()

		if _, seen := seriesMeta[id]; !seen {
			seriesMeta[id] = TimeSeries{Labels: labels}
			order = append(order, id)
		}
		grouped[id] = append(grouped[id], Sample{Value: it.Value, TimestampMs: it.TimestampMs})
	}

	req := WriteRequest{TimeSeries: make([]TimeSeries, 0, len(order))}
	for _, id := range order {
		ts := seriesMeta[id]
		ts.Samples = grouped[id]
		req.TimeSeries = append(req.TimeSeries, ts)
	}
	return req
}

// hostProjects maps host name -> project name for labeling; failures leave
// the map empty rather than blocking export.
func (e *Exporter) hostProjects(ctx context.Context) map[string]string {
	out := map[string]string{}
	if e.db == nil {
		return out
	}
	hosts, err := e.db.GetHostsByProject(ctx, nil)
	if err != nil {
		return out
	}
	nameByID := map[int64]string{}
	if projects, err := e.db.GetProjects(ctx); err == nil {
		for _, p := range projects {
			nameByID[p.ID] = p.Name
		}
	}
	for _, h := range hosts {
		if h.ProjectID != nil {
			if name, ok := nameByID[*h.ProjectID]; ok {
				out[h.Name] = name
			}
		}
	}
	return out
}

type permanentStatusError struct{ code int }

func (p *permanentStatusError) Error() string {
	return "permanent remote write failure: HTTP " + strconv.Itoa(p.code)
}

func isRetryable(err error) bool {
	var perm *permanentStatusError
	return err != nil && !errors.As(err, &perm)
}

// send performs one delivery attempt. 2xx succeeds; other 4xx are permanent;
// everything else (5xx, 429, network) is retryable.
func (e *Exporter) send(ctx context.Context, url string, auth creds, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	switch auth.authType {
	case "basic":
		req.SetBasicAuth(auth.username, auth.password)
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+auth.token)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		return &permanentStatusError{code: resp.StatusCode}
	}
	return fmt.Errorf("remote write endpoint returned HTTP %d", resp.StatusCode)
}

// Metrics is a snapshot of exporter health for self-monitoring.
type Metrics struct {
	SentSamples     int64
	FailedBatches   int64
	DroppedSamples  int64
	QueueDepth      int
	LastSuccessUnix int64
}

func (e *Exporter) Snapshot() Metrics {
	return Metrics{
		SentSamples:     e.sentTotal.Load(),
		FailedBatches:   e.failedBatches.Load(),
		DroppedSamples:  e.queue.DroppedTotal(),
		QueueDepth:      e.queue.Depth(),
		LastSuccessUnix: e.lastSuccess.Load(),
	}
}

// RenderMetrics formats a snapshot as Prometheus text exposition.
func RenderMetrics(m Metrics) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# HELP uptime_remote_write_sent_samples_total Samples accepted by the remote write endpoint.")
	fmt.Fprintln(&b, "# TYPE uptime_remote_write_sent_samples_total counter")
	fmt.Fprintf(&b, "uptime_remote_write_sent_samples_total %d\n", m.SentSamples)
	fmt.Fprintln(&b, "# HELP uptime_remote_write_failed_batches_total Batches dropped after exhausting send attempts.")
	fmt.Fprintln(&b, "# TYPE uptime_remote_write_failed_batches_total counter")
	fmt.Fprintf(&b, "uptime_remote_write_failed_batches_total %d\n", m.FailedBatches)
	fmt.Fprintln(&b, "# HELP uptime_remote_write_dropped_samples_total Samples dropped because the queue was full.")
	fmt.Fprintln(&b, "# TYPE uptime_remote_write_dropped_samples_total counter")
	fmt.Fprintf(&b, "uptime_remote_write_dropped_samples_total %d\n", m.DroppedSamples)
	fmt.Fprintln(&b, "# HELP uptime_remote_write_queue_depth Samples currently buffered.")
	fmt.Fprintln(&b, "# TYPE uptime_remote_write_queue_depth gauge")
	fmt.Fprintf(&b, "uptime_remote_write_queue_depth %d\n", m.QueueDepth)
	fmt.Fprintln(&b, "# HELP uptime_remote_write_last_success_timestamp_seconds Unix time of the last accepted batch; 0 = never.")
	fmt.Fprintln(&b, "# TYPE uptime_remote_write_last_success_timestamp_seconds gauge")
	fmt.Fprintf(&b, "uptime_remote_write_last_success_timestamp_seconds %d\n", m.LastSuccessUnix)
	return b.String()
}
