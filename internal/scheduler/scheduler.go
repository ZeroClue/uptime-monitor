package scheduler

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

type Scheduler struct {
	interval       time.Duration
	db             *storage.DB
	collectors     *collector.Chain
	logger         *slog.Logger
	mu             sync.RWMutex
	hostStatuses   map[int64]*HostStatus
	stopCh         chan struct{}
	wg             sync.WaitGroup
	lastDownsample time.Time
}

type HostStatus struct {
	HostID           int64
	ConsecutiveFails int
	LastSuccess      time.Time
	LastError        string
	LastCollector    string
}

func New(interval time.Duration, db *storage.DB, collectors *collector.Chain, logger *slog.Logger) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		interval:     interval,
		db:           db,
		collectors:   collectors,
		logger:       logger,
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

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case <-s.stopCh:
			s.wg.Wait()
			return
		case <-ticker.C:
			s.pollAll(ctx)
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

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	var wg sync.WaitGroup
	for _, h := range hosts {
		wg.Add(1)
		go func(host storage.Host) {
			defer wg.Done()
			jitter := time.Duration(rng.Int63n(int64(s.interval / 10)))
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

	now := time.Now()
	s.mu.Lock()
	shouldDownsample := s.lastDownsample.IsZero() || now.Sub(s.lastDownsample) >= time.Minute
	if shouldDownsample {
		s.lastDownsample = now.Truncate(time.Minute)
	}
	s.mu.Unlock()

	if shouldDownsample {
		if err := s.db.Downsample(ctx); err != nil {
			s.logger.Error("downsample failed", "error", err)
		}
	}
	if err := s.db.Cleanup(ctx); err != nil {
		s.logger.Error("cleanup failed", "error", err)
	}
}

func (s *Scheduler) pollHost(ctx context.Context, host storage.Host) {
	start := time.Now()
	samples, err := s.collectors.Collect(ctx, collector.Host{
		ID:                host.ID,
		Name:              host.Name,
		Connection:        host.Connection,
		Endpoint:          host.Endpoint,
		Port:              host.Port,
		User:              host.User,
		KeyPath:           host.KeyPath,
		Sudo:              host.Sudo,
		Timeout:           host.Timeout,
		ProxyJump:         host.ProxyJump,
		CollectorPreference: host.CollectorPreference,
	})
	latency := time.Since(start)

	s.mu.Lock()
	status := s.hostStatuses[host.ID]
	if status == nil {
		status = &HostStatus{HostID: host.ID}
		s.hostStatuses[host.ID] = status
	}
	s.mu.Unlock()

	if err != nil {
		s.logger.Warn("poll failed", "host", host.Name, "error", err, "latency", latency)
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

	s.logger.Debug("poll succeeded", "host", host.Name, "samples", len(samples), "latency", latency, "collector", status.LastCollector)
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