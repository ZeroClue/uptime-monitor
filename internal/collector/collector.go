package collector

import (
	"context"
	"errors"
	"time"
)

type Sample struct {
	HostID    int64
	Metric    string
	Value     float64
	Timestamp time.Time
	Collector string
}

type Collector interface {
	Name() string
	Collect(ctx context.Context, host Host) ([]Sample, error)
}

var ErrCollectorFailed = errors.New("collector failed")

type Host struct {
	ID                int64
	Name              string
	Connection        string
	Endpoint          string
	Port              int
	User              string
	KeyPath           string
	Sudo              bool
	Timeout           time.Duration
	ProxyJump         string
	CollectorPreference string
}