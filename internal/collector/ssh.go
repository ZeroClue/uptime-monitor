package collector

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

func execSSH(ctx context.Context, logger *slog.Logger, host Host, cmd string, timeout time.Duration) (string, error) {
	args := []string{"ssh", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null"}
	if timeout > 0 {
		args = append(args, "-o", fmt.Sprintf("ConnectTimeout=%d", int(timeout.Seconds())))
	}
	if host.Port != 0 {
		args = append(args, "-p", fmt.Sprintf("%d", host.Port))
	}
	if host.KeyPath != "" {
		args = append(args, "-i", host.KeyPath)
	}
	if host.ProxyJump != "" {
		args = append(args, "-J", host.ProxyJump)
	}
	args = append(args, fmt.Sprintf("%s@%s", host.User, host.Endpoint), cmd)

	logger.Debug("executing ssh", "host", host.Name, "cmd", cmd)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmdExec := exec.CommandContext(ctx, args[0], args[1:]...)
	output, err := cmdExec.CombinedOutput()
	if err != nil {
		logger.Debug("ssh command failed", "host", host.Name, "error", err, "output", string(output))
		return "", fmt.Errorf("ssh failed: %w", err)
	}
	return string(output), nil
}