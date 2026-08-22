package scheduler

import (
	"context"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/config"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

type Scheduler struct {
	interval     time.Duration
	db           *storage.DB
	collectors   *collector.Chain
	logger       *slog.Logger
	retry        config.RetryConfig
	mu           sync.RWMutex
	hostStatuses map[int64]*HostStatus
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

type HostStatus struct {
	HostID           int64
	ConsecutiveFails int
	LastSuccess      time.Time
	LastFailure      time.Time
	LastError        string
	LastCollector    string
	LastLatency      time.Duration // wall-clock duration of the most recent poll
	LastPollAttempts int           // attempts made in the most recent poll (1 = no retry)
	LastRetryTime    time.Duration // time spent backing off during the most recent poll
}

func New(interval time.Duration, db *storage.DB, collectors *collector.Chain, logger *slog.Logger) *Scheduler {
	return NewWithRetry(interval, db, collectors, logger, config.RetryConfig{}.WithDefaults())
}

func NewWithRetry(interval time.Duration, db *storage.DB, collectors *collector.Chain, logger *slog.Logger, retry config.RetryConfig) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		interval:     interval,
		db:           db,
		collectors:   collectors,
		logger:       logger,
		retry:        retry.WithDefaults(),
		hostStatuses: make(map[int64]*HostStatus),
		stopCh:       make(chan struct{}),
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	hosts, err := s.db.GetHosts()
	if err != nil {
		s.logger.Error("failed to get hosts for initial poll", "error", err)
		return
	}

	for _, h := range hosts {
		s.hostStatuses[h.ID] = &HostStatus{HostID: h.ID}
	}

	s.pollAll(ctx)

	pollTicker := time.NewTicker(s.interval)
	defer pollTicker.Stop()

	downsampleTicker := time.NewTicker(time.Minute)
	defer downsampleTicker.Stop()

	cleanupTicker := time.NewTicker(24 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case <-s.stopCh:
			s.wg.Wait()
			return
		case <-pollTicker.C:
			s.pollAll(ctx)
		case <-downsampleTicker.C:
			if err := s.db.Downsample(ctx); err != nil {
				s.logger.Error("scheduled downsample failed", "error", err)
			}
		case <-cleanupTicker.C:
			if err := s.db.Cleanup(ctx); err != nil {
				s.logger.Error("scheduled cleanup failed", "error", err)
			}
		}
	}
}

func (s *Scheduler) Stop() {
	close(s.stopCh)
}

func (s *Scheduler) pollAll(ctx context.Context) {
	hosts, err := s.db.GetHosts()
	if err != nil {
		s.logger.Error("failed to get hosts", "error", err)
		return
	}

	var wg sync.WaitGroup
	for _, h := range hosts {
		wg.Add(1)
		go func(host storage.Host) {
			defer wg.Done()
			jitter := time.Duration(rand.Int63n(int64(s.interval / 10)))
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter):
			}
			select {
			case <-ctx.Done():
				return
			default:
				s.pollHost(ctx, host)
			}
		}(h)
	}
	wg.Wait()
}

// nonRetryableMarkers are error substrings that indicate a permanent failure
// where retrying is pointless (auth, host key). Matched case-insensitively.
var nonRetryableMarkers = []string{
	"permission denied",
	"authentication failed",
	"publickey",
	"password",
	"host key verification failed",
}

// retryable reports whether an error from the collector chain is worth
// retrying (transient network/timeout/parse issues vs permanent auth/key failures).
func retryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range nonRetryableMarkers {
		if strings.Contains(msg, marker) {
			return false
		}
	}
	return true
}

