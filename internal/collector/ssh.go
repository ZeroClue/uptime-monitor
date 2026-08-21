package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/ssh"
)

// execSSH is a thin wrapper for backward compatibility during migration.
// It delegates to the SSHClient adapter.
func execSSH(ctx context.Context, logger *slog.Logger, host Host, cmd string, timeout time.Duration) (string, error) {
	client := ssh.NewSSHClient(logger, nil)
	target := SSHTargetFromHost(host)
	return client.Exec(ctx, target, cmd)
}

// SSHTargetFromHost converts a collector.Host to an ssh.SSHTarget.
// This is the single point of mapping between collector and SSH concerns.
func SSHTargetFromHost(host Host) *ssh.SSHTarget {
	connectTimeout := 10 * time.Second // default; overridable per host
	if host.SSHTimeout > 0 {
		connectTimeout = host.SSHTimeout
	}
	return &ssh.SSHTarget{
		Endpoint:              host.Endpoint,
		Port:                  host.Port,
		User:                  host.User,
		KeyPath:               host.KeyPath,
		ProxyJump:             host.ProxyJump,
		Timeout:               host.Timeout,
		StrictHostKeyChecking: "no",
		UserKnownHostsFile:    "/dev/null",
		ConnectTimeout:        connectTimeout,
	}
}
