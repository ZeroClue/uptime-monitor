package ssh

import (
	"bytes"
	"context"
	"errors"
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
			StrictHostKeyChecking: "yes",
			UserKnownHostsFile:    "/config/known_hosts",
			ConnectTimeout:        10 * time.Second,
			DefaultPort:           22,
			DefaultTimeout:        30 * time.Second,
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
	return c.ExecLimited(ctx, target, cmd, 0)
}

// ExecLimited executes a command like Exec but fails once the combined
// output exceeds maxBytes. A non-positive maxBytes means unlimited.
func (c *sshClient) ExecLimited(ctx context.Context, target *SSHTarget, cmd string, maxBytes int64) (string, error) {
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

	auditNewKey := target.StrictHostKeyChecking == "accept-new"
	if auditNewKey {
		hadKey := c.knownHostsHasEntry(target.UserKnownHostsFile, target.Endpoint, target.Port)
		defer func() { c.auditAcceptedKey(hadKey, target) }()
	}

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
	var output bytes.Buffer
	if maxBytes > 0 {
		cmdExec.Stdout = &limitedWriter{w: &output, n: maxBytes}
		cmdExec.Stderr = cmdExec.Stdout
	} else {
		cmdExec.Stdout = &output
		cmdExec.Stderr = &output
	}
	err := cmdExec.Run()
	if err != nil {
		c.logger.Debug("ssh command failed", "host", target.Endpoint, "error", err, "output", output.String())
		return "", fmt.Errorf("ssh failed: %w", err)
	}
	return output.String(), nil
}

// limitedWriter writes at most n total bytes; further writes fail loudly so
// a runaway remote command is cut off instead of exhausting memory.
type limitedWriter struct {
	w *bytes.Buffer
	n int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errors.New("output exceeds limit")
	}
	written := int64(len(p))
	if written > l.n {
		l.w.Write(p[:l.n])
		l.n = 0
		return len(p), errors.New("output exceeds limit")
	}
	l.w.Write(p)
	l.n -= written
	return len(p), nil
}

// knownHostsHasEntry reports whether the endpoint already has an entry.
// Non-default ports use the [host]:port form OpenSSH writes.
func (c *sshClient) knownHostsHasEntry(file, endpoint string, port int) bool {
	hostRef := endpoint
	if port != 0 && port != 22 {
		hostRef = fmt.Sprintf("[%s]:%d", endpoint, port)
	}
	out, err := exec.Command("ssh-keygen", "-F", hostRef, "-f", file).CombinedOutput()
	return err == nil && len(out) > 0
}

// auditAcceptedKey logs newly learned host keys for audit purposes.
func (c *sshClient) auditAcceptedKey(hadKey bool, target *SSHTarget) {
	if hadKey {
		return
	}
	if c.knownHostsHasEntry(target.UserKnownHostsFile, target.Endpoint, target.Port) {
		c.logger.Info("accepted new host key",
			"host", target.Endpoint,
			"port", target.Port,
			"known_hosts_file", target.UserKnownHostsFile,
			"policy", "auto")
	}
}
