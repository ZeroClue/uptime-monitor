package collector

import (
	"context"
	"log/slog"
)

type Chain struct {
	collectors []Collector
	logger     *slog.Logger
}

func NewChain(collectors ...Collector) *Chain {
	return &Chain{
		collectors: collectors,
		logger:     slog.Default(),
	}
}

func (c *Chain) Collect(ctx context.Context, host Host) ([]Sample, error) {
	var lastErr error
	for _, coll := range c.collectors {
		if host.CollectorPreference != "" && host.CollectorPreference != coll.Name() {
			continue
		}
		samples, err := coll.Collect(ctx, host)
		if err == nil {
			c.logger.Debug("collector succeeded", "host", host.Name, "collector", coll.Name())
			return samples, nil
		}
		lastErr = err
		c.logger.Debug("collector failed", "host", host.Name, "collector", coll.Name(), "error", err)
	}
	return nil, lastErr
}