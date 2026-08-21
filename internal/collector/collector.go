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

// appendCPUSampleGroup appends the standard user/system/idle/iowait percentage
// quartet for a CPU (aggregate or per-core) under the given metric prefix.
func appendCPUSampleGroup(samples []Sample, hostID int64, prefix string, user, system, idle, iowait float64, ts time.Time) []Sample {
	return append(samples,
		Sample{HostID: hostID, Metric: prefix + ".user_pct", Value: user, Timestamp: ts},
		Sample{HostID: hostID, Metric: prefix + ".system_pct", Value: system, Timestamp: ts},
		Sample{HostID: hostID, Metric: prefix + ".idle_pct", Value: idle, Timestamp: ts},
		Sample{HostID: hostID, Metric: prefix + ".iowait_pct", Value: iowait, Timestamp: ts},
	)
}

type Collector interface {
	Name() string
	Collect(ctx context.Context, host Host) ([]Sample, error)
}

var ErrCollectorFailed = errors.New("collector failed")

type Host struct {
	ID                  int64
	Name                string
	Connection          string
	Endpoint            string
	Port                int
	User                string
	KeyPath             string
	Sudo                bool
	Timeout             time.Duration // per-command execution budget
	SSHTimeout          time.Duration // connection phase budget; 0 = SSH client default (10s)
	CollectorTimeout    time.Duration // whole-collect budget; 0 = scheduler default (30s)
	ProxyJump           string
	CollectorPreference string
}
