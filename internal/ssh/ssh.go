package ssh

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// SSHTarget represents a target host for SSH execution.
// Contains only SSH transport concerns, not collector-specific fields.

// SSHTarget represents a target host for SSH execution.
// Contains only SSH transport concerns, not collector-specific fields.
type SSHTarget struct {
	Endpoint              string
	Port                  int
	User                  string
	KeyPath               string
	ProxyJump             string
	Timeout               time.Duration
	StrictHostKeyChecking string
	UserKnownHostsFile    string
	ConnectTimeout        time.Duration
}

// SSHTargetDefaults holds default values for SSHTarget fields.
// Used by NewSSHClient to provide sensible defaults.
type SSHTargetDefaults struct {
	StrictHostKeyChecking string
	UserKnownHostsFile    string
	ConnectTimeout        time.Duration
	DefaultPort           int
	DefaultTimeout        time.Duration
}

// SSHClient interface defines the contract for SSH command execution.
// Implementations handle transport; callers provide target and command.
type SSHClient interface {
	Exec(ctx context.Context, target *SSHTarget, cmd string) (string, error)
}

// sshClient is the default implementation of SSHClient.
// Uses system ssh binary with configurable options.
type sshClient struct {
	logger   *slog.Logger
	defaults *SSHTargetDefaults
}

// NewSSHClient creates a new SSHClient with the given logger and optional defaults.
// If defaults is nil, sensible built-in defaults are used.
func NewSSHClient(logger *slog.Logger, defaults *SSHTargetDefaults) SSHClient {
	if logger == nil {
		logger = slog.Default()
	}
	if defaults == nil {
		defaults = &SSHTargetDefaults{
			StrictHostKeyChecking: "no",
			UserKnownHostsFile:    "/dev/null",
			ConnectTimeout:        10 * time.Second,
			DefaultPort:           22,
			DefaultTimeout:        10 * time.Second,
		}
	}
	return &sshClient{
		logger:   logger,
		defaults: defaults,
	}
}

// Exec executes a command on the target host via SSH.
// Returns stdout+stderr combined output, or an error if the command fails.
func (c *sshClient) Exec(ctx context.Context, target *SSHTarget, cmd string) (string, error) {
	// Apply defaults for any zero values
	if target.Port == 0 {
		target.Port = c.defaults.DefaultPort
	}
	if target.Timeout == 0 {
		target.Timeout = c.defaults.DefaultTimeout
	}
	if target.StrictHostKeyChecking == "" {
		target.StrictHostKeyChecking = c.defaults.StrictHostKeyChecking
	}
	if target.UserKnownHostsFile == "" {
		target.UserKnownHostsFile = c.defaults.UserKnownHostsFile
	}
	if target.ConnectTimeout == 0 {
		target.ConnectTimeout = c.defaults.ConnectTimeout
	}

	args := []string{"ssh", "-o", "StrictHostKeyChecking=" + target.StrictHostKeyChecking}
	args = append(args, "-o", "UserKnownHostsFile="+target.UserKnownHostsFile)
	args = append(args, "-o", "LogLevel=ERROR")

	if target.ConnectTimeout > 0 {
		args = append(args, "-o", fmt.Sprintf("ConnectTimeout=%d", int(target.ConnectTimeout.Seconds())))
	}
	if target.Port != 0 {
		args = append(args, "-p", fmt.Sprintf("%d", target.Port))
	}
	if target.KeyPath != "" {
		args = append(args, "-i", target.KeyPath)
	}
	if target.ProxyJump != "" {
		args = append(args, "-J", target.ProxyJump)
	}
	args = append(args, fmt.Sprintf("%s@%s", target.User, target.Endpoint), cmd)

	c.logger.Debug("executing ssh", "host", target.Endpoint, "cmd", cmd)

	ctx, cancel := context.WithTimeout(ctx, target.Timeout)
	defer cancel()

	cmdExec := exec.CommandContext(ctx, args[0], args[1:]...)
	output, err := cmdExec.CombinedOutput()
	if err != nil {
		c.logger.Debug("ssh command failed", "host", target.Endpoint, "error", err, "output", string(output))
		return "", fmt.Errorf("ssh failed: %w", err)
	}
	return string(output), nil
}