// backoffDelay computes delay = min(base * 2^attempt + jitter, max).
// Uses the package-level rand source (thread-safe: pollAll fans out per-host).
func backoffDelay(cfg config.RetryConfig, attempt int) time.Duration {
	d := cfg.BaseDelay << attempt // base * 2^attempt; overflow-safe enough for sane configs
	if d <= 0 || d > cfg.MaxDelay {
		d = cfg.MaxDelay
	}
	delay := d
	if cfg.JitterFraction > 0 {
		jitter := time.Duration(rand.Float64() * float64(delay) * cfg.JitterFraction)
		delay += jitter
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}
	return delay
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// durationFromMs converts a nullable millisecond column to a Duration (0 when nil).
func durationFromMs(p *int64) time.Duration {
	if p == nil || *p <= 0 {
		return 0
	}
	return time.Duration(*p) * time.Millisecond
}

type retryPlan struct {
	maxAttempts int
	base        time.Duration
	max         time.Duration
}

// effectivePlan merges global retry config with per-host overrides.
func effectivePlan(global config.RetryConfig, host storage.Host) retryPlan {
	p := retryPlan{maxAttempts: global.MaxRetries, base: global.BaseDelay, max: global.MaxDelay}
	if host.RetryMaxRetries != nil && *host.RetryMaxRetries > 0 {
		p.maxAttempts = int(*host.RetryMaxRetries)
	}
	if host.RetryBaseMs != nil && *host.RetryBaseMs > 0 {
		p.base = time.Duration(*host.RetryBaseMs) * time.Millisecond
	}
	if host.RetryMaxMs != nil && *host.RetryMaxMs > 0 {
		p.max = time.Duration(*host.RetryMaxMs) * time.Millisecond
	}
	if p.base > p.max {
		p.max = p.base
	}
	return p
}

func (s *Scheduler) pollHost(ctx context.Context, host storage.Host) {
	start := time.Now()
	collectorHost := CollectorHostFor(host)

	collectCtx := ctx
	if host.CollectorTimeoutMs != nil && *host.CollectorTimeoutMs > 0 {
		var cancel context.CancelFunc
		collectCtx, cancel = context.WithTimeout(ctx, time.Duration(*host.CollectorTimeoutMs)*time.Millisecond)
		defer cancel()
	}

	plan := effectivePlan(s.retry, host)
	var samples []collector.Sample
	var err error
	attempts := 0
	retryTime := time.Duration(0)

	for attempt := 0; attempt < plan.maxAttempts; attempt++ {
		attempts = attempt + 1
		samples, err = s.collectors.Collect(collectCtx, collectorHost)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return
		}
		if !retryable(err) || attempt == plan.maxAttempts-1 {
			break
		}
		delay := backoffDelay(config.RetryConfig{
			BaseDelay:      plan.base,
			MaxDelay:       plan.max,
			JitterFraction: s.retry.JitterFraction,
		}, attempt)
		retryTime += delay
		s.logger.Debug("poll failed, retrying", "host", host.Name, "attempt", attempts, "delay", delay, "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
	latency := time.Since(start)

	s.mu.Lock()
	status := s.hostStatuses[host.ID]
	if status == nil {
		status = &HostStatus{HostID: host.ID}
		s.hostStatuses[host.ID] = status
	}
	status.LastPollAttempts = attempts
	status.LastRetryTime = retryTime
	status.LastLatency = latency
	if err != nil {
		status.LastFailure = time.Now()
	}
	s.mu.Unlock()

	if err != nil {
		s.logger.Warn("poll failed", "host", host.Name, "error", err, "latency", latency, "attempts", attempts)
		s.mu.Lock()
		status.ConsecutiveFails++
		status.LastError = err.Error()
		s.mu.Unlock()
		return
	}

	if err := s.db.SaveSamples(samples); err != nil {
		s.logger.Error("failed to save samples", "host", host.Name, "error", err)
		return
	}

	s.mu.Lock()
	status.ConsecutiveFails = 0
	status.LastSuccess = time.Now()
	if len(samples) > 0 {
		status.LastCollector = samples[0].Collector
	}
	s.mu.Unlock()

	s.logger.Debug("poll succeeded", "host", host.Name, "samples", len(samples), "latency", latency, "attempts", attempts, "collector", status.LastCollector)
}

func (s *Scheduler) GetHostStatus(hostID int64) *HostStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hostStatuses[hostID]
}

func (s *Scheduler) GetAllHostStatuses() map[int64]*HostStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[int64]*HostStatus, len(s.hostStatuses))
	for k, v := range s.hostStatuses {
		result[k] = v
	}
	return result
}
